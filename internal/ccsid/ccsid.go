// Package ccsid converts between Go strings and IBM i CCSID encoded bytes.
//
// The single-byte, double-byte and mixed-byte conversion tables in tables/
// are extracted from JTOpen's ConvTable classes (see gen/CcsidDump.java), so
// every CCSID JTOpen ships a table for is supported with identical mappings.
// The Unicode CCSIDs (1200, 1201, 1202, 1208, 13488, 17584, 21680) are
// converted algorithmically.
//
// Like JTOpen, characters that cannot be represented in the target CCSID are
// replaced by the CCSID's substitution character instead of failing.
package ccsid

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// The tables are regenerated from a JTOpen checkout with:
//
//	javac -d classes -sourcepath $JTOPEN/src/main/java gen/*.java $JTOPEN/src/main/java/com/ibm/as400/access/ConvTable[0-9]*.java
//	java -cp classes com.ibm.as400.access.CcsidDump tables <ccsid>...

//go:embed tables/*.bin
var tableFS embed.FS

// ErrUnsupported is returned for CCSIDs without a known conversion.
var ErrUnsupported = errors.New("ccsid: unsupported CCSID")

// Codec converts text for one CCSID.
type Codec interface {
	// CCSID returns the CCSID handled by the codec.
	CCSID() int
	// Decode converts CCSID encoded bytes to a string.
	Decode(data []byte) (string, error)
	// Encode converts a string to CCSID encoded bytes.
	Encode(text string) ([]byte, error)
	// Space returns the encoding of the blank used to pad fixed-length fields.
	Space() []byte
}

const (
	sbSubUnicode = 0x1A
	dbSubUnicode = 0xFFFD
	dbSubChar    = 0xFEFE
	shiftOut     = 0x0E
	shiftIn      = 0x0F
	euro         = 0x20AC
)

var (
	cacheMu sync.Mutex
	cache   = map[int]Codec{}
)

// Lookup returns the codec for ccsid. Tables are loaded on first use.
func Lookup(ccsid int) (Codec, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return lookupLocked(ccsid)
}

func lookupLocked(ccsid int) (Codec, error) {
	if c, ok := cache[ccsid]; ok {
		return c, nil
	}
	c, err := load(ccsid)
	if err != nil {
		return nil, err
	}
	cache[ccsid] = c
	return c, nil
}

// Supported reports whether ccsid can be converted.
func Supported(ccsid int) bool {
	if _, ok := unicodeCodec(ccsid); ok {
		return true
	}
	_, err := tableFS.Open(tableName(ccsid))
	return err == nil
}

// List returns all supported CCSIDs in ascending order. Internal component
// tables of mixed CCSIDs (numbers above 65535) are not included.
func List() []int {
	out := []int{1200, 1201, 1202, 1208, 13488, 17584, 21680}
	entries, _ := tableFS.ReadDir("tables")
	for _, e := range entries {
		n, err := strconv.Atoi(strings.TrimSuffix(e.Name(), ".bin"))
		if err == nil && n <= 0xFFFF {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// Decode converts data from ccsid to a string.
func Decode(data []byte, ccsid int) (string, error) {
	c, err := Lookup(ccsid)
	if err != nil {
		return "", err
	}
	return c.Decode(data)
}

// Encode converts text to ccsid.
func Encode(text string, ccsid int) ([]byte, error) {
	c, err := Lookup(ccsid)
	if err != nil {
		return nil, err
	}
	return c.Encode(text)
}

func tableName(ccsid int) string {
	return "tables/" + strconv.Itoa(ccsid) + ".bin"
}

func load(ccsid int) (Codec, error) {
	if c, ok := unicodeCodec(ccsid); ok {
		return c, nil
	}
	f, err := tableFS.Open(tableName(ccsid))
	if err != nil {
		return nil, fmt.Errorf("%w %d", ErrUnsupported, ccsid)
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("ccsid %d: corrupt table: %w", ccsid, err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("ccsid %d: corrupt table: %w", ccsid, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("ccsid %d: empty table", ccsid)
	}
	switch raw[0] {
	case 'S':
		return parseSingle(ccsid, raw[1:])
	case 'D':
		return parseDouble(ccsid, raw[1:])
	case 'M':
		if len(raw) < 9 {
			return nil, fmt.Errorf("ccsid %d: corrupt mixed table", ccsid)
		}
		sb, err := lookupLocked(int(binary.BigEndian.Uint32(raw[1:5])))
		if err != nil {
			return nil, err
		}
		db, err := lookupLocked(int(binary.BigEndian.Uint32(raw[5:9])))
		if err != nil {
			return nil, err
		}
		s, ok1 := sb.(*singleByte)
		d, ok2 := db.(*doubleByte)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("ccsid %d: invalid mixed table components", ccsid)
		}
		return &mixedByte{ccsid: ccsid, sb: s, db: d}, nil
	default:
		return nil, fmt.Errorf("ccsid %d: unknown table kind %q", ccsid, raw[0])
	}
}

// singleByte is an SBCS (EBCDIC, ASCII or bidi) table.
type singleByte struct {
	ccsid     int
	sub       byte
	toUnicode [256]uint16
	fromUni   []byte // 65536 entries
}

func parseSingle(ccsid int, raw []byte) (*singleByte, error) {
	if len(raw) != 1+512+65536 {
		return nil, fmt.Errorf("ccsid %d: corrupt single-byte table", ccsid)
	}
	t := &singleByte{ccsid: ccsid, sub: raw[0], fromUni: raw[513:]}
	for i := range t.toUnicode {
		t.toUnicode[i] = binary.BigEndian.Uint16(raw[1+i*2:])
	}
	return t, nil
}

func (t *singleByte) CCSID() int { return t.ccsid }

func (t *singleByte) Decode(data []byte) (string, error) {
	var sb strings.Builder
	sb.Grow(len(data))
	for _, b := range data {
		sb.WriteRune(rune(t.toUnicode[b]))
	}
	return sb.String(), nil
}

func (t *singleByte) Encode(text string) ([]byte, error) {
	out := make([]byte, 0, len(text))
	for _, r := range text {
		if r > 0xFFFF {
			// JTOpen converts per UTF-16 code unit.
			hi, lo := utf16.EncodeRune(r)
			out = append(out, t.fromUni[hi], t.fromUni[lo])
			continue
		}
		out = append(out, t.fromUni[r])
	}
	return out, nil
}

func (t *singleByte) Space() []byte { return []byte{t.fromUni[' ']} }

// doubleByte is a pure DBCS table.
type doubleByte struct {
	ccsid     int
	toUnicode []uint16 // 65536 entries
	fromUni   []uint16 // 65536 entries
	toMulti   map[uint16][]uint16
	fromMulti map[string]uint16 // key: UTF-16 code units as string(runes)
	maxMulti  int
}

func parseDouble(ccsid int, raw []byte) (*doubleByte, error) {
	const tableBytes = 65536 * 2
	if len(raw) < 2*tableBytes+4 {
		return nil, fmt.Errorf("ccsid %d: corrupt double-byte table", ccsid)
	}
	t := &doubleByte{
		ccsid:     ccsid,
		toUnicode: make([]uint16, 65536),
		fromUni:   make([]uint16, 65536),
		toMulti:   map[uint16][]uint16{},
		fromMulti: map[string]uint16{},
	}
	for i := 0; i < 65536; i++ {
		t.toUnicode[i] = binary.BigEndian.Uint16(raw[i*2:])
		t.fromUni[i] = binary.BigEndian.Uint16(raw[tableBytes+i*2:])
	}
	rest := raw[2*tableBytes:]
	n := int(binary.BigEndian.Uint32(rest))
	rest = rest[4:]
	for i := 0; i < n; i++ {
		if len(rest) < 3 {
			return nil, fmt.Errorf("ccsid %d: corrupt double-byte table", ccsid)
		}
		code := binary.BigEndian.Uint16(rest)
		l := int(rest[2])
		rest = rest[3:]
		if len(rest) < l*2 {
			return nil, fmt.Errorf("ccsid %d: corrupt double-byte table", ccsid)
		}
		seq := make([]uint16, l)
		for k := range seq {
			seq[k] = binary.BigEndian.Uint16(rest[k*2:])
		}
		rest = rest[l*2:]
		t.toMulti[code] = seq
		t.fromMulti[unitsKey(seq)] = code
		if l > t.maxMulti {
			t.maxMulti = l
		}
	}
	return t, nil
}

func unitsKey(units []uint16) string {
	b := make([]byte, len(units)*2)
	for i, u := range units {
		binary.BigEndian.PutUint16(b[i*2:], u)
	}
	return string(b)
}

func (t *doubleByte) CCSID() int { return t.ccsid }

func (t *doubleByte) decodeCode(dst []uint16, code uint16) []uint16 {
	if seq, ok := t.toMulti[code]; ok {
		return append(dst, seq...)
	}
	return append(dst, t.toUnicode[code])
}

func (t *doubleByte) Decode(data []byte) (string, error) {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = t.decodeCode(units, uint16(data[i])<<8|uint16(data[i+1]))
	}
	return string(utf16.Decode(units)), nil
}

// encodeAt converts the code units starting at src[i] and returns the DBCS
// code and the number of code units consumed.
func (t *doubleByte) encodeAt(src []uint16, i int) (uint16, int) {
	for l := min(t.maxMulti, len(src)-i); l >= 2; l-- {
		if code, ok := t.fromMulti[unitsKey(src[i:i+l])]; ok {
			return code, l
		}
	}
	u := src[i]
	if utf16.IsSurrogate(rune(u)) {
		if u < 0xDC00 && i+1 < len(src) {
			return dbSubChar, 2
		}
		return dbSubChar, 1
	}
	return t.fromUni[u], 1
}

func (t *doubleByte) Encode(text string) ([]byte, error) {
	src := utf16.Encode([]rune(text))
	out := make([]byte, 0, len(src)*2)
	for i := 0; i < len(src); {
		code, n := t.encodeAt(src, i)
		out = append(out, byte(code>>8), byte(code))
		i += n
	}
	return out, nil
}

func (t *doubleByte) Space() []byte {
	code := t.fromUni[0x3000]
	return []byte{byte(code >> 8), byte(code)}
}

// mixedByte is an EBCDIC mixed SBCS/DBCS table using shift-out/shift-in.
type mixedByte struct {
	ccsid int
	sb    *singleByte
	db    *doubleByte
}

func (t *mixedByte) CCSID() int { return t.ccsid }

func (t *mixedByte) Decode(data []byte) (string, error) {
	units := make([]uint16, 0, len(data))
	single := true
	for i := 0; i < len(data); i++ {
		b := data[i]
		if single {
			if b == shiftOut {
				single = false
				continue
			}
			units = append(units, t.sb.toUnicode[b])
			continue
		}
		if b == shiftIn {
			single = true
			continue
		}
		if i+1 >= len(data) {
			units = append(units, dbSubUnicode)
			break
		}
		units = t.db.decodeCode(units, uint16(b)<<8|uint16(data[i+1]))
		i++
	}
	return string(utf16.Decode(units)), nil
}

func (t *mixedByte) Encode(text string) ([]byte, error) {
	src := utf16.Encode([]rune(text))
	out := make([]byte, 0, len(src)*2)
	single := true
	putSingle := func(b byte) {
		if !single {
			single = true
			out = append(out, shiftIn)
		}
		out = append(out, b)
	}
	putDouble := func(code uint16) {
		if single {
			single = false
			out = append(out, shiftOut)
		}
		out = append(out, byte(code>>8), byte(code))
	}
	sbSub := t.sb.sub
	for i := 0; i < len(src); {
		cur := src[i]
		if cur&0xFF00 == 0 || cur == euro || (cur == 0x203E && (t.ccsid == 935 || t.ccsid == 1388)) {
			if b := t.sb.fromUni[cur]; b != sbSub || cur == sbSubUnicode {
				putSingle(b)
				i++
				continue
			}
			code, n := t.db.encodeAt(src, i)
			i += n
			if code == dbSubChar {
				putSingle(sbSub)
			} else {
				putDouble(code)
			}
			continue
		}
		code, n := t.db.encodeAt(src, i)
		i += n
		if code != dbSubChar || cur == dbSubUnicode {
			putDouble(code)
			continue
		}
		b := t.sb.fromUni[cur]
		switch {
		case b != sbSub:
			putSingle(b)
		case single:
			out = append(out, sbSub)
		default:
			out = append(out, byte(dbSubChar>>8), byte(dbSubChar&0xFF))
		}
	}
	if !single {
		out = append(out, shiftIn)
	}
	return out, nil
}

func (t *mixedByte) Space() []byte { return t.sb.Space() }

// unicodeCodec handles the algorithmic Unicode CCSIDs.
type unicodeImpl struct {
	ccsid int
	kind  int // 0 = UTF-16BE, 1 = UTF-16LE, 2 = UTF-8
}

func unicodeCodec(ccsid int) (Codec, bool) {
	switch ccsid {
	case 1200, 1201, 13488, 17584, 21680:
		return &unicodeImpl{ccsid: ccsid, kind: 0}, true
	case 1202:
		return &unicodeImpl{ccsid: ccsid, kind: 1}, true
	case 1208:
		return &unicodeImpl{ccsid: ccsid, kind: 2}, true
	}
	return nil, false
}

func (u *unicodeImpl) CCSID() int { return u.ccsid }

func (u *unicodeImpl) order() binary.ByteOrder {
	if u.kind == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func (u *unicodeImpl) Decode(data []byte) (string, error) {
	if u.kind == 2 {
		if utf8.Valid(data) {
			return string(data), nil
		}
		return string(bytes.ToValidUTF8(data, []byte("�"))), nil
	}
	if len(data)%2 != 0 {
		return "", fmt.Errorf("ccsid %d: data length must be even: %d", u.ccsid, len(data))
	}
	order := u.order()
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2:])
	}
	return string(utf16.Decode(units)), nil
}

func (u *unicodeImpl) Encode(text string) ([]byte, error) {
	if u.kind == 2 {
		return []byte(text), nil
	}
	order := u.order()
	units := utf16.Encode([]rune(text))
	out := make([]byte, len(units)*2)
	for i, c := range units {
		order.PutUint16(out[i*2:], c)
	}
	return out, nil
}

func (u *unicodeImpl) Space() []byte {
	if u.kind == 2 {
		return []byte{' '}
	}
	out := make([]byte, 2)
	u.order().PutUint16(out, ' ')
	return out
}

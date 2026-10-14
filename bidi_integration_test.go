package i400

// Golden end-to-end tests of the bidi layer against JTOpen's JDBC behaviour.
//
// The vectors come from internal/bidi/gen/BidiDriverGolden.java, which runs the real JTOpen
// ConvTable classes (ConvTableBidiMap) and AS400BidiTransform string functions with the
// properties SQLChar builds. To regenerate (JTOpen sources at ../JTOpen):
//
//	javac -d classes -sourcepath <JTOpen>/src/main/java internal/bidi/gen/BuildInfo.java internal/bidi/gen/BidiDriverGolden.java
//	java -cp classes com.ibm.as400.access.BidiDriverGolden conv    testdata/bidi_driver_conv.txt.gz
//	java -cp classes com.ibm.as400.access.BidiDriverGolden strings testdata/bidi_driver_strings.txt.gz
//
// The conversion vectors go through the driver: the DSN is parsed with ParseDSN, the column is
// decoded with decodeColumnValueWithBidi (CHAR) and parameters are encoded with
// encodeVarcharParameter (VARCHAR, whose length prefix is removed). The vectors avoid the
// deviation 4b (stale tail), which JDBC does not produce with its default properties, so the
// padded results equal JTOpen's.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bashdi/i400/internal/bidi"
	ccsidtab "github.com/bashdi/i400/internal/ccsid"
)

func readGoldenLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gunzip %s: %v", path, err)
	}
	defer zr.Close()
	var lines []string
	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return lines
}

// goldenBytes decodes a hex field of bytes ("-" is empty).
func goldenBytes(t *testing.T, field string) []byte {
	t.Helper()
	if field == "-" {
		return nil
	}
	b, err := hex.DecodeString(field)
	if err != nil {
		t.Fatalf("bad byte hex %q: %v", field, err)
	}
	return b
}

// goldenUnits decodes a hex field of UTF-16 code units ("-" is empty).
func goldenUnits(t *testing.T, field string) []uint16 {
	t.Helper()
	if field == "-" {
		return nil
	}
	if len(field)%4 != 0 {
		t.Fatalf("bad unit hex %q", field)
	}
	units := make([]uint16, len(field)/4)
	for i := range units {
		v, err := strconv.ParseUint(field[i*4:i*4+4], 16, 16)
		if err != nil {
			t.Fatalf("bad unit hex %q: %v", field, err)
		}
		units[i] = uint16(v)
	}
	return units
}

func goldenUnitsString(units []uint16) string {
	if len(units) == 0 {
		return "-"
	}
	var sb strings.Builder
	for _, u := range units {
		fmt.Fprintf(&sb, "%04X", u)
	}
	return sb.String()
}

func goldenBytesString(b []byte) string {
	if len(b) == 0 {
		return "-"
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

// configForVector parses the DSN that the vector's settings correspond to.
func configForVector(t *testing.T, cache map[string]Config, dsnType int, implicit, numeric bool) Config {
	t.Helper()
	key := fmt.Sprintf("%d/%t/%t", dsnType, implicit, numeric)
	if cfg, ok := cache[key]; ok {
		return cfg
	}
	dsn := fmt.Sprintf("as400://user:pass@host/?bidiStringType=%d&bidiImplicitReordering=%t&bidiNumericOrdering=%t",
		dsnType, implicit, numeric)
	cfg, err := ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN(%q): %v", dsn, err)
	}
	cache[key] = *cfg
	return *cfg
}

func TestBidiDriverConversionGolden(t *testing.T) {
	lines := readGoldenLines(t, "testdata/bidi_driver_conv.txt.gz")
	if len(lines) == 0 {
		t.Fatal("no conversion vectors")
	}
	cache := map[string]Config{}
	counts := map[string]int{}
	// changed counts the vectors whose layout differs from the plain table mapping, so the
	// test cannot pass vacuously.
	changed := 0
	for n, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) != 8 || f[0] != "C" {
			t.Fatalf("vector %d: malformed line %q", n, line)
		}
		ccsid, err := strconv.Atoi(f[1])
		if err != nil {
			t.Fatalf("vector %d: ccsid: %v", n, err)
		}
		dsnType, _ := strconv.Atoi(f[2])
		implicit := f[3] == "1"
		numeric := f[4] == "1"
		cfg := configForVector(t, cache, dsnType, implicit, numeric)
		bs := bidiSettingsFromConfig(cfg)
		counts[f[5]]++
		switch f[5] {
		case "D":
			raw := goldenBytes(t, f[6])
			want := f[7]
			col := columnMeta{Name: "C", Type: db2TypeChar, Length: len(raw), CCSID: ccsid}
			value, err := decodeColumnValueWithBidi(col, raw, defaultDateTimeSettings(), bs)
			if err != nil {
				t.Fatalf("vector %d (ccsid %d, dsn %d, %s) decode %s: %v", n, ccsid, dsnType, f[6], f[1], err)
			}
			got := value.(string)
			gotUnits := utf16.Encode([]rune(got))
			if goldenUnitsString(gotUnits) != want {
				t.Fatalf("vector %d (ccsid %d, dsn %d, implicit %t, numeric %t) decode %s:\n got  %s\n want %s",
					n, ccsid, dsnType, implicit, numeric, f[6], goldenUnitsString(gotUnits), want)
			}
			if plain, err := ccsidtab.Decode(raw, ccsid); err == nil && plain != got {
				changed++
			}
		case "E":
			units := goldenUnits(t, f[6])
			text := string(utf16.Decode(units))
			want := f[7]
			field := parameterMarkerField{SQLType: 448, Length: 0, CCSID: ccsid, bidi: &bs}
			encoded, err := encodeVarcharParameter(text, field)
			if err != nil {
				t.Fatalf("vector %d (ccsid %d, dsn %d) encode %s: %v", n, ccsid, dsnType, f[6], err)
			}
			raw := encoded[2:] // strip the VARCHAR length prefix
			if plain, err := ccsidtab.Encode(text, ccsid); err == nil && goldenBytesString(plain) != goldenBytesString(raw) {
				changed++
			}
			if goldenBytesString(raw) != want {
				t.Fatalf("vector %d (ccsid %d, dsn %d, implicit %t, numeric %t) encode %s:\n got  %s\n want %s",
					n, ccsid, dsnType, implicit, numeric, f[6], goldenBytesString(raw), want)
			}
		default:
			t.Fatalf("vector %d: unknown op %q", n, f[5])
		}
	}
	if changed == 0 {
		t.Fatal("no vector changed the layout; the golden test would pass vacuously")
	}
	t.Logf("checked %d decode and %d encode vectors over %d configurations (%d with a layout change)",
		counts["D"], counts["E"], len(cache), changed)
}

func TestBidiDriverStringFunctionsGolden(t *testing.T) {
	lines := readGoldenLines(t, "testdata/bidi_driver_strings.txt.gz")
	if len(lines) == 0 {
		t.Fatal("no string vectors")
	}
	counts := map[string]int{}
	for n, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			t.Fatalf("vector %d: malformed line %q", n, line)
		}
		in, _ := strconv.Atoi(f[1])
		out, _ := strconv.Atoi(f[2])
		text := goldenUnits(t, f[3])
		want := f[4]
		var got []uint16
		switch f[0] {
		case "T":
			got = bidi.TransformString(text, in, out)
		case "S":
			got = bidi.SQLStatementReordering(text, in, out)
		case "M":
			got = bidi.MetaDataReordering(text, in, out)
		default:
			t.Fatalf("vector %d: unknown function %q", n, f[0])
		}
		counts[f[0]]++
		if goldenUnitsString(got) != want {
			t.Fatalf("vector %d %s(in %d, out %d) %s:\n got  %s\n want %s",
				n, f[0], in, out, f[3], goldenUnitsString(got), want)
		}
	}
	t.Logf("checked T=%d S=%d M=%d string vectors", counts["T"], counts["S"], counts["M"])
}

// TestBidiDriverCCSIDListMatchesJTOpen checks that the CCSIDs decoded with a bidi layout are the
// ones whose JTOpen conversion class is ConvTableBidiMap (the list is generated from them).
func TestBidiDriverCCSIDListMatchesJTOpen(t *testing.T) {
	want := []int{420, 424, 425, 862, 864, 916, 1046, 1089, 1255, 1256, 5351, 8612, 12708,
		62211, 62224, 62235, 62245, 62251}
	for _, id := range want {
		if !bidi.IsConversionBidiCCSID(id) {
			t.Errorf("IsConversionBidiCCSID(%d) = false, want true", id)
		}
	}
	if bidi.IsConversionBidiCCSID(37) || bidi.IsConversionBidiCCSID(1200) || bidi.IsConversionBidiCCSID(5352) {
		t.Error("IsConversionBidiCCSID is true for a CCSID without a bidi conversion class")
	}
}

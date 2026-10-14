package i400

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// udZonedCol builds a column whose value sits at offset 0 of the row.
func udZonedCol(sqlType, length, precision, scale, ccsidValue int) columnMeta {
	return columnMeta{Type: sqlType, Length: length, Precision: precision, Scale: scale, CCSID: ccsidValue}
}

// TestUDDecodeZonedDecimalSigns follows JTOpen AS400ZonedDecimal.toObject: the
// sign is the high nibble of the last byte. B and D are negative; A, C, E and F
// are positive. Only the low nibble of each byte is a digit.
func TestUDDecodeZonedDecimalSigns(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		precision int
		scale     int
		want      string
	}{
		{name: "positive F sign", data: []byte{0xF1, 0xF2, 0xF3, 0xF4}, precision: 4, want: "1234"},
		{name: "positive C sign", data: []byte{0xF1, 0xF2, 0xF3, 0xC4}, precision: 4, want: "1234"},
		{name: "positive A sign", data: []byte{0xF1, 0xF2, 0xF3, 0xA4}, precision: 4, want: "1234"},
		{name: "positive E sign", data: []byte{0xF1, 0xF2, 0xF3, 0xE4}, precision: 4, want: "1234"},
		{name: "negative D sign", data: []byte{0xF1, 0xF2, 0xF3, 0xD4}, precision: 4, want: "-1234"},
		{name: "negative B sign", data: []byte{0xF1, 0xF2, 0xF3, 0xB4}, precision: 4, want: "-1234"},
		{name: "negative with scale", data: []byte{0xF1, 0xF2, 0xF3, 0xD4}, precision: 4, scale: 2, want: "-12.34"},
		{name: "scale pads leading zero", data: []byte{0xF0, 0xF5, 0xF0}, precision: 3, scale: 2, want: "0.50"},
		{name: "scale below one digit", data: []byte{0xF0, 0xF5}, precision: 2, scale: 2, want: "0.05"},
		// JTOpen reads only the low nibble of non-sign bytes, so the zone is ignored.
		{name: "zone of leading bytes ignored", data: []byte{0x31, 0x32, 0x33, 0xC4}, precision: 4, want: "1234"},
		{name: "pads to precision", data: []byte{0xF7, 0xC8}, precision: 5, want: "00078"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeZonedDecimalString(tt.data, tt.precision, tt.scale)
			if err != nil {
				t.Fatalf("decodeZonedDecimalString() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeZonedDecimalString(% X) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

// TestUDDecodeZonedDecimalRejectsInvalidData matches JTOpen, which throws a
// NumberFormatException for a digit nibble above 9 or a sign nibble that is
// not in A-F.
func TestUDDecodeZonedDecimalRejectsInvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "digit nibble A in middle byte", data: []byte{0xF1, 0xFA, 0xF3, 0xC4}},
		{name: "digit nibble in last byte", data: []byte{0xF1, 0xF2, 0xF3, 0xFE}},
		{name: "sign nibble 0 in last byte", data: []byte{0xF1, 0xF2, 0xF3, 0x04}},
		{name: "sign nibble 9 in last byte", data: []byte{0xF1, 0xF2, 0xF3, 0x94}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := decodeZonedDecimalString(tt.data, 4, 0); err == nil {
				t.Fatalf("decodeZonedDecimalString(% X) = %q, nil error; want error", tt.data, got)
			}
		})
	}
}

func TestUDDecodeZonedDecimalEmptyIsZero(t *testing.T) {
	got, err := decodeZonedDecimalString(nil, 5, 2)
	if err != nil || got != "0" {
		t.Fatalf("decodeZonedDecimalString(nil) = %q, %v; want 0", got, err)
	}
}

func TestUDDecodeColumnValueNumericZoned(t *testing.T) {
	col := udZonedCol(db2TypeNumeric, 4, 4, 2, 37)
	got, err := decodeColumnValue(col, []byte{0xF1, 0xF2, 0xF3, 0xD4})
	if err != nil || got != "-12.34" {
		t.Fatalf("NUMERIC(4,2) negative = %v, %v; want -12.34", got, err)
	}
	got, err = decodeColumnValue(col, []byte{0xF1, 0xF2, 0xF3, 0xC4})
	if err != nil || got != "12.34" {
		t.Fatalf("NUMERIC(4,2) positive = %v, %v; want 12.34", got, err)
	}
}

func TestUDDecodeColumnValueFloatWidths(t *testing.T) {
	// REAL (FLOAT(4)) is 4-byte IEEE 754 big-endian, returned as float64.
	realRaw := make([]byte, 4)
	binary.BigEndian.PutUint32(realRaw, math.Float32bits(1.5))
	got, err := decodeColumnValue(columnMeta{Type: db2TypeFloatingPoint, Length: 4}, realRaw)
	if err != nil || got != 1.5 {
		t.Fatalf("FLOAT(4) = %v, %v; want 1.5", got, err)
	}
	if name := (columnMeta{Type: db2TypeFloatingPoint, Length: 4}).databaseTypeName(); name != "REAL" {
		t.Fatalf("FLOAT(4) type name = %q, want REAL", name)
	}

	doubleRaw := make([]byte, 8)
	binary.BigEndian.PutUint64(doubleRaw, math.Float64bits(-2.25))
	got, err = decodeColumnValue(columnMeta{Type: db2TypeFloatingPoint + 1, Length: 8}, doubleRaw)
	if err != nil || got != -2.25 {
		t.Fatalf("FLOAT(8) = %v, %v; want -2.25", got, err)
	}

	if _, err := decodeColumnValue(columnMeta{Type: db2TypeFloatingPoint, Length: 4}, []byte{1, 2}); err == nil {
		t.Fatalf("short FLOAT error = nil, want error")
	}
}

func TestUDDecodeColumnValueDatalinkIsVarcharShaped(t *testing.T) {
	// JTOpen SQLDatalink carries a 2-byte length then the URL text in the column CCSID.
	col := columnMeta{Type: db2TypeDatalink, Length: 5, CCSID: 37}
	got, err := decodeColumnValue(col, []byte{0x00, 0x03, 0xC1, 0xC2, 0xC3})
	if err != nil || got != "ABC" {
		t.Fatalf("DATALINK = %v, %v; want ABC", got, err)
	}
	if name := col.databaseTypeName(); name != "DATALINK" {
		t.Fatalf("DATALINK type name = %q", name)
	}
	// An empty value (fewer than 2 bytes) decodes to an empty string.
	got, err = decodeColumnValue(col, []byte{0x00})
	if err != nil || got != "" {
		t.Fatalf("short DATALINK = %v, %v; want empty string", got, err)
	}
}

func TestUDDecodeColumnValueRowIDBytes(t *testing.T) {
	// ROWID is a 2-byte length then bytes, returned as a copy of the bytes.
	col := columnMeta{Type: db2TypeRowID, Length: 6}
	raw := []byte{0x00, 0x02, 0x01, 0x02, 0xEE, 0xEE}
	got, err := decodeColumnValue(col, raw)
	if err != nil || !bytes.Equal(got.([]byte), []byte{0x01, 0x02}) {
		t.Fatalf("ROWID = %v, %v; want 01 02", got, err)
	}
	// The returned slice must be a copy, not an alias of the row buffer.
	got.([]byte)[0] = 0xFF
	if raw[2] != 0x01 {
		t.Fatalf("ROWID value aliases the row buffer")
	}
}

func TestUDDecodeColumnValueGraphicCCSID1200(t *testing.T) {
	got, err := decodeColumnValue(columnMeta{Type: db2TypeGraphic, Length: 4, CCSID: 1200}, udUTF16BE("AB"))
	if err != nil || got != "AB" {
		t.Fatalf("GRAPHIC 1200 = %v, %v; want AB", got, err)
	}
	// VARGRAPHIC has a 2-byte count of double-byte characters.
	raw := append([]byte{0x00, 0x02}, udUTF16BE("AB")...)
	got, err = decodeColumnValue(columnMeta{Type: db2TypeVarGraphic, Length: 6, CCSID: 1200}, raw)
	if err != nil || got != "AB" {
		t.Fatalf("VARGRAPHIC 1200 = %v, %v; want AB", got, err)
	}
}

func TestUDDecodeColumnValueBlankTimeIsReturnedAsString(t *testing.T) {
	// Blank TIME text is returned unchanged (datetime_format.go decodeTimeText).
	// JTOpen would instead decode 0x40 digits as 00.00.00.
	blank := udEBCDIC(t, "        ")
	got, err := decodeColumnValue(columnMeta{Type: db2TypeTime, Length: 8, CCSID: 37}, blank)
	if err != nil {
		t.Fatalf("blank TIME error = %v", err)
	}
	if s, ok := got.(string); !ok || s != "" {
		t.Fatalf("blank TIME = %#v, want empty string", got)
	}
}

func TestUDDecodeColumnValueCCSID65535IsBinary(t *testing.T) {
	// CCSID 65535 means no conversion: CHAR becomes BINARY and returns raw bytes.
	got, err := decodeColumnValue(columnMeta{Type: db2TypeChar, Length: 3, CCSID: 65535}, []byte{1, 2, 3})
	if err != nil || !bytes.Equal(got.([]byte), []byte{1, 2, 3}) {
		t.Fatalf("CHAR 65535 = %#v, %v; want []byte{1,2,3}", got, err)
	}
	// VARCHAR 65535 becomes VARBINARY: the 2-byte length is consumed.
	got, err = decodeColumnValue(columnMeta{Type: db2TypeVarchar, Length: 4, CCSID: 65535}, []byte{0x00, 0x02, 0xAB, 0xCD})
	if err != nil || !bytes.Equal(got.([]byte), []byte{0xAB, 0xCD}) {
		t.Fatalf("VARCHAR 65535 = %#v, %v; want AB CD", got, err)
	}
}

func TestUDDecodeColumnValueCharKeepsTrailingBlanks(t *testing.T) {
	// JTOpen SQLChar keeps the full CHAR value, including trailing blanks.
	raw := udEBCDIC(t, "AB  ")
	got, err := decodeColumnValue(columnMeta{Type: db2TypeChar, Length: 4, CCSID: 37}, raw)
	if err != nil || got != "AB  " {
		t.Fatalf("CHAR(4) = %#v, %v; want AB  ", got, err)
	}
}

func TestUDDecodeColumnValueInlineLOBs(t *testing.T) {
	// Inline BLOB: 4-byte length prefix then the data; padding is dropped.
	blob := columnMeta{Type: db2TypeBlob, Length: 10}
	got, err := decodeColumnValue(blob, []byte{0, 0, 0, 2, 0xAA, 0xBB, 0, 0, 0, 0})
	if err != nil || !bytes.Equal(got.([]byte), []byte{0xAA, 0xBB}) {
		t.Fatalf("inline BLOB = %#v, %v; want AA BB", got, err)
	}

	// Inline CLOB: the prefix counts bytes in the column CCSID.
	clob := columnMeta{Type: db2TypeClob, Length: 8, CCSID: 37}
	got, err = decodeColumnValue(clob, append([]byte{0, 0, 0, 2}, udEBCDIC(t, "HI")...))
	if err != nil || got != "HI" {
		t.Fatalf("inline CLOB = %#v, %v; want HI", got, err)
	}

	// Inline DBCLOB: the prefix counts characters, so the byte count is doubled.
	dbclob := columnMeta{Type: db2TypeDbclob, Length: 8, CCSID: 1200}
	got, err = decodeColumnValue(dbclob, append([]byte{0, 0, 0, 2}, udUTF16BE("AB")...))
	if err != nil || got != "AB" {
		t.Fatalf("inline DBCLOB = %#v, %v; want AB", got, err)
	}

	if _, err := decodeColumnValue(blob, []byte{0, 0}); err == nil {
		t.Fatalf("short inline BLOB error = nil, want error")
	}
	if _, err := decodeColumnValue(blob, []byte{0, 0, 0, 9, 0xAA}); err == nil {
		t.Fatalf("inline BLOB length past row error = nil, want error")
	}
}

func TestUDDecodeColumnValueOffsetErrors(t *testing.T) {
	col := columnMeta{Type: db2TypeInteger, Length: 4, Offset: 10}
	if _, err := decodeColumnValue(col, []byte{0, 0, 0, 1}); err == nil {
		t.Fatalf("offset past row error = nil, want error")
	}
	// A column that runs past the row end is truncated to the row, then fails
	// its own length check.
	col = columnMeta{Type: db2TypeInteger, Length: 4, Offset: 2}
	if _, err := decodeColumnValue(col, []byte{0, 0, 0, 1}); err == nil {
		t.Fatalf("truncated INTEGER error = nil, want error")
	}
}

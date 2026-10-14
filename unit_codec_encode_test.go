package i400

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// udEBCDIC returns the CCSID 37 bytes of s, using the independent x/text
// CP037 table so expectations do not depend on the driver's ccsid package.
func udEBCDIC(t *testing.T, s string) []byte {
	t.Helper()
	out, err := charmap.CodePage037.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("CP037 encode %q: %v", s, err)
	}
	return out
}

// udUTF16BE returns the big-endian UTF-16 bytes of s (CCSID 1200).
func udUTF16BE(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = binary.BigEndian.AppendUint16(out, uint16(r))
	}
	return out
}

func udField(sqlType, length, ccsid int) parameterMarkerField {
	return parameterMarkerField{SQLType: sqlType, Length: length, CCSID: ccsid, LOBLocator: -1, LOBMaxSize: -1}
}

func TestUDEncodeDateParameterISO(t *testing.T) {
	field := udField(db2TypeDate, 10, 37)
	// JTOpen SQLDate uses the ISO format yyyy-mm-dd for DATE columns.
	got, err := encodeDateParameter(time.Date(2024, 3, 5, 13, 4, 5, 0, time.UTC), field)
	if err != nil {
		t.Fatalf("encodeDateParameter(time.Time) error = %v", err)
	}
	if want := udEBCDIC(t, "2024-03-05"); !bytes.Equal(got, want) {
		t.Fatalf("time.Time date bytes = % X, want % X", got, want)
	}

	got, err = encodeDateParameter("2024-03-05", field)
	if err != nil {
		t.Fatalf("encodeDateParameter(string) error = %v", err)
	}
	if want := udEBCDIC(t, "2024-03-05"); !bytes.Equal(got, want) {
		t.Fatalf("string date bytes = % X, want % X", got, want)
	}

	// Shorter text is padded with the CCSID 37 blank (0x40).
	got, err = encodeDateParameter("2024-3-5", field)
	if err != nil {
		t.Fatalf("encodeDateParameter(short string) error = %v", err)
	}
	if want := append(udEBCDIC(t, "2024-3-5"), 0x40, 0x40); !bytes.Equal(got, want) {
		t.Fatalf("short date bytes = % X, want % X", got, want)
	}

	if _, err := encodeDateParameter("2024-03-051", field); err == nil {
		t.Fatalf("encodeDateParameter(too long) error = nil, want error")
	}
}

func TestUDEncodeTimeParameterISO(t *testing.T) {
	field := udField(db2TypeTime, 8, 37)
	// JTOpen SQLTime ISO format is hh.mm.ss; fractional seconds are not sent.
	got, err := encodeTimeParameter(time.Date(2000, 1, 1, 13, 4, 5, 987000000, time.UTC), field)
	if err != nil {
		t.Fatalf("encodeTimeParameter(time.Time) error = %v", err)
	}
	if want := udEBCDIC(t, "13.04.05"); !bytes.Equal(got, want) {
		t.Fatalf("time.Time time bytes = % X, want % X", got, want)
	}
	got, err = encodeTimeParameter("01.02.03", field)
	if err != nil {
		t.Fatalf("encodeTimeParameter(string) error = %v", err)
	}
	if want := udEBCDIC(t, "01.02.03"); !bytes.Equal(got, want) {
		t.Fatalf("string time bytes = % X, want % X", got, want)
	}
	if _, err := encodeTimeParameter("123.04.05", field); err == nil {
		t.Fatalf("encodeTimeParameter(too long) error = nil, want error")
	}
}

func TestUDParameterFieldDateAndTimeText(t *testing.T) {
	ts := time.Date(2024, 12, 31, 23, 59, 58, 500000000, time.UTC)
	if got, err := parameterFieldDateText(ts); err != nil || got != "2024-12-31" {
		t.Fatalf("parameterFieldDateText(time.Time) = %q, %v; want 2024-12-31", got, err)
	}
	if got, err := parameterFieldTimeText(ts); err != nil || got != "23.59.58" {
		t.Fatalf("parameterFieldTimeText(time.Time) = %q, %v; want 23.59.58", got, err)
	}
	if got, err := parameterFieldDateText("2024-02-29"); err != nil || got != "2024-02-29" {
		t.Fatalf("parameterFieldDateText(string) = %q, %v", got, err)
	}
	if got, err := parameterFieldTimeText([]byte("07.08.09")); err != nil || got != "07.08.09" {
		t.Fatalf("parameterFieldTimeText([]byte) = %q, %v", got, err)
	}
	if got, err := parameterFieldDateText(int64(5)); err != nil || got != "5" {
		t.Fatalf("parameterFieldDateText(int64) = %q, %v", got, err)
	}
	if got, err := parameterFieldTimestampText(ts); err != nil || got != "2024-12-31-23.59.58.5" {
		t.Fatalf("parameterFieldTimestampText(time.Time) = %q, %v", got, err)
	}
}

func TestUDEncodeFixedTextParameterPadsWithCCSIDBlank(t *testing.T) {
	// CHAR(5) CCSID 37: short text is padded with the EBCDIC blank 0x40.
	field := udField(db2TypeChar, 5, 37)
	got, err := encodeFixedTextParameter("AB", field)
	if err != nil {
		t.Fatalf("encodeFixedTextParameter() error = %v", err)
	}
	want := append(udEBCDIC(t, "AB"), 0x40, 0x40, 0x40)
	if !bytes.Equal(got, want) {
		t.Fatalf("CHAR(5) bytes = % X, want % X", got, want)
	}

	// Exact length needs no padding.
	got, err = encodeFixedTextParameter("ABCDE", field)
	if err != nil || !bytes.Equal(got, udEBCDIC(t, "ABCDE")) {
		t.Fatalf("exact CHAR(5) = % X, %v", got, err)
	}

	// Too long is an error, not a silent truncation.
	if _, err := encodeFixedTextParameter("ABCDEF", field); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("CHAR too long error = %v, want parameter value too long", err)
	}

	// Non-string scalars are rendered as text first (int64, float64, bool).
	got, err = encodeFixedTextParameter(int64(42), udField(db2TypeChar, 3, 37))
	if err != nil || !bytes.Equal(got, append(udEBCDIC(t, "42"), 0x40)) {
		t.Fatalf("CHAR(3) from int64 = % X, %v", got, err)
	}
	got, err = encodeFixedTextParameter(true, udField(db2TypeChar, 1, 37))
	if err != nil || !bytes.Equal(got, udEBCDIC(t, "1")) {
		t.Fatalf("CHAR(1) from bool = % X, %v", got, err)
	}

	// Length zero means no fixed length: the raw encoding is returned unpadded.
	got, err = encodeFixedTextParameter("AB", udField(db2TypeChar, 0, 37))
	if err != nil || !bytes.Equal(got, udEBCDIC(t, "AB")) {
		t.Fatalf("CHAR(no length) = % X, %v", got, err)
	}
}

func TestUDEncodeGraphicParameterUnicodeCCSIDs(t *testing.T) {
	for _, ccsidValue := range []int{1200, 13488} {
		// GRAPHIC(3) is 6 bytes of UTF-16BE; the pad is the UTF-16 space 0x0020.
		field := udField(db2TypeGraphic, 6, ccsidValue)
		got, err := encodeGraphicParameter("AB", field)
		if err != nil {
			t.Fatalf("CCSID %d encodeGraphicParameter() error = %v", ccsidValue, err)
		}
		want := append(udUTF16BE("AB"), 0x00, 0x20)
		if !bytes.Equal(got, want) {
			t.Fatalf("CCSID %d GRAPHIC bytes = % X, want % X", ccsidValue, got, want)
		}
		if _, err := encodeGraphicParameter("ABCD", field); err == nil {
			t.Fatalf("CCSID %d too long error = nil, want error", ccsidValue)
		}
	}
}

func TestUDEncodeGraphicParameterDBCS835(t *testing.T) {
	// CCSID 835 is an EBCDIC DBCS table: JTOpen re-pads graphic fields with the
	// ideographic space U+3000, which is X'4040' in DBCS.
	field := udField(db2TypeGraphic, 4, 835)
	got, err := encodeGraphicParameter("", field)
	if err != nil {
		t.Fatalf("encodeGraphicParameter(empty, 835) error = %v", err)
	}
	if want := []byte{0x40, 0x40, 0x40, 0x40}; !bytes.Equal(got, want) {
		t.Fatalf("DBCS 835 pad = % X, want % X", got, want)
	}

	// Two ideographic spaces fill exactly 4 bytes, and do not fit in 2.
	got, err = encodeGraphicParameter("　　", field)
	if err != nil || !bytes.Equal(got, []byte{0x40, 0x40, 0x40, 0x40}) {
		t.Fatalf("DBCS 835 two spaces = % X, %v", got, err)
	}
	if _, err := encodeGraphicParameter("　　", udField(db2TypeGraphic, 2, 835)); err == nil {
		t.Fatalf("DBCS 835 too long error = nil, want error")
	}
}

func TestUDEncodeVarbinaryParameterBranches(t *testing.T) {
	field := udField(db2TypeVarbinary, 0, 65535)

	// []byte is copied with a 2-byte big-endian length prefix (JTOpen SQLVarbinary).
	got, err := encodeVarbinaryParameter([]byte{1, 2, 3}, field)
	if err != nil || !bytes.Equal(got, []byte{0x00, 0x03, 0x01, 0x02, 0x03}) {
		t.Fatalf("[]byte varbinary = % X, %v", got, err)
	}

	tests := []struct {
		name  string
		value driver.Value
		want  []byte
	}{
		{name: "0x prefix", value: "0xABCD", want: []byte{0x00, 0x02, 0xAB, 0xCD}},
		{name: "0X prefix", value: "0XabCD", want: []byte{0x00, 0x02, 0xAB, 0xCD}},
		{name: "X quoted literal", value: "X'0a0b'", want: []byte{0x00, 0x02, 0x0A, 0x0B}},
		{name: "lowercase x quoted", value: "x'FF'", want: []byte{0x00, 0x01, 0xFF}},
		{name: "embedded spaces", value: "AB CD", want: []byte{0x00, 0x02, 0xAB, 0xCD}},
		{name: "odd length left padded", value: "ABC", want: []byte{0x00, 0x02, 0x0A, 0xBC}},
		{name: "empty string", value: "", want: []byte{0x00, 0x00}},
		{name: "byte slice value", value: []byte("hi"), want: []byte{0x00, 0x02, 'h', 'i'}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeVarbinaryParameter(tt.value, field)
			if err != nil {
				t.Fatalf("encodeVarbinaryParameter(%v) error = %v", tt.value, err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("encodeVarbinaryParameter(%v) = % X, want % X", tt.value, got, tt.want)
			}
		})
	}

	if _, err := encodeVarbinaryParameter("0xZZ", field); err == nil {
		t.Fatalf("invalid hex string error = nil, want error")
	}
	if _, err := encodeVarbinaryParameter("X'0A", field); err == nil {
		t.Fatalf("unterminated X' literal error = nil, want error")
	}
	if _, err := encodeVarbinaryParameter(int64(5), field); !errors.Is(err, errPreparedStatementBindingUnsupported) {
		t.Fatalf("int64 into VARBINARY error = %v, want unsupported binding", err)
	}

	// Declared length includes the 2-byte prefix: 3 data bytes need 5.
	if _, err := encodeVarbinaryParameter([]byte{1, 2, 3}, udField(db2TypeVarbinary, 4, 65535)); err == nil {
		t.Fatalf("VARBINARY(4) with 3 bytes error = nil, want too long")
	}
	got, err = encodeVarbinaryParameter([]byte{1, 2, 3}, udField(db2TypeVarbinary, 5, 65535))
	if err != nil || len(got) != 5 {
		t.Fatalf("VARBINARY(5) with 3 bytes = % X, %v", got, err)
	}

	if _, err := encodeVarbinaryParameter(make([]byte, 65536), udField(db2TypeVarbinary, 0, 65535)); err == nil {
		t.Fatalf("65536 bytes error = nil, want 2-byte length prefix error")
	}
}

func TestUDParameterFieldValueStringAcceptedTypes(t *testing.T) {
	tests := []struct {
		name  string
		value driver.Value
		want  string
	}{
		{name: "string", value: "abc", want: "abc"},
		{name: "bytes", value: []byte("xyz"), want: "xyz"},
		{name: "int64", value: int64(-42), want: "-42"},
		{name: "float64", value: 1.5, want: "1.5"},
		{name: "float64 integral", value: float64(3), want: "3"},
		{name: "bool true", value: true, want: "1"},
		{name: "bool false", value: false, want: "0"},
		// JTOpen binds a Timestamp to character columns in the DB2 form
		// yyyy-mm-dd-hh.mm.ss.ffffff (SQLVarcharBase.set), not RFC 3339.
		{name: "time with fraction", value: time.Date(2024, 3, 5, 13, 4, 5, 123456000, time.UTC), want: "2024-03-05-13.04.05.123456"},
		{name: "time whole second", value: time.Date(2024, 3, 5, 13, 4, 5, 0, time.UTC), want: "2024-03-05-13.04.05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parameterFieldValueString(tt.value)
			if err != nil {
				t.Fatalf("parameterFieldValueString(%v) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("parameterFieldValueString(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestUDParameterFieldFloat64AcceptedTypes(t *testing.T) {
	cases := []struct {
		value driver.Value
		want  float64
	}{
		{value: 1.25, want: 1.25},
		{value: int64(-3), want: -3},
		{value: true, want: 1},
		{value: false, want: 0},
		{value: " 2.5 ", want: 2.5},
		{value: []byte("1e3"), want: 1000},
	}
	for _, c := range cases {
		got, err := parameterFieldFloat64(c.value)
		if err != nil || got != c.want {
			t.Fatalf("parameterFieldFloat64(%#v) = %v, %v; want %v", c.value, got, err, c.want)
		}
	}
	if _, err := parameterFieldFloat64("not a number"); err == nil {
		t.Fatalf("parameterFieldFloat64(invalid text) error = nil, want error")
	}
	if _, err := parameterFieldFloat64(nil); !errors.Is(err, errPreparedStatementBindingUnsupported) {
		t.Fatalf("parameterFieldFloat64(nil) error = %v, want unsupported binding", err)
	}
}

func TestUDParameterFieldNullLengthPerType(t *testing.T) {
	tests := []struct {
		name  string
		field parameterMarkerField
		want  int
	}{
		{name: "bigint", field: udField(db2TypeBigint, 0, 0), want: 8},
		{name: "integer", field: udField(db2TypeInteger, 0, 0), want: 4},
		{name: "smallint", field: udField(db2TypeSmallint, 0, 0), want: 2},
		{name: "boolean", field: udField(db2TypeBoolean, 0, 0), want: 1},
		{name: "double", field: udField(db2TypeFloatingPoint, 0, 0), want: 8},
		{name: "decimal 5 digits", field: parameterMarkerField{SQLType: db2TypeDecimal, Precision: 5}, want: 3},
		{name: "decimal 6 digits", field: parameterMarkerField{SQLType: db2TypeDecimal, Precision: 6}, want: 4},
		{name: "decimal unknown precision", field: parameterMarkerField{SQLType: db2TypeDecimal}, want: 0},
		{name: "numeric zoned", field: parameterMarkerField{SQLType: db2TypeNumeric, Precision: 7}, want: 7},
		{name: "decfloat 16", field: parameterMarkerField{SQLType: db2TypeDecfloat, Precision: 16}, want: 8},
		{name: "decfloat 34", field: parameterMarkerField{SQLType: db2TypeDecfloat, Precision: 34}, want: 16},
		{name: "varbinary", field: udField(db2TypeVarbinary, 0, 0), want: 0},
		{name: "varchar", field: udField(db2TypeVarchar, 0, 37), want: 0},
		{name: "date without length", field: udField(db2TypeDate, 0, 37), want: 0},
		{name: "explicit length wins", field: udField(db2TypeInteger, 12, 0), want: 12},
		{name: "explicit length on decimal", field: parameterMarkerField{SQLType: db2TypeDecimal, Length: 9, Precision: 5}, want: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parameterFieldNullLength(tt.field); got != tt.want {
				t.Fatalf("parameterFieldNullLength() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUDEncodeParameterValueNullIsZeroFilled(t *testing.T) {
	got, err := encodeParameterValue(udField(db2TypeInteger, 0, 0), nil, true)
	if err != nil || !bytes.Equal(got, make([]byte, 4)) {
		t.Fatalf("NULL INTEGER = % X, %v; want 4 zero bytes", got, err)
	}
	got, err = encodeParameterValue(udField(db2TypeVarchar, 0, 37), nil, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("NULL VARCHAR = % X, %v; want empty", got, err)
	}
}

func TestUDIsInlineLOBParameterField(t *testing.T) {
	tests := []struct {
		name  string
		field parameterMarkerField
		want  bool
	}{
		{name: "blob inline", field: parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 0, LOBMaxSize: 100}, want: true},
		{name: "clob inline", field: parameterMarkerField{SQLType: db2TypeClob, LOBLocator: -1, LOBMaxSize: 10}, want: true},
		{name: "dbclob inline", field: parameterMarkerField{SQLType: db2TypeDbclob, LOBLocator: 0, LOBMaxSize: 10}, want: true},
		{name: "blob locator handle", field: parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 7, LOBMaxSize: 100}, want: false},
		{name: "no max size", field: parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 0, LOBMaxSize: 0}, want: false},
		{name: "xml not inline", field: parameterMarkerField{SQLType: db2TypeXML, LOBLocator: 0, LOBMaxSize: 100}, want: false},
		{name: "integer", field: parameterMarkerField{SQLType: db2TypeInteger, LOBLocator: 0, LOBMaxSize: 100}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isInlineLOBParameterField(tt.field); got != tt.want {
				t.Fatalf("isInlineLOBParameterField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUDEncodeInlineLOBParameterValue(t *testing.T) {
	// BLOB: 4-byte big-endian byte count, then data, padded to the field size.
	blob := parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 0, LOBMaxSize: 10}
	got, err := encodeInlineLOBParameterValue(blob, []byte{1, 2, 3}, true, false)
	if err != nil {
		t.Fatalf("BLOB encode error = %v", err)
	}
	want := []byte{0, 0, 0, 3, 1, 2, 3, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("BLOB inline = % X, want % X", got, want)
	}

	// An explicit field length is used as the total size.
	blob.Length = 20
	got, err = encodeInlineLOBParameterValue(blob, []byte{9}, true, false)
	if err != nil || len(got) != 20 || got[3] != 1 || got[4] != 9 {
		t.Fatalf("BLOB with Length 20 = % X, %v", got, err)
	}

	// Too long (3 bytes against a 2-byte maximum) is an error.
	small := parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 0, LOBMaxSize: 2}
	if _, err := encodeInlineLOBParameterValue(small, []byte{1, 2, 3}, true, false); err == nil {
		t.Fatalf("BLOB too long error = nil, want error")
	}

	// CLOB: the prefix is the byte count of the CCSID 37 text.
	clob := parameterMarkerField{SQLType: db2TypeClob, LOBLocator: 0, LOBMaxSize: 8, CCSID: 37}
	got, err = encodeInlineLOBParameterValue(clob, "HI", true, false)
	if err != nil {
		t.Fatalf("CLOB encode error = %v", err)
	}
	wantClob := append([]byte{0, 0, 0, 2}, udEBCDIC(t, "HI")...)
	wantClob = append(wantClob, make([]byte, 12-len(wantClob))...)
	if !bytes.Equal(got, wantClob) {
		t.Fatalf("CLOB inline = % X, want % X", got, wantClob)
	}

	// DBCLOB: the prefix counts characters (UTF-16 code units), not bytes.
	dbclob := parameterMarkerField{SQLType: db2TypeDbclob, LOBLocator: 0, LOBMaxSize: 8, CCSID: 1200}
	got, err = encodeInlineLOBParameterValue(dbclob, "AB", true, false)
	if err != nil {
		t.Fatalf("DBCLOB encode error = %v", err)
	}
	wantDB := append([]byte{0, 0, 0, 2}, udUTF16BE("AB")...)
	wantDB = append(wantDB, make([]byte, 12-len(wantDB))...)
	if !bytes.Equal(got, wantDB) {
		t.Fatalf("DBCLOB inline = % X, want % X", got, wantDB)
	}

	// NULL or no input gives a zero-filled field of the full size.
	fresh := parameterMarkerField{SQLType: db2TypeBlob, LOBLocator: 0, LOBMaxSize: 10}
	got, err = encodeInlineLOBParameterValue(fresh, nil, true, true)
	if err != nil || !bytes.Equal(got, make([]byte, 14)) {
		t.Fatalf("NULL inline BLOB = % X, %v", got, err)
	}
	got, err = encodeInlineLOBParameterValue(fresh, []byte{1}, false, false)
	if err != nil || !bytes.Equal(got, make([]byte, 14)) {
		t.Fatalf("no-input inline BLOB = % X, %v", got, err)
	}

	// Non-LOB type is not supported by this encoder.
	if _, err := encodeInlineLOBParameterValue(udField(db2TypeInteger, 4, 0), []byte{1}, true, false); !errors.Is(err, errPreparedStatementBindingUnsupported) {
		t.Fatalf("INTEGER through inline LOB error = %v, want unsupported", err)
	}
}

func TestUDEncodeInlineLOBParameterValueRejectsOversizedText(t *testing.T) {
	clob := parameterMarkerField{SQLType: db2TypeClob, LOBLocator: 0, LOBMaxSize: 2, CCSID: 37}
	if _, err := encodeInlineLOBParameterValue(clob, "ABC", true, false); err == nil {
		t.Fatalf("CLOB too long error = nil, want error")
	}
}

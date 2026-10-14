package i400

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func mustDecodeText(t *testing.T, raw []byte) string {
	t.Helper()
	text, err := decodeTextByCCSID(raw, 37)
	if err != nil {
		t.Fatalf("decodeTextByCCSID() error = %v", err)
	}
	return text
}

// JTOpen SQLTimestamp.convertToRawBytes truncates the formatted value to the column length.
func TestTimestampParameterTruncatesToColumnLength(t *testing.T) {
	value := time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC)

	cases := []struct {
		name   string
		value  any
		length int
		want   string
	}{
		{"time.Time TIMESTAMP(6)", value, 26, "2024-01-02-03.04.05.123456"},
		{"time.Time TIMESTAMP(3)", value, 23, "2024-01-02-03.04.05.123"},
		{"time.Time TIMESTAMP(0)", value, 19, "2024-01-02-03.04.05"},
		{"time.Time length below 19", value, 10, "2024-01-02"},
		{"string TIMESTAMP(6)", "2024-01-02-03.04.05.123456789", 26, "2024-01-02-03.04.05.123456"},
		{"string fits", "2024-01-02-03.04.05.5", 26, "2024-01-02-03.04.05.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field := parameterMarkerField{SQLType: 392, Length: tc.length, CCSID: 37}
			encoded, err := encodeTimestampParameter(tc.value, field)
			if err != nil {
				t.Fatalf("encodeTimestampParameter() error = %v", err)
			}
			if len(encoded) != tc.length {
				t.Fatalf("encoded length = %d, want %d", len(encoded), tc.length)
			}
			if got := mustDecodeText(t, encoded); strings.TrimRight(got, " ") != tc.want {
				t.Fatalf("encoded text = %q, want %q", got, tc.want)
			}
		})
	}
}

// JTOpen SQLBinary.set truncates values longer than the column and zero-pads shorter ones.
func TestFixedBinaryParameterTruncatesAndPads(t *testing.T) {
	field := parameterMarkerField{SQLType: 912, Length: 4}

	got, err := encodeFixedBinaryParameter([]byte{1, 2, 3, 4, 5, 6}, field)
	if err != nil {
		t.Fatalf("encodeFixedBinaryParameter(long) error = %v", err)
	}
	if want := []byte{1, 2, 3, 4}; !bytes.Equal(got, want) {
		t.Fatalf("long value = %v, want %v", got, want)
	}

	got, err = encodeFixedBinaryParameter([]byte{9}, field)
	if err != nil {
		t.Fatalf("encodeFixedBinaryParameter(short) error = %v", err)
	}
	if want := []byte{9, 0, 0, 0}; !bytes.Equal(got, want) {
		t.Fatalf("short value = %v, want %v", got, want)
	}
}

func TestVarLengthParametersRejectOverflowingPrefix(t *testing.T) {
	long := strings.Repeat("a", 70000)

	if _, err := encodeVarcharParameter(long, parameterMarkerField{SQLType: 448, Length: 0, CCSID: 37}); err == nil {
		t.Fatal("encodeVarcharParameter() error = nil, want error for 70000 bytes with length 0")
	}
	if _, err := encodeVarbinaryParameter(make([]byte, 70000), parameterMarkerField{SQLType: 908, Length: 0}); err == nil {
		t.Fatal("encodeVarbinaryParameter() error = nil, want error for 70000 bytes with length 0")
	}
	// 140000 characters encode to 140000 bytes, i.e. 70000 graphic characters, above the 2-byte prefix limit.
	if _, err := encodeVarGraphicParameter(strings.Repeat("a", 140000), parameterMarkerField{SQLType: 464, Length: 0, CCSID: 37}); err == nil {
		t.Fatal("encodeVarGraphicParameter() error = nil, want error for 70000 graphic characters with length 0")
	}

	// Values within the prefix limit still encode.
	got, err := encodeVarcharParameter(strings.Repeat("a", 65535), parameterMarkerField{SQLType: 448, Length: 0, CCSID: 37})
	if err != nil {
		t.Fatalf("encodeVarcharParameter(65535) error = %v", err)
	}
	if n := binary.BigEndian.Uint16(got[:2]); n != 65535 {
		t.Fatalf("length prefix = %d, want 65535", n)
	}
}

// JTOpen BinaryConverter.stringToBytes accepts 0x and X'..' prefixes and left-pads odd-length input.
func TestBinaryStringParameterHexForms(t *testing.T) {
	cases := map[string][]byte{
		"0x0ABC":  {0x0A, 0xBC},
		"0X0abc":  {0x0A, 0xBC},
		"X'0ABC'": {0x0A, 0xBC},
		"0ABC":    {0x0A, 0xBC},
		"ABC":     {0x0A, 0xBC},
		"0A BC":   {0x0A, 0xBC},
		"  FF  ":  {0xFF},
	}
	for in, want := range cases {
		got, err := parameterFieldBinaryBytes(in)
		if err != nil {
			t.Fatalf("parameterFieldBinaryBytes(%q) error = %v", in, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("parameterFieldBinaryBytes(%q) = %v, want %v", in, got, want)
		}
	}

	if _, err := parameterFieldBinaryBytes("0xZZ"); err == nil {
		t.Fatal("parameterFieldBinaryBytes(\"0xZZ\") error = nil, want error")
	}
	if _, err := parameterFieldBinaryBytes("X'0A"); err == nil {
		t.Fatal("parameterFieldBinaryBytes(\"X'0A\") error = nil, want error")
	}
}

// JTOpen AS400PackedDecimal.toObject accepts sign nibbles 0xA-0xF (0xB/0xD negative) and digits 0-9 only.
func TestDecodePackedDecimalValidatesNibbles(t *testing.T) {
	valid := []struct {
		data []byte
		want string
	}{
		{[]byte{0x12, 0x3C}, "123"},
		{[]byte{0x12, 0x3F}, "123"},
		{[]byte{0x12, 0x3D}, "-123"},
		{[]byte{0x12, 0x3B}, "-123"},
		{[]byte{0x12, 0x3A}, "123"},
	}
	for _, tc := range valid {
		got, err := decodePackedDecimalString(tc.data, 3, 0)
		if err != nil || got != tc.want {
			t.Fatalf("decodePackedDecimalString(%x) = (%q, %v), want (%q, nil)", tc.data, got, err, tc.want)
		}
	}

	invalid := [][]byte{
		{0x12, 0x30},       // sign nibble 0x0 is not a valid sign
		{0x12, 0x00},       // sign nibble 0x0 is not a valid sign
		{0x1A, 0x3C},       // digit nibble 0xA in a non-sign position
		{0xC2, 0x3C},       // high digit nibble 0xC
		{0x12, 0x3F, 0x00}, // sign nibble 0x0 in the final byte
		{0x00, 0x00},       // all-zero field has no valid sign
	}
	for _, data := range invalid {
		if got, err := decodePackedDecimalString(data, 30, 0); err == nil {
			t.Fatalf("decodePackedDecimalString(%x) = %q, want error", data, got)
		}
	}
}

func TestGeneratedKeyZeroFieldIsNotPresent(t *testing.T) {
	// All-zero SQLCA key bytes have no valid sign nibble, so no generated key is reported (JTOpen getGeneratedKey returns null).
	if _, err := decodePackedDecimalString(make([]byte, 16), 30, 0); err == nil {
		t.Fatal("decodePackedDecimalString(zeros) error = nil, want error")
	}
	result := &execResult{}
	if _, err := result.LastInsertId(); err == nil {
		t.Fatal("LastInsertId() error = nil, want ErrUnsupported")
	}
}

func TestCurrentLobLengthPayloadValidation(t *testing.T) {
	ok := appendTestLLCP(nil, CodePointCurrentLOBLength, append(appendU16(nil, 4), appendU32(nil, 3)...))
	if got, err := parseCurrentLobLengthFromPayload(ok); err != nil || got != 3 {
		t.Fatalf("parseCurrentLobLengthFromPayload(4-byte) = (%d, %v), want (3, nil)", got, err)
	}

	big := make([]byte, 8)
	binary.BigEndian.PutUint64(big, 1<<63)
	overflow := appendTestLLCP(nil, CodePointCurrentLOBLength, append(appendU16(nil, 4), big...))
	if _, err := parseCurrentLobLengthFromPayload(overflow); err == nil {
		t.Fatal("parseCurrentLobLengthFromPayload(uint64 > MaxInt64) error = nil, want error")
	}

	short := appendTestLLCP(nil, CodePointCurrentLOBLength, append(appendU16(nil, 4), 0, 0))
	if _, err := parseCurrentLobLengthFromPayload(short); err == nil {
		t.Fatal("parseCurrentLobLengthFromPayload(malformed length) error = nil, want error")
	}

	if got, err := parseCurrentLobLengthFromPayload(nil); err != nil || got != -1 {
		t.Fatalf("parseCurrentLobLengthFromPayload(empty) = (%d, %v), want (-1, nil)", got, err)
	}
}

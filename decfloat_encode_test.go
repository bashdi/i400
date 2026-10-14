package i400

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
)

func mustDecodeDecfloatRoundTrip(t *testing.T, encoded []byte) string {
	t.Helper()
	var (
		got string
		err error
	)
	switch len(encoded) {
	case 8:
		got, err = decodeDecfloat16(encoded)
	case 16:
		got, err = decodeDecfloat34(encoded)
	default:
		t.Fatalf("unexpected encoded length %d", len(encoded))
	}
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	return got
}

// sameDecimalValue reports whether two decimal strings denote the same finite value.
func sameDecimalValue(t *testing.T, a, b string) bool {
	t.Helper()
	ra, ok := new(big.Rat).SetString(a)
	if !ok {
		t.Fatalf("cannot parse %q as a rational", a)
	}
	rb, ok := new(big.Rat).SetString(b)
	if !ok {
		t.Fatalf("cannot parse %q as a rational", b)
	}
	return ra.Cmp(rb) == 0
}

func TestEncodeDecfloatRoundTripDecimal64(t *testing.T) {
	values := []string{
		"0", "-0", "1", "-1", "123.45", "-0.001", "1.5e-3", "1E+10",
		"9999999999999999", "1E-383", "1E+384", "1E-398", "2.5E-300",
		"1234567890123456", "-9.999999999999999E+369", "0.000000000000001",
	}
	for _, v := range values {
		encoded, err := encodeDecfloat(v, 8)
		if err != nil {
			t.Fatalf("encodeDecfloat(%q, 8) error = %v", v, err)
		}
		if len(encoded) != 8 {
			t.Fatalf("encodeDecfloat(%q, 8) length = %d", v, len(encoded))
		}
		got := mustDecodeDecfloatRoundTrip(t, encoded)
		if !sameDecimalValue(t, got, v) {
			t.Errorf("round trip %q: decoded %q", v, got)
		}
	}
}

func TestEncodeDecfloatRoundTripDecimal128(t *testing.T) {
	values := []string{
		"0", "-0", "1", "-1", "123.45", "-0.001", "1.5e-3", "1E+10",
		"9999999999999999999999999999999999", // 34 digits
		"1234567890123456789012345678901234",
		"-1234567890123456789012345678901234E+6000",
		"1E-6143", "1E+6111", "1E-6176",
		"9999999999999999999999999999999999E-6176",
		"3.141592653589793238462643383279502",
	}
	for _, v := range values {
		encoded, err := encodeDecfloat(v, 16)
		if err != nil {
			t.Fatalf("encodeDecfloat(%q, 16) error = %v", v, err)
		}
		if len(encoded) != 16 {
			t.Fatalf("encodeDecfloat(%q, 16) length = %d", v, len(encoded))
		}
		got := mustDecodeDecfloatRoundTrip(t, encoded)
		if !sameDecimalValue(t, got, v) {
			t.Errorf("round trip %q: decoded %q", v, got)
		}
	}
}

func TestEncodeDecfloatSpecialValues(t *testing.T) {
	tests := []struct {
		value string
		size  int
		want  string
	}{
		{"NaN", 8, "NaN"},
		{"nan", 16, "NaN"},
		{"-NaN", 8, "-NaN"},
		{"sNaN", 8, "SNaN"},
		{"-sNaN", 16, "-SNaN"},
		{"Infinity", 8, "Infinity"},
		{"+Infinity", 16, "Infinity"},
		{"-Infinity", 8, "-Infinity"},
		{"-Inf", 16, "-Infinity"},
	}
	for _, tt := range tests {
		encoded, err := encodeDecfloat(tt.value, tt.size)
		if err != nil {
			t.Fatalf("encodeDecfloat(%q, %d) error = %v", tt.value, tt.size, err)
		}
		if got := mustDecodeDecfloatRoundTrip(t, encoded); got != tt.want {
			t.Errorf("encodeDecfloat(%q, %d) decodes to %q, want %q", tt.value, tt.size, got, tt.want)
		}
	}
}

func TestEncodeDecfloatNegativeZeroSignBit(t *testing.T) {
	encoded, err := encodeDecfloat("-0", 8)
	if err != nil {
		t.Fatalf("encodeDecfloat error = %v", err)
	}
	if encoded[0]&0x80 == 0 {
		t.Fatalf("encodeDecfloat(\"-0\") sign bit not set: % x", encoded)
	}
}

func TestEncodeDecfloatRoundsHalfEven(t *testing.T) {
	tests := []struct {
		value string
		size  int
		want  string
	}{
		// 17 digits into DECFLOAT(16): dropped digit is exactly 5, kept digit 6 is even.
		{"12345678901234565", 8, "12345678901234560"},
		// kept digit 7 is odd, so round up.
		{"12345678901234575", 8, "12345678901234580"},
		// 35 digits into DECFLOAT(34): dropped digit 5, kept digit 4 is even.
		{"12345678901234567890123456789012345", 16, "12345678901234567890123456789012340"},
		// dropped digits above half round up.
		{"12345678901234567890123456789012346", 16, "12345678901234567890123456789012350"},
	}
	for _, tt := range tests {
		encoded, err := encodeDecfloat(tt.value, tt.size)
		if err != nil {
			t.Fatalf("encodeDecfloat(%q, %d) error = %v", tt.value, tt.size, err)
		}
		got := mustDecodeDecfloatRoundTrip(t, encoded)
		if !sameDecimalValue(t, got, tt.want) {
			t.Errorf("encodeDecfloat(%q, %d) decodes to %q, want %q", tt.value, tt.size, got, tt.want)
		}
	}
}

func TestEncodeDecfloatOutOfRange(t *testing.T) {
	tests := []struct {
		value string
		size  int
	}{
		{"1E+385", 8},   // above the DECFLOAT(16) range
		{"1E+6145", 16}, // above the DECFLOAT(34) range
		{"abc", 8},      // not a number
		{"", 16},        // empty
		{"1.2.3", 8},    // malformed
		{"1", 4},        // unsupported size
	}
	for _, tt := range tests {
		if _, err := encodeDecfloat(tt.value, tt.size); err == nil {
			t.Errorf("encodeDecfloat(%q, %d) succeeded, want error", tt.value, tt.size)
		}
	}
}

func TestEncodeDecfloatUnderflowRoundsToZero(t *testing.T) {
	encoded, err := encodeDecfloat("1E-400", 8)
	if err != nil {
		t.Fatalf("encodeDecfloat(\"1E-400\", 8) error = %v", err)
	}
	if got := mustDecodeDecfloatRoundTrip(t, encoded); !sameDecimalValue(t, got, "0") {
		t.Fatalf("encodeDecfloat(\"1E-400\", 8) decodes to %q, want 0", got)
	}
}

func TestEncodeDecfloatKnownVectors(t *testing.T) {
	tests := []struct {
		value string
		size  int
		want  string
	}{
		{"1", 8, "2238000000000001"},
		{"1", 16, "22080000000000000000000000000001"},
	}
	for _, tt := range tests {
		encoded, err := encodeDecfloat(tt.value, tt.size)
		if err != nil {
			t.Fatalf("encodeDecfloat(%q, %d) error = %v", tt.value, tt.size, err)
		}
		want, err := hex.DecodeString(tt.want)
		if err != nil {
			t.Fatalf("bad test vector: %v", err)
		}
		if !bytes.Equal(encoded, want) {
			t.Errorf("encodeDecfloat(%q, %d) = % x, want % x", tt.value, tt.size, encoded, want)
		}
	}
}

func TestEncodeDecfloatParameterFromGoValues(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeDecfloat, CCSID: 1208, Length: 8}
	tests := []struct {
		value any
		want  string
	}{
		{int64(42), "42"},
		{float64(1e-7), "1e-07"},
		{"  -12.5 ", "-12.5"},
		{[]byte("7E2"), "700"},
	}
	for _, tt := range tests {
		encoded, err := encodeDecfloatParameter(tt.value, field)
		if err != nil {
			t.Fatalf("encodeDecfloatParameter(%v) error = %v", tt.value, err)
		}
		got := mustDecodeDecfloatRoundTrip(t, encoded)
		if !sameDecimalValue(t, got, tt.want) {
			t.Errorf("encodeDecfloatParameter(%v) decodes to %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestDecfloatDecletTableIsInverse(t *testing.T) {
	for v := 0; v < 1000; v++ {
		bits := decfloatDecletTable[v]
		if bits < 0 {
			t.Fatalf("no declet for %d", v)
		}
		if got := decfloatUnpackDeclet(bits); got != v {
			t.Fatalf("declet 0x%03x decodes to %d, want %d", bits, got, v)
		}
	}
}

package i400

import (
	"encoding/binary"
	"testing"
)

// ---------------------------------------------------------------------------
// formatDecfloatStr
// ---------------------------------------------------------------------------

func TestFormatDecfloatStrPureInteger(t *testing.T) {
	// coef "123", exponent=0 → "123"
	if got := formatDecfloatStr(1, "123", 0); got != "123" {
		t.Fatalf("got %q, want %q", got, "123")
	}
}

func TestFormatDecfloatStrPositiveExponent(t *testing.T) {
	// "12" * 10^3 → "12000"
	if got := formatDecfloatStr(1, "12", 3); got != "12000" {
		t.Fatalf("got %q, want %q", got, "12000")
	}
}

func TestFormatDecfloatStrNegativeExponent(t *testing.T) {
	// "12345" * 10^-2 → "123.45"
	if got := formatDecfloatStr(1, "12345", -2); got != "123.45" {
		t.Fatalf("got %q, want %q", got, "123.45")
	}
}

func TestFormatDecfloatStrAllFractional(t *testing.T) {
	// "5" * 10^-3 → "0.005"
	if got := formatDecfloatStr(1, "5", -3); got != "0.005" {
		t.Fatalf("got %q, want %q", got, "0.005")
	}
}

func TestFormatDecfloatStrNegativeValue(t *testing.T) {
	// sign=-1, "42" * 10^-1 → "-4.2"
	if got := formatDecfloatStr(-1, "42", -1); got != "-4.2" {
		t.Fatalf("got %q, want %q", got, "-4.2")
	}
}

func TestFormatDecfloatStrZero(t *testing.T) {
	if got := formatDecfloatStr(1, "0", 0); got != "0" {
		t.Fatalf("got %q, want %q", got, "0")
	}
}

// ---------------------------------------------------------------------------
// decfloatUnpackDeclet
// ---------------------------------------------------------------------------

func TestDecfloatUnpackDecletKnownValues(t *testing.T) {
	tests := []struct {
		bits int
		want int
	}{
		{0x000, 0},   // DPD 0x000 → 000
		{0x001, 1},   // DPD 0x001 → 001
		{0x049, 49},  // DPD 0x049 → 049
		{0x149, 249}, // DPD 0x149 → 249 (verified by algorithm)
	}
	for _, tt := range tests {
		if got := decfloatUnpackDeclet(tt.bits); got != tt.want {
			t.Errorf("decfloatUnpackDeclet(0x%03x) = %d, want %d", tt.bits, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// decodeDecfloat16 — special values
// ---------------------------------------------------------------------------

// buildDecfloat16Bits constructs 8 bytes from a 64-bit value.
func buildDecfloat16Bits(bits uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, bits)
	return b
}

// buildDecfloat34Bits constructs 16 bytes from two 64-bit halves.
func buildDecfloat34Bits(hi, lo uint64) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[:8], hi)
	binary.BigEndian.PutUint64(b[8:], lo)
	return b
}

func TestDecodeDecfloat16NaN(t *testing.T) {
	// combination = 0x1f → NaN. combination occupies bits 62-58.
	// 0x1f << 58 = 0x7c00000000000000
	bits := uint64(0x7c00000000000000)
	got, err := decodeDecfloat16(buildDecfloat16Bits(bits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "NaN" {
		t.Fatalf("got %q, want NaN", got)
	}
}

func TestDecodeDecfloat16NegativeNaN(t *testing.T) {
	// sign bit + combination = 0x1f
	bits := uint64(0xfc00000000000000)
	got, err := decodeDecfloat16(buildDecfloat16Bits(bits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "-NaN" {
		t.Fatalf("got %q, want -NaN", got)
	}
}

func TestDecodeDecfloat16Infinity(t *testing.T) {
	// combination = 0x1e → Infinity.  0x1e << 58 = 0x7800000000000000
	bits := uint64(0x7800000000000000)
	got, err := decodeDecfloat16(buildDecfloat16Bits(bits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Infinity" {
		t.Fatalf("got %q, want Infinity", got)
	}
}

func TestDecodeDecfloat16NegativeInfinity(t *testing.T) {
	bits := uint64(0xf800000000000000)
	got, err := decodeDecfloat16(buildDecfloat16Bits(bits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "-Infinity" {
		t.Fatalf("got %q, want -Infinity", got)
	}
}

func TestDecodeDecfloat16PositiveZero(t *testing.T) {
	// All-zero bytes → combination=0, exponent continuation=0, coefficient=0
	// exponent = 0 - 398 = -398; coefficient = 0 → value = 0
	// Expected decimal representation: "0" (with 398 leading zeros suppressed)
	got, err := decodeDecfloat16(make([]byte, 8))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The all-zero encoding is coef=0, exp = -398; formatDecfloatStr("0", -398) = "0.000...0"
	// but coefDigits=="0" so scale<n is false and scale>=n path gives "0." + zeros + "0".
	// Accept anything that starts with "0" and contains no non-zero digits.
	for _, c := range got {
		if c != '0' && c != '.' {
			t.Fatalf("decodeDecfloat16(zeros) = %q, expected all-zero decimal", got)
		}
	}
}

// TestDecodeDecfloat16KnownValue encodes "1" (coefficient=1, exponent=0) in
// DECFLOAT(16) and verifies round-trip decode.
//
// IEEE 754 Decimal64 encoding of +1:
//
//	sign=0, coefficient=1, biased_exponent = 0+398 = 398 = 0x18e
//	combination field: exponentMSD = (398>>8) = 1, coefficientMSD = 0
//	Since coefficientMSD < 8: combination = (exponentMSD<<3) | coefficientMSD = 0x08
//	bits[62:58] = 0x08 → shifted: 0x08 << 58 = 0x0200000000000000
//	exponent continuation = 398 & 0xff = 0x8e → placed at bits[57:50]:  0x8e << 50 = 0x0023800000000000
//	coefficient continuation = 1 in DPD:
//	  low 30 bits encode 9 digits = 000000001, DPD of 001 = 0x001; 0x000000001
//	  high 20 bits encode 6 digits = 000000, all zero
//	Result: 0x0200000000000000 | 0x0023800000000000 | 0x0000000000000001
//	      = 0x0223800000000001
func TestDecodeDecfloat16KnownValue1(t *testing.T) {
	bits := uint64(0x2238000000000001)
	got, err := decodeDecfloat16(buildDecfloat16Bits(bits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1" {
		t.Fatalf("decodeDecfloat16(+1) = %q, want \"1\"", got)
	}
}

func TestDecodeDecfloat16TooShort(t *testing.T) {
	_, err := decodeDecfloat16(make([]byte, 4))
	if err == nil {
		t.Fatal("expected error for too-short input")
	}
}

// ---------------------------------------------------------------------------
// decodeDecfloat34 — special values
// ---------------------------------------------------------------------------

func TestDecodeDecfloat34NaN(t *testing.T) {
	bits := uint64(0x7c00000000000000)
	got, err := decodeDecfloat34(buildDecfloat34Bits(bits, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "NaN" {
		t.Fatalf("got %q, want NaN", got)
	}
}

func TestDecodeDecfloat34Infinity(t *testing.T) {
	bits := uint64(0x7800000000000000)
	got, err := decodeDecfloat34(buildDecfloat34Bits(bits, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Infinity" {
		t.Fatalf("got %q, want Infinity", got)
	}
}

func TestDecodeDecfloat34NegativeInfinity(t *testing.T) {
	bits := uint64(0xf800000000000000)
	got, err := decodeDecfloat34(buildDecfloat34Bits(bits, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "-Infinity" {
		t.Fatalf("got %q, want -Infinity", got)
	}
}

func TestDecodeDecfloat34TooShort(t *testing.T) {
	_, err := decodeDecfloat34(make([]byte, 8))
	if err == nil {
		t.Fatal("expected error for too-short input")
	}
}

// ---------------------------------------------------------------------------
// decodeColumnValue — DECFLOAT dispatch
// ---------------------------------------------------------------------------

func TestDecodeColumnValueDecfloat16Infinity(t *testing.T) {
	col := columnMeta{Type: db2TypeDecfloat, Length: 8, Offset: 0}
	row := buildDecfloat16Bits(0x7800000000000000)
	v, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "Infinity" {
		t.Fatalf("got %v, want \"Infinity\"", v)
	}
}

func TestDecodeColumnValueDecfloat34Infinity(t *testing.T) {
	col := columnMeta{Type: db2TypeDecfloat, Length: 16, Offset: 0}
	row := buildDecfloat34Bits(0x7800000000000000, 0)
	v, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "Infinity" {
		t.Fatalf("got %v, want \"Infinity\"", v)
	}
}

func TestDecodeColumnValueDecfloatUnknownLength(t *testing.T) {
	col := columnMeta{Type: db2TypeDecfloat, Length: 4, Offset: 0}
	_, err := decodeColumnValue(col, make([]byte, 4))
	if err == nil {
		t.Fatal("expected error for unexpected DECFLOAT length")
	}
}

func TestDecodeColumnValueDecfloat16Known1(t *testing.T) {
	col := columnMeta{Type: db2TypeDecfloat, Length: 8, Offset: 0}
	row := buildDecfloat16Bits(0x2238000000000001)
	v, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "1" {
		t.Fatalf("decodeColumnValue DECFLOAT(16) +1 = %v, want \"1\"", v)
	}
}

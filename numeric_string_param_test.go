package i400

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

type numericTypeCase struct {
	name  string
	field parameterMarkerField
}

func numericTypeCases() []numericTypeCase {
	return []numericTypeCase{
		{"SMALLINT", parameterMarkerField{SQLType: db2TypeSmallint, Length: 2, CCSID: 37}},
		{"INTEGER", parameterMarkerField{SQLType: db2TypeInteger, Length: 4, CCSID: 37}},
		{"BIGINT", parameterMarkerField{SQLType: db2TypeBigint, Length: 8, CCSID: 37}},
		{"DECIMAL", parameterMarkerField{SQLType: db2TypeDecimal, Length: 3, Precision: 5, Scale: 2, CCSID: 37}},
		{"NUMERIC", parameterMarkerField{SQLType: db2TypeNumeric, Length: 5, Precision: 5, Scale: 2, CCSID: 37}},
		{"REAL", parameterMarkerField{SQLType: db2TypeFloatingPoint, Length: 4, CCSID: 37}},
		{"DOUBLE", parameterMarkerField{SQLType: db2TypeFloatingPoint, Length: 8, CCSID: 37}},
		{"DECFLOAT", parameterMarkerField{SQLType: db2TypeDecfloat, Length: 8, Precision: 16, CCSID: 1208}},
	}
}

// Empty and whitespace-only strings must be a binding error for every numeric
// type, never a silent 0 (JTOpen throws EXC_DATA_TYPE_MISMATCH / 07006).
func TestNumericParameterEmptyStringIsError(t *testing.T) {
	for _, tc := range numericTypeCases() {
		for _, v := range []any{"", "   ", "\t\n", []byte(""), []byte("  ")} {
			_, err := encodeParameterValue(tc.field, v, false)
			if err == nil {
				t.Errorf("%s: encode(%q) error = nil, want invalid number error", tc.name, v)
				continue
			}
			want := "not a valid number for " + tc.name
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: encode(%q) error = %q, want it to contain %q", tc.name, v, err, want)
			}
		}
	}
}

func TestNumericParameterJunkStringIsError(t *testing.T) {
	junk := []string{"abc", "1.2.3", "--1", "1e", "1..2", "1 2", "0x10", ".", "+", "-"}
	for _, tc := range numericTypeCases() {
		for _, v := range junk {
			_, err := encodeParameterValue(tc.field, v, false)
			if err == nil {
				t.Errorf("%s: encode(%q) error = nil, want error", tc.name, v)
				continue
			}
			if !strings.Contains(err.Error(), "not a valid number for "+tc.name) {
				t.Errorf("%s: encode(%q) error = %q, want invalid number error", tc.name, v, err)
			}
		}
	}
}

func TestNumericParameterNullStillZeroFilled(t *testing.T) {
	for _, tc := range numericTypeCases() {
		buf, err := encodeParameterValue(tc.field, nil, true)
		if err != nil {
			t.Errorf("%s: NULL encode error = %v", tc.name, err)
			continue
		}
		for _, b := range buf {
			if b != 0 {
				t.Errorf("%s: NULL encode = % x, want zero-filled", tc.name, buf)
				break
			}
		}
	}
}

func packedField() parameterMarkerField {
	return parameterMarkerField{SQLType: db2TypeDecimal, Length: 3, Precision: 5, Scale: 2, CCSID: 37}
}

func zonedField() parameterMarkerField {
	return parameterMarkerField{SQLType: db2TypeNumeric, Length: 5, Precision: 5, Scale: 2, CCSID: 37}
}

func TestDecimalStringParsingAndTruncation(t *testing.T) {
	tests := []struct {
		value any
		want  []byte // packed DECIMAL(5,2) encoding
	}{
		{"123.45", []byte{0x12, 0x34, 0x5C}},
		{"-123.45", []byte{0x12, 0x34, 0x5D}},
		{" 123.456 ", []byte{0x12, 0x34, 0x5C}}, // extra scale digit truncated, not rounded
		{"2.279", []byte{0x00, 0x22, 0x7C}},     // ROUND_DOWN, not 2.28
		{"0.135", []byte{0x00, 0x01, 0x3C}},     // ROUND_DOWN, not 0.14
		{"1.5e1", []byte{0x01, 0x50, 0x0C}},     // exponent form
		{"+.5", []byte{0x00, 0x05, 0x0C}},       // no integer digits
		{"5.", []byte{0x00, 0x50, 0x0C}},        // trailing point
		{"-0.001", []byte{0x00, 0x00, 0x0C}},    // truncates to zero; zero is not negative
		{"1e-400", []byte{0x00, 0x00, 0x0C}},    // underflow truncates to zero
		{float64(2.5), []byte{0x00, 0x25, 0x0C}},
		{int64(-7), []byte{0x00, 0x70, 0x0D}},
		{true, []byte{0x00, 0x10, 0x0C}},
	}
	for _, tt := range tests {
		got, err := encodeParameterValue(packedField(), tt.value, false)
		if err != nil {
			t.Errorf("encode DECIMAL(%v) error = %v", tt.value, err)
			continue
		}
		if !bytes.Equal(got, tt.want) {
			t.Errorf("encode DECIMAL(%v) = % x, want % x", tt.value, got, tt.want)
		}
	}
}

func TestDecimalStringIntegerOverflowIsError(t *testing.T) {
	// DECIMAL(5,2) holds at most 3 integer digits.
	for _, v := range []string{"1000", "1234.5", "-99999", "1e3", "100000000000000000000000000000000000000000000000000"} {
		if _, err := encodeParameterValue(packedField(), v, false); err == nil {
			t.Errorf("DECIMAL(5,2) encode(%q) error = nil, want out of range", v)
		}
		if _, err := encodeParameterValue(zonedField(), v, false); err == nil {
			t.Errorf("NUMERIC(5,2) encode(%q) error = nil, want out of range", v)
		}
	}
	// The largest valid value still encodes.
	if _, err := encodeParameterValue(packedField(), "999.99", false); err != nil {
		t.Errorf("DECIMAL(5,2) encode(999.99) error = %v", err)
	}
}

func TestNumericDecimalStringParsing(t *testing.T) {
	tests := []struct {
		value string
		want  []byte // zoned NUMERIC(5,2) encoding
	}{
		{"1.5", []byte{0xF0, 0xF0, 0xF1, 0xF5, 0xF0}},
		{"-1.5", []byte{0xF0, 0xF0, 0xF1, 0xF5, 0xD0}},
		{"1.239", []byte{0xF0, 0xF0, 0xF1, 0xF2, 0xF3}},
	}
	for _, tt := range tests {
		got, err := encodeParameterValue(zonedField(), tt.value, false)
		if err != nil {
			t.Errorf("encode NUMERIC(%q) error = %v", tt.value, err)
			continue
		}
		if !bytes.Equal(got, tt.want) {
			t.Errorf("encode NUMERIC(%q) = % x, want % x", tt.value, got, tt.want)
		}
	}
}

func TestFloatStringNaNAndInfParsing(t *testing.T) {
	double := parameterMarkerField{SQLType: db2TypeFloatingPoint, Length: 8, CCSID: 37}
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf", "-Inf", "Infinity", "-infinity"} {
		buf, err := encodeParameterValue(double, v, false)
		if err != nil {
			t.Errorf("DOUBLE encode(%q) error = %v, want Go ParseFloat behaviour", v, err)
			continue
		}
		f := math.Float64frombits(binary.BigEndian.Uint64(buf))
		switch {
		case strings.Contains(strings.ToLower(v), "nan"):
			if !math.IsNaN(f) {
				t.Errorf("DOUBLE encode(%q) = %v, want NaN", v, f)
			}
		case strings.HasPrefix(v, "-"):
			if !math.IsInf(f, -1) {
				t.Errorf("DOUBLE encode(%q) = %v, want -Inf", v, f)
			}
		default:
			if !math.IsInf(f, 1) {
				t.Errorf("DOUBLE encode(%q) = %v, want +Inf", v, f)
			}
		}
	}
}

func TestFloatStringInvalidAndEmpty(t *testing.T) {
	real4 := parameterMarkerField{SQLType: db2TypeFloatingPoint, Length: 4, CCSID: 37}
	double := parameterMarkerField{SQLType: db2TypeFloatingPoint, Length: 8, CCSID: 37}
	for _, field := range []parameterMarkerField{real4, double} {
		for _, v := range []string{"", "  ", "abc", "1.2.3", "1,5x"} {
			if _, err := encodeParameterValue(field, v, false); err == nil {
				t.Errorf("float(len %d) encode(%q) error = nil, want error", field.Length, v)
			}
		}
	}
	if _, err := encodeParameterValue(double, "1e400", false); err == nil {
		t.Errorf("DOUBLE encode(1e400) error = nil, want out of range")
	}
}

func TestDecfloatStringEmptyAndJunkAreErrors(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeDecfloat, Length: 8, Precision: 16, CCSID: 1208}
	for _, v := range []string{"", "   ", "abc", "1.2.3", "--1"} {
		if _, err := encodeParameterValue(field, v, false); err == nil {
			t.Errorf("DECFLOAT encode(%q) error = nil, want error", v)
		}
	}
}

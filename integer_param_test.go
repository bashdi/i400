package i400

import (
	"database/sql/driver"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

type integerParamEncoder func(value driver.Value, field parameterMarkerField) ([]byte, error)

func integerParamField(sqlType int) parameterMarkerField {
	switch sqlType {
	case db2TypeSmallint:
		return parameterMarkerField{SQLType: db2TypeSmallint, Length: 2, CCSID: 37}
	case db2TypeInteger:
		return parameterMarkerField{SQLType: db2TypeInteger, Length: 4, CCSID: 37}
	default:
		return parameterMarkerField{SQLType: db2TypeBigint, Length: 8, CCSID: 37}
	}
}

// trailingInt decodes the big-endian value stored at the end of buf, as the
// encoders write it.
func trailingInt(t *testing.T, buf []byte, width int) int64 {
	t.Helper()
	if len(buf) < width {
		t.Fatalf("encoded length %d < %d", len(buf), width)
	}
	tail := buf[len(buf)-width:]
	switch width {
	case 2:
		return int64(int16(binary.BigEndian.Uint16(tail)))
	case 4:
		return int64(int32(binary.BigEndian.Uint32(tail)))
	default:
		return int64(binary.BigEndian.Uint64(tail))
	}
}

func TestIntegerParameterInRange(t *testing.T) {
	tests := []struct {
		name    string
		enc     integerParamEncoder
		sqlType int
		width   int
		value   any
		want    int64
	}{
		{"smallint max", encodeSmallintParameter, db2TypeSmallint, 2, int64(math.MaxInt16), math.MaxInt16},
		{"smallint min", encodeSmallintParameter, db2TypeSmallint, 2, int64(math.MinInt16), math.MinInt16},
		{"integer max", encodeIntegerParameter, db2TypeInteger, 4, int64(math.MaxInt32), math.MaxInt32},
		{"integer min", encodeIntegerParameter, db2TypeInteger, 4, int64(math.MinInt32), math.MinInt32},
		{"bigint max", encodeBigintParameter, db2TypeBigint, 8, int64(math.MaxInt64), math.MaxInt64},
		{"bigint min", encodeBigintParameter, db2TypeBigint, 8, int64(math.MinInt64), math.MinInt64},
		{"bool true", encodeIntegerParameter, db2TypeInteger, 4, true, 1},
		{"bytes", encodeIntegerParameter, db2TypeInteger, 4, []byte("-7"), -7},
		{"string plus sign", encodeIntegerParameter, db2TypeInteger, 4, "+3", 3},
		{"string trimmed", encodeIntegerParameter, db2TypeInteger, 4, " 12 ", 12},
		{"string negative", encodeIntegerParameter, db2TypeInteger, 4, "-7", -7},
		{"string 2.00", encodeIntegerParameter, db2TypeInteger, 4, "2.00", 2},
		{"string 2.0", encodeSmallintParameter, db2TypeSmallint, 2, "2.0", 2},
		{"string 1e3", encodeIntegerParameter, db2TypeInteger, 4, "1e3", 1000},
		{"string 1.5e1", encodeIntegerParameter, db2TypeInteger, 4, "1.5E1", 15},
		{"string 0.0", encodeIntegerParameter, db2TypeInteger, 4, "0.0", 0},
		{"string bigint max", encodeBigintParameter, db2TypeBigint, 8, "9223372036854775807", math.MaxInt64},
		{"string bigint min", encodeBigintParameter, db2TypeBigint, 8, "-9223372036854775808", math.MinInt64},
		{"float 42", encodeIntegerParameter, db2TypeInteger, 4, float64(42), 42},
		{"float -1024", encodeSmallintParameter, db2TypeSmallint, 2, float64(-1024), -1024},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, err := tt.enc(tt.value, integerParamField(tt.sqlType))
			if err != nil {
				t.Fatalf("encode(%v) error = %v", tt.value, err)
			}
			if got := trailingInt(t, buf, tt.width); got != tt.want {
				t.Fatalf("encode(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestIntegerParameterOutOfRange(t *testing.T) {
	tests := []struct {
		name    string
		enc     integerParamEncoder
		sqlType int
		value   any
		wantMsg string
	}{
		{"smallint above", encodeSmallintParameter, db2TypeSmallint, int64(math.MaxInt16 + 1), "SMALLINT"},
		{"smallint below", encodeSmallintParameter, db2TypeSmallint, int64(math.MinInt16 - 1), "SMALLINT"},
		{"smallint string", encodeSmallintParameter, db2TypeSmallint, "40000", "SMALLINT"},
		{"integer above", encodeIntegerParameter, db2TypeInteger, int64(math.MaxInt32 + 1), "INTEGER"},
		{"integer below", encodeIntegerParameter, db2TypeInteger, int64(math.MinInt32 - 1), "INTEGER"},
		{"integer float", encodeIntegerParameter, db2TypeInteger, float64(1 << 31), "INTEGER"},
		{"integer string", encodeIntegerParameter, db2TypeInteger, "3000000000", "INTEGER"},
		{"bigint string overflow", encodeBigintParameter, db2TypeBigint, "9223372036854775808", "64-bit"},
		{"bigint string huge", encodeBigintParameter, db2TypeBigint, "123456789012345678901234567890", "64-bit"},
		{"bigint string neg overflow", encodeBigintParameter, db2TypeBigint, "-9223372036854775809", "64-bit"},
		{"bigint string exponent", encodeBigintParameter, db2TypeBigint, "1e19", "64-bit"},
		{"bigint float 2^63", encodeBigintParameter, db2TypeBigint, float64(1 << 63), "64-bit"},
		{"bigint float -2^63 - 2048", encodeBigintParameter, db2TypeBigint, float64(-(1 << 63)) - 2048, "64-bit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.enc(tt.value, integerParamField(tt.sqlType))
			if err == nil {
				t.Fatalf("encode(%v) error = nil, want out of range", tt.value)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Fatalf("encode(%v) error = %v, want it to mention %q", tt.value, err, tt.wantMsg)
			}
		})
	}
}

func TestIntegerParameterRejectsFraction(t *testing.T) {
	values := []any{"1.9", "-0.5", "1.0000001", "1.5e0", float64(1.5), float64(-2.25)}
	encoders := []struct {
		enc     integerParamEncoder
		sqlType int
	}{
		{encodeSmallintParameter, db2TypeSmallint},
		{encodeIntegerParameter, db2TypeInteger},
		{encodeBigintParameter, db2TypeBigint},
	}
	for _, e := range encoders {
		for _, v := range values {
			if _, err := e.enc(v, integerParamField(e.sqlType)); err == nil {
				t.Errorf("encode(%v) for type %d error = nil, want fractional error", v, e.sqlType)
			}
		}
	}
}

func TestIntegerParameterRejectsInvalidText(t *testing.T) {
	for _, v := range []string{"abc", "", "   ", "-", "1 2", "1/2", "0x10", "1..2", "1e", "1e+-3", "NaN", "Inf", "--1"} {
		if _, err := parameterFieldInt64(v); err == nil {
			t.Errorf("parameterFieldInt64(%q) error = nil, want error", v)
		}
	}
}

func TestIntegerParameterFloatNaNAndInf(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := encodeIntegerParameter(v, integerParamField(db2TypeInteger)); err == nil {
			t.Errorf("encodeIntegerParameter(%v) error = nil, want error", v)
		}
		if _, err := encodeBigintParameter(v, integerParamField(db2TypeBigint)); err == nil {
			t.Errorf("encodeBigintParameter(%v) error = nil, want error", v)
		}
	}
}

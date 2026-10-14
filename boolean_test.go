package i400

import (
	"bytes"
	"reflect"
	"testing"
)

func TestDecodeBooleanColumn(t *testing.T) {
	col := columnMeta{Type: db2TypeBoolean, Length: 1, Offset: 0}
	tests := []struct {
		name string
		raw  []byte
		want bool
	}{
		{"true", []byte{0xF1}, true},
		{"false", []byte{0xF0}, false},
		{"zero byte is true", []byte{0x00}, true},
		{"other byte is true", []byte{0x01}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeColumnValue(col, tt.raw)
			if err != nil {
				t.Fatalf("decodeColumnValue() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("decodeColumnValue() = %#v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeBooleanColumnNullableTypeCode(t *testing.T) {
	col := columnMeta{Type: db2TypeBoolean | 1, Length: 1, Offset: 0}
	got, err := decodeColumnValue(col, []byte{0xF1})
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if got != true {
		t.Fatalf("decodeColumnValue() = %#v, want true", got)
	}
}

func TestDecodeBooleanColumnEmptyRaw(t *testing.T) {
	col := columnMeta{Type: db2TypeBoolean, Length: 0, Offset: 0}
	if _, err := decodeColumnValue(col, []byte{}); err == nil {
		t.Fatal("decodeColumnValue() with empty BOOLEAN value returned no error")
	}
}

func TestEncodeBooleanParameter(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeBoolean | 1, Length: 1}
	tests := []struct {
		name  string
		value any
		want  []byte
	}{
		{"bool true", true, []byte{0xF1}},
		{"bool false", false, []byte{0xF0}},
		{"int64 zero", int64(0), []byte{0xF0}},
		{"int64 non-zero", int64(-7), []byte{0xF1}},
		{"string true", "TRUE", []byte{0xF1}},
		{"string 1", "1", []byte{0xF1}},
		{"string false", " False ", []byte{0xF0}},
		{"string 0", "0", []byte{0xF0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeParameterValue(field, tt.value, false)
			if err != nil {
				t.Fatalf("encodeParameterValue() error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("encodeParameterValue() = % X, want % X", got, tt.want)
			}
		})
	}
}

func TestEncodeBooleanParameterInvalidString(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeBoolean, Length: 1}
	if _, err := encodeParameterValue(field, "yes", false); err == nil {
		t.Fatal("encodeParameterValue() with invalid BOOLEAN string returned no error")
	}
}

func TestEncodeBooleanParameterNullUsesOneByte(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeBoolean, Length: 1}
	got, err := encodeParameterValue(field, nil, true)
	if err != nil {
		t.Fatalf("encodeParameterValue(NULL) error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("NULL BOOLEAN encoded to %d bytes, want 1", len(got))
	}
}

func TestBooleanDatabaseTypeNameAndScanType(t *testing.T) {
	rows := &queryRows{
		meta: &resultSetMeta{
			columns: []columnMeta{
				{Name: "FLAG", Type: db2TypeBoolean, Length: 1},
				{Name: "FLAG_NULL", Type: db2TypeBoolean | 1, Length: 1},
			},
		},
	}
	for i := range 2 {
		if got := rows.ColumnTypeDatabaseTypeName(i); got != "BOOLEAN" {
			t.Fatalf("ColumnTypeDatabaseTypeName(%d) = %q, want BOOLEAN", i, got)
		}
		if got := rows.ColumnTypeScanType(i); got != reflect.TypeOf(false) {
			t.Fatalf("ColumnTypeScanType(%d) = %v, want bool", i, got)
		}
	}
}

func TestBooleanParameterResultLength(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeBoolean | 1}
	if got := parameterFieldResultLength(field); got != 1 {
		t.Fatalf("parameterFieldResultLength() = %d, want 1", got)
	}
}

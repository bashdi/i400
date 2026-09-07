package i400

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"
)

// â”€â”€â”€ parseIBMiTimestamp â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestParseIBMiTimestampNoFraction(t *testing.T) {
	got, err := parseIBMiTimestamp("2026-04-22-13.45.30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseIBMiTimestampMicroseconds(t *testing.T) {
	got, err := parseIBMiTimestamp("2026-04-22-13.45.30.123456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 123456*1000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseIBMiTimestampNanoseconds(t *testing.T) {
	got, err := parseIBMiTimestamp("2026-04-22-13.45.30.123456789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 123456789, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseIBMiTimestampSubNanoTruncated(t *testing.T) {
	// 12-digit fraction â€“ sub-nanosecond digits must be truncated
	got, err := parseIBMiTimestamp("2026-04-22-13.45.30.123456789012")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 123456789, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseIBMiTimestampMilliseconds(t *testing.T) {
	got, err := parseIBMiTimestamp("2026-04-22-13.45.30.100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 100_000_000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// â”€â”€â”€ decodeColumnValue DATE / TIME / TIMESTAMP â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// IBM i CCSID-37 encoding helpers:
// digits 0-9 â†’ 0xF0â€“0xF9, '-' â†’ 0x60, '.' â†’ 0x4B
func ebcdic37Byte(ch rune) byte {
	switch {
	case ch >= '0' && ch <= '9':
		return byte(0xF0 + (ch - '0'))
	case ch == '-':
		return 0x60
	case ch == '.':
		return 0x4B
	}
	return 0x40 // space
}

func encodeEBCDIC37(s string) []byte {
	out := make([]byte, len(s))
	for i, ch := range s {
		out[i] = ebcdic37Byte(ch)
	}
	return out
}

func TestDecodeColumnValueDate(t *testing.T) {
	raw := encodeEBCDIC37("2026-04-22")
	col := columnMeta{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 10}

	val, err := decodeColumnValue(col, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T (%v)", val, val)
	}
	want := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecodeColumnValueTime(t *testing.T) {
	raw := encodeEBCDIC37("13.45.30")
	col := columnMeta{Type: db2TypeTime, CCSID: 37, Offset: 0, Length: 8}

	val, err := decodeColumnValue(col, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T (%v)", val, val)
	}
	// time.Parse with no date gives year 0, month 1, day 1
	want := time.Date(0, 1, 1, 13, 45, 30, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecodeColumnValueTimestamp(t *testing.T) {
	src := "2026-04-22-13.45.30.123456"
	raw := encodeEBCDIC37(src)
	col := columnMeta{Type: db2TypeTimestamp, CCSID: 37, Offset: 0, Length: len(src)}

	val, err := decodeColumnValue(col, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T (%v)", val, val)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 123456*1000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDecodeColumnValueTimestampNoFraction(t *testing.T) {
	src := "2026-04-22-13.45.30"
	raw := encodeEBCDIC37(src)
	col := columnMeta{Type: db2TypeTimestamp, CCSID: 37, Offset: 0, Length: len(src)}

	val, err := decodeColumnValue(col, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := val.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T (%v)", val, val)
	}
	want := time.Date(2026, 4, 22, 13, 45, 30, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// â”€â”€â”€ columnScanType for date/time types â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func TestColumnScanTypeDateIsTime(t *testing.T) {
	got := columnScanType(columnMeta{Type: db2TypeDate})
	want := reflect.TypeOf(time.Time{})
	if got != want {
		t.Errorf("DATE scan type: got %v, want %v", got, want)
	}
}

func TestColumnScanTypeTimeIsTime(t *testing.T) {
	got := columnScanType(columnMeta{Type: db2TypeTime})
	want := reflect.TypeOf(time.Time{})
	if got != want {
		t.Errorf("TIME scan type: got %v, want %v", got, want)
	}
}

func TestColumnScanTypeTimestampIsTime(t *testing.T) {
	got := columnScanType(columnMeta{Type: db2TypeTimestamp})
	want := reflect.TypeOf(time.Time{})
	if got != want {
		t.Errorf("TIMESTAMP scan type: got %v, want %v", got, want)
	}
}

// â”€â”€â”€ CheckNamedValue int/uint/float32 normalization â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func cnvCheck(t *testing.T, in interface{}) driver.NamedValue {
	t.Helper()
	c := &Conn{}
	nv := &driver.NamedValue{Value: in}
	if err := c.CheckNamedValue(nv); err != nil {
		t.Fatalf("CheckNamedValue(%T %v) returned error: %v", in, in, err)
	}
	return *nv
}

func TestCheckNamedValueInt(t *testing.T) {
	nv := cnvCheck(t, int(42))
	if got, ok := nv.Value.(int64); !ok || got != 42 {
		t.Errorf("expected int64(42), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueInt8(t *testing.T) {
	nv := cnvCheck(t, int8(-1))
	if got, ok := nv.Value.(int64); !ok || got != -1 {
		t.Errorf("expected int64(-1), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueInt32(t *testing.T) {
	nv := cnvCheck(t, int32(-7))
	if got, ok := nv.Value.(int64); !ok || got != -7 {
		t.Errorf("expected int64(-7), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueUint8(t *testing.T) {
	nv := cnvCheck(t, uint8(255))
	if got, ok := nv.Value.(int64); !ok || got != 255 {
		t.Errorf("expected int64(255), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueUint16(t *testing.T) {
	nv := cnvCheck(t, uint16(65535))
	if got, ok := nv.Value.(int64); !ok || got != 65535 {
		t.Errorf("expected int64(65535), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueUint32(t *testing.T) {
	nv := cnvCheck(t, uint32(4294967295))
	if got, ok := nv.Value.(int64); !ok || got != 4294967295 {
		t.Errorf("expected int64(4294967295), got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueUint64Overflow(t *testing.T) {
	c := &Conn{}
	nv := &driver.NamedValue{Value: uint64(1 << 63)}
	if err := c.CheckNamedValue(nv); err == nil {
		t.Fatal("CheckNamedValue(uint64 overflow) returned nil, want error")
	}
}

func TestCheckNamedValueDefinedScalarTypes(t *testing.T) {
	type accountID int64
	type label string
	type ratio float32

	c := &Conn{}
	values := []struct {
		name string
		in   any
		want any
	}{
		{name: "defined integer", in: accountID(7), want: int64(7)},
		{name: "defined string", in: label("ready"), want: "ready"},
		{name: "defined float", in: ratio(1.5), want: float64(1.5)},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			nv := &driver.NamedValue{Value: test.in}
			if err := c.CheckNamedValue(nv); err != nil {
				t.Fatalf("CheckNamedValue() error = %v", err)
			}
			if nv.Value != test.want {
				t.Fatalf("normalized value = %#v (%T), want %#v (%T)", nv.Value, nv.Value, test.want, test.want)
			}
		})
	}
}

func TestCheckNamedValueFloat32(t *testing.T) {
	nv := cnvCheck(t, float32(3.14))
	if _, ok := nv.Value.(float64); !ok {
		t.Errorf("expected float64, got %T", nv.Value)
	}
}

type mockValuer struct{ v int64 }

func (m mockValuer) Value() (driver.Value, error) { return m.v, nil }

func TestCheckNamedValueDriverValuer(t *testing.T) {
	nv := cnvCheck(t, mockValuer{v: 99})
	if got, ok := nv.Value.(int64); !ok || got != 99 {
		t.Errorf("expected int64(99) from Valuer, got %T(%v)", nv.Value, nv.Value)
	}
}

func TestCheckNamedValueSqlOut(t *testing.T) {
	c := &Conn{}
	dest := int64(0)
	nv := &driver.NamedValue{Value: sql.Out{Dest: &dest}}
	if err := c.CheckNamedValue(nv); err != nil {
		t.Fatalf("sql.Out should be accepted, got error: %v", err)
	}
	// Value must remain a sql.Out (not mutated)
	if _, ok := nv.Value.(sql.Out); !ok {
		t.Errorf("sql.Out value was mutated: %T", nv.Value)
	}
}

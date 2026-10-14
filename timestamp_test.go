package i400

import (
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTimestampScanAcceptedInputs(t *testing.T) {
	base := time.Date(2026, 4, 22, 13, 45, 30, 0, time.UTC)
	tests := []struct {
		name  string
		src   any
		want  time.Time
		picos int64
	}{
		{"ibm no fraction", "2026-04-22-13.45.30", base, 0},
		{"ibm 6 digits", "2026-04-22-13.45.30.123456", base.Add(123456 * time.Microsecond), 123456000000},
		{"ibm 9 digits", []byte("2026-04-22-13.45.30.123456789"), base.Add(123456789), 123456789000},
		{"ibm 12 digits", "2026-04-22-13.45.30.123456789012", base.Add(123456789), 123456789012},
		{"ibm 1 digit", "2026-04-22-13.45.30.5", base.Add(500 * time.Millisecond), 500000000000},
		{"iso no fraction", "2026-04-22 13:45:30", base, 0},
		{"iso 12 digits", "2026-04-22 13:45:30.000000000001", base, 1},
		{"iso 3 digits", "2026-04-22 13:45:30.100", base.Add(100 * time.Millisecond), 100000000000},
		{"time.Time", base.Add(123456789), base.Add(123456789), 123456789000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ts Timestamp
			if err := ts.Scan(tt.src); err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if !ts.Time.Equal(tt.want) {
				t.Errorf("Time = %v, want %v", ts.Time, tt.want)
			}
			if ts.Picoseconds != tt.picos {
				t.Errorf("Picoseconds = %d, want %d", ts.Picoseconds, tt.picos)
			}
		})
	}
}

func TestTimestampScanRejectsBadInput(t *testing.T) {
	bad := []any{
		nil,
		"2026-04-22",
		"2026-02-30-13.45.30",
		"2026-04-22-25.45.30",
		"2026-04-22-13.45.30.",
		"2026-04-22-13.45.30.1234567890123",
		"2026-04-22-13.45.30.12a",
		"2026-04-22X13.45.30",
		42,
	}
	for _, src := range bad {
		var ts Timestamp
		if err := ts.Scan(src); err == nil {
			t.Errorf("Scan(%#v) returned no error", src)
		}
	}
}

func TestNullTimestampScan(t *testing.T) {
	var n NullTimestamp
	if err := n.Scan(nil); err != nil || n.Valid {
		t.Fatalf("Scan(nil) = %v, Valid = %v; want nil error and Valid false", err, n.Valid)
	}
	if v, err := n.Value(); err != nil || v != nil {
		t.Fatalf("Value() of NULL = %v, %v; want nil", v, err)
	}
	if err := n.Scan("2026-04-22-13.45.30.123456789012"); err != nil || !n.Valid {
		t.Fatalf("Scan(text) error = %v, Valid = %v", err, n.Valid)
	}
	if n.Timestamp.Picoseconds != 123456789012 {
		t.Fatalf("Picoseconds = %d, want 123456789012", n.Timestamp.Picoseconds)
	}
}

func TestTimestampValueRoundTrip(t *testing.T) {
	in := "2026-04-22-13.45.30.123456789012"
	var ts Timestamp
	if err := ts.Scan(in); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	v, err := ts.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if v != in {
		t.Fatalf("Value() = %v, want %s", v, in)
	}
	if _, ok := v.(string); !ok {
		t.Fatalf("Value() type = %T, want string", v)
	}
	var back Timestamp
	if err := back.Scan(v); err != nil {
		t.Fatalf("Scan(Value()) error = %v", err)
	}
	if back != ts {
		t.Fatalf("round trip = %+v, want %+v", back, ts)
	}
	if ts.String() != in {
		t.Fatalf("String() = %q, want %q", ts.String(), in)
	}
}

func TestTimestampValueFromTimeAndNullDefaults(t *testing.T) {
	ts := Timestamp{Time: time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC)}
	v, err := ts.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if want := "2026-01-02-03.04.05.000006000000"; v != want {
		t.Fatalf("Value() = %v, want %s", v, want)
	}
	if _, err := (Timestamp{Picoseconds: maxTimestampPicoseconds + 1}).Value(); err == nil {
		t.Fatal("Value() accepted picoseconds above 999999999999")
	}
	nv, err := NullTimestamp{Timestamp: ts, Valid: true}.Value()
	if err != nil || nv != v {
		t.Fatalf("NullTimestamp.Value() = %v, %v", nv, err)
	}
	var _ driver.Valuer = NullTimestamp{}
}

func TestTimestampParseTruncatesNanoseconds(t *testing.T) {
	got, picos, err := parseTimestampText("2026-04-22-13.45.30.123456789999")
	if err != nil {
		t.Fatalf("parseTimestampText() error = %v", err)
	}
	if got.Nanosecond() != 123456789 {
		t.Errorf("Nanosecond() = %d, want 123456789 (truncated)", got.Nanosecond())
	}
	if picos != 123456789999 {
		t.Errorf("picoseconds = %d, want 123456789999", picos)
	}
}

func TestParseDSNTimestampPrecision(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/?timestampPrecision=string")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.TimestampString {
		t.Fatal("TimestampString = false, want true")
	}

	cfg, err = ParseDSN("as400://user:pass@host/?timestampPrecision=nanoseconds")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.TimestampString {
		t.Fatal("TimestampString = true, want false")
	}

	cfg, err = ParseDSN("as400://user:pass@host/")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.TimestampString {
		t.Fatal("default TimestampString = true, want false")
	}

	if _, err := ParseDSN("as400://user:pass@host/?timestampPrecision=picos"); err == nil {
		t.Fatal("ParseDSN() accepted an unknown timestampPrecision")
	}
}

func TestDecodeTimestampStringModeKeepsTwelveDigits(t *testing.T) {
	src := "2026-04-22-13.45.30.123456789012"
	col := columnMeta{Type: db2TypeTimestamp, CCSID: 37, Offset: 0, Length: len(src)}
	dt := defaultDateTimeSettings()
	dt.TimestampString = true

	val, err := decodeColumnValueWithSettings(col, encodeEBCDIC37(src), dt)
	if err != nil {
		t.Fatalf("decodeColumnValueWithSettings() error = %v", err)
	}
	if val != src {
		t.Fatalf("value = %#v, want %q", val, src)
	}

	var ts Timestamp
	if err := ts.Scan(val); err != nil {
		t.Fatalf("Scan(decoded) error = %v", err)
	}
	if ts.Picoseconds != 123456789012 {
		t.Fatalf("Picoseconds = %d, want 123456789012", ts.Picoseconds)
	}
}

func TestDecodeTimestampDefaultModeUnchanged(t *testing.T) {
	src := "2026-04-22-13.45.30.123456789012"
	col := columnMeta{Type: db2TypeTimestamp, CCSID: 37, Offset: 0, Length: len(src)}

	val, err := decodeColumnValue(col, encodeEBCDIC37(src))
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	got, ok := val.(time.Time)
	if !ok {
		t.Fatalf("value type = %T, want time.Time", val)
	}
	if want := time.Date(2026, 4, 22, 13, 45, 30, 123456789, time.UTC); !got.Equal(want) {
		t.Fatalf("value = %v, want %v", got, want)
	}
}

func TestColumnScanTypeTimestampStringMode(t *testing.T) {
	col := columnMeta{Type: db2TypeTimestamp}
	stringMode := &queryRows{
		conn: &Conn{cfg: Config{TimestampString: true}},
		meta: &resultSetMeta{columns: []columnMeta{col}},
	}
	if got := stringMode.ColumnTypeScanType(0); got != reflect.TypeOf("") {
		t.Fatalf("string mode scan type = %v, want string", got)
	}
	nanoMode := &queryRows{
		conn: &Conn{cfg: Config{}},
		meta: &resultSetMeta{columns: []columnMeta{col}},
	}
	if got := nanoMode.ColumnTypeScanType(0); got != reflect.TypeOf(time.Time{}) {
		t.Fatalf("nanoseconds scan type = %v, want time.Time", got)
	}
}

func TestTimestampParameterKeepsTwelveDigits(t *testing.T) {
	ts := Timestamp{}
	if err := ts.Scan("2026-04-22-13.45.30.123456789012"); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	v, err := ts.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	field := parameterMarkerField{SQLType: 392, Length: 32, CCSID: 37}
	encoded, err := encodeTimestampParameter(v, field)
	if err != nil {
		t.Fatalf("encodeTimestampParameter() error = %v", err)
	}
	if len(encoded) != 32 {
		t.Fatalf("encoded length = %d, want 32", len(encoded))
	}
	if got := strings.TrimRight(mustDecodeText(t, encoded), " "); got != "2026-04-22-13.45.30.123456789012" {
		t.Fatalf("encoded text = %q, want all twelve digits", got)
	}
}

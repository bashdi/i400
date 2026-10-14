package i400

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"math"
	"reflect"
	"testing"
	"time"
)

// uxOutputFields is the parameter format of CALL MYPROC(?,...) with one input
// and every output mode and SQL type.
func uxOutputFields() []parameterMarkerField {
	return []parameterMarkerField{
		uxIntField(0xF0), // input 7
		uxIntField(0xF1), // INTEGER out -> *int64
		{SQLType: db2TypeFloatingPoint, Length: 8, CCSID: 37, ParameterType: 0xF1}, // DOUBLE -> *float64
		{SQLType: db2TypeBoolean, Length: 1, CCSID: 37, ParameterType: 0xF1},       // BOOLEAN -> *bool
		{SQLType: db2TypeVarchar, Length: 12, CCSID: 37, ParameterType: 0xF1},      // VARCHAR -> *string
		{SQLType: db2TypeVarbinary, Length: 6, CCSID: 65535, ParameterType: 0xF1},  // VARBINARY -> *[]byte
		{SQLType: db2TypeTimestamp, Length: 26, CCSID: 37, ParameterType: 0xF1},    // TIMESTAMP -> *time.Time
		{SQLType: db2TypeVarchar, Length: 12, CCSID: 37, ParameterType: 0xF1},      // VARCHAR NULL -> *sql.NullString
		{SQLType: db2TypeInteger, Length: 4, CCSID: 37, ParameterType: 0xF2},       // INOUT INTEGER 5 -> 6
	}
}

// uxOutputRow is the output row the server returns for uxOutputFields. The
// seventh output (VARCHAR) is NULL.
func uxOutputRow() (row []byte, nulls []bool) {
	row = append(row, 0, 0, 0, 42) // INTEGER 42
	f := make([]byte, 8)
	binary.BigEndian.PutUint64(f, math.Float64bits(2.5))
	row = append(row, f...)                                                     // DOUBLE 2.5
	row = append(row, booleanWireTrue)                                          // BOOLEAN true
	row = append(row, uxPad(append([]byte{0, 5}, uxEBCDIC("hello")...), 12)...) // VARCHAR 'hello'
	row = append(row, uxPad([]byte{0, 3, 1, 2, 3}, 6)...)                       // VARBINARY 01 02 03
	row = append(row, uxEBCDIC("2024-03-05-10.20.30.123456")...)                // TIMESTAMP
	row = append(row, make([]byte, 12)...)                                      // VARCHAR NULL
	row = append(row, 0, 0, 0, 6)                                               // INOUT result 6
	nulls = []bool{false, false, false, false, false, false, true, false}
	return row, nulls
}

func TestUxCallOutputParametersEveryMode(t *testing.T) {
	row, nulls := uxOutputRow()
	db := &uxDB2{fields: uxOutputFields()}
	db.execute = func(s *uxSession, req uxRequest) []byte {
		payload := append(uxSQLCA(0, 0, 0), buildTestResultDataPayload(8, len(row), [][]byte{row}, nulls)...)
		return uxReply(payload)
	}

	var outInt int64
	var outFloat float64
	var outBool bool
	var outString string
	var outBytes []byte
	var outTime time.Time
	var outNull sql.NullString
	inout := int64(5)
	args := []driver.NamedValue{
		{Ordinal: 1, Value: int64(7)},
		{Ordinal: 2, Value: sql.Out{Dest: &outInt}},
		{Ordinal: 3, Value: sql.Out{Dest: &outFloat}},
		{Ordinal: 4, Value: sql.Out{Dest: &outBool}},
		{Ordinal: 5, Value: sql.Out{Dest: &outString}},
		{Ordinal: 6, Value: sql.Out{Dest: &outBytes}},
		{Ordinal: 7, Value: sql.Out{Dest: &outTime}},
		{Ordinal: 8, Value: sql.Out{Dest: &outNull}},
		{Ordinal: 9, Value: sql.Out{Dest: &inout, In: true}},
	}
	var err error
	s := uxRunSession(t, nil, db.handle, func(c *Conn, _ *uxSession) {
		_, err = c.ExecContext(context.Background(), "CALL MYPROC(?, ?, ?, ?, ?, ?, ?, ?, ?)", args)
	})
	if err != nil {
		t.Fatalf("ExecContext(CALL) error = %v", err)
	}

	if outInt != 42 {
		t.Errorf("INTEGER out = %d, want 42", outInt)
	}
	if outFloat != 2.5 {
		t.Errorf("DOUBLE out = %v, want 2.5", outFloat)
	}
	if !outBool {
		t.Errorf("BOOLEAN out = %v, want true", outBool)
	}
	if outString != "hello" {
		t.Errorf("VARCHAR out = %q, want %q", outString, "hello")
	}
	if !bytes.Equal(outBytes, []byte{1, 2, 3}) {
		t.Errorf("VARBINARY out = % x, want 01 02 03", outBytes)
	}
	want := time.Date(2024, 3, 5, 10, 20, 30, 123456000, time.UTC)
	if !outTime.Equal(want) {
		t.Errorf("TIMESTAMP out = %v, want %v", outTime, want)
	}
	if outNull.Valid {
		t.Errorf("NULL VARCHAR out = %+v, want Valid=false", outNull)
	}
	if inout != 6 {
		t.Errorf("INOUT result = %d, want 6", inout)
	}

	// The InOut input value must reach the server as the parameter value.
	execs := uxRequestsWith(s, FunctionExecute)
	if len(execs) != 1 {
		t.Fatalf("execute requests = %d, want 1", len(execs))
	}
	params, found, _ := readTestLLCPValue(execs[0].packet[40:], CodePointParameterMarkerData)
	if !found {
		t.Fatal("execute request missing parameter marker data")
	}
	if !bytes.Contains(params, []byte{0, 0, 0, 5}) {
		t.Errorf("execute parameter block % x does not carry the InOut input 5", params)
	}
}

func TestUxCallOutputRejectsOutOnInputMarker(t *testing.T) {
	field := uxIntField(0xF0)
	var dest int64
	_, err := buildParameterBindings(&parameterMarkerFormat{Fields: []parameterMarkerField{field}},
		[]driver.NamedValue{{Ordinal: 1, Value: sql.Out{Dest: &dest}}})
	if err == nil {
		t.Fatal("buildParameterBindings() error = nil, want sql.Out mode rejection on an input marker")
	}
}

func TestUxAssignCallOutputParametersBranches(t *testing.T) {
	outInt := func(dest *int64) parameterBinding {
		return parameterBinding{field: uxIntField(0xF1), outDest: dest}
	}

	t.Run("no output destinations is a no-op", func(t *testing.T) {
		payload := buildTestResultDataPayload(1, 4, [][]byte{{0, 0, 0, 1}}, []bool{false})
		if err := assignCallOutputParameters(context.Background(), nil, payload, []parameterBinding{{field: uxIntField(0xF1)}}); err != nil {
			t.Fatalf("assignCallOutputParameters() error = %v", err)
		}
	})

	t.Run("no rows leaves destination unchanged", func(t *testing.T) {
		var dest int64 = 9
		payload := buildTestResultDataPayload(1, 4, nil, nil)
		if err := assignCallOutputParameters(context.Background(), nil, payload, []parameterBinding{outInt(&dest)}); err != nil {
			t.Fatalf("assignCallOutputParameters() error = %v", err)
		}
		if dest != 9 {
			t.Fatalf("dest = %d, want unchanged 9", dest)
		}
	})

	t.Run("column count mismatch", func(t *testing.T) {
		var dest int64
		payload := buildTestResultDataPayload(3, 4, [][]byte{{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}}, []bool{false, false, false})
		err := assignCallOutputParameters(context.Background(), nil, payload, []parameterBinding{outInt(&dest)})
		if err == nil {
			t.Fatal("assignCallOutputParameters() error = nil, want column count mismatch")
		}
	})

	t.Run("NULL output leaves zero value", func(t *testing.T) {
		dest := int64(9)
		payload := buildTestResultDataPayload(1, 4, [][]byte{{0, 0, 0, 0}}, []bool{true})
		if err := assignCallOutputParameters(context.Background(), nil, payload, []parameterBinding{outInt(&dest)}); err != nil {
			t.Fatalf("assignCallOutputParameters() error = %v", err)
		}
		if dest != 0 {
			t.Fatalf("dest = %d, want 0 for NULL output", dest)
		}
	})

	t.Run("row too short for the output", func(t *testing.T) {
		var dest int64
		payload := buildTestResultDataPayload(1, 2, [][]byte{{0, 1}}, []bool{false})
		if err := assignCallOutputParameters(context.Background(), nil, payload, []parameterBinding{outInt(&dest)}); err == nil {
			t.Fatal("assignCallOutputParameters() error = nil, want row too short")
		}
	})
}

func TestUxAssignOutDestinationEveryDestinationType(t *testing.T) {
	when := time.Date(2024, 3, 5, 10, 20, 30, 0, time.UTC)
	var (
		str      string
		i64      int64
		i32      int32
		u16      uint16
		f64      float64
		f32      float32
		b        bool
		raw      []byte
		tm       time.Time
		nullStr  sql.NullString
		nullI64  sql.NullInt64
		nullBool sql.NullBool
		nullF64  sql.NullFloat64
		nullTime sql.NullTime
		anyValue any
	)
	type myInt int64
	var named myInt

	cases := []struct {
		name  string
		dest  any
		value driver.Value
		check func() any
		want  any
	}{
		{"string from string", &str, "abc", func() any { return str }, "abc"},
		{"string from bytes", &str, []byte("xyz"), func() any { return str }, "xyz"},
		{"string from int64", &str, int64(-12), func() any { return str }, "-12"},
		{"string from float64", &str, 1.5, func() any { return str }, "1.5"},
		{"string from bool", &str, true, func() any { return str }, "true"},
		{"int64 from int64", &i64, int64(9), func() any { return i64 }, int64(9)},
		{"int64 from float64", &i64, 4.0, func() any { return i64 }, int64(4)},
		{"int64 from string", &i64, " 77 ", func() any { return i64 }, int64(77)},
		{"int64 from bytes", &i64, []byte("-5"), func() any { return i64 }, int64(-5)},
		{"int32 from int64", &i32, int64(123), func() any { return i32 }, int32(123)},
		{"uint16 from int64", &u16, int64(65), func() any { return u16 }, uint16(65)},
		{"named int kind", &named, int64(3), func() any { return named }, myInt(3)},
		{"float64 from float64", &f64, 2.25, func() any { return f64 }, 2.25},
		{"float64 from int64", &f64, int64(8), func() any { return f64 }, 8.0},
		{"float64 from string", &f64, "0.5", func() any { return f64 }, 0.5},
		{"float64 from bytes", &f64, []byte("1e3"), func() any { return f64 }, 1000.0},
		{"float32 from float64", &f32, 0.5, func() any { return f32 }, float32(0.5)},
		{"bool from bool", &b, true, func() any { return b }, true},
		{"bool from int64", &b, int64(0), func() any { return b }, false},
		{"bool from string", &b, "true", func() any { return b }, true},
		{"bool from bytes", &b, []byte("false"), func() any { return b }, false},
		{"bytes from bytes", &raw, []byte{1, 2}, func() any { return raw }, []byte{1, 2}},
		{"bytes from string", &raw, "ab", func() any { return raw }, []byte("ab")},
		{"time from time", &tm, when, func() any { return tm }, when},
		{"NullString scanner", &nullStr, "s", func() any { return nullStr }, sql.NullString{String: "s", Valid: true}},
		{"NullInt64 scanner", &nullI64, int64(4), func() any { return nullI64 }, sql.NullInt64{Int64: 4, Valid: true}},
		{"NullBool scanner", &nullBool, true, func() any { return nullBool }, sql.NullBool{Bool: true, Valid: true}},
		{"NullFloat64 scanner", &nullF64, 1.25, func() any { return nullF64 }, sql.NullFloat64{Float64: 1.25, Valid: true}},
		{"NullTime scanner", &nullTime, when, func() any { return nullTime }, sql.NullTime{Time: when, Valid: true}},
		{"interface destination", &anyValue, int64(3), func() any { return anyValue }, int64(3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := assignOutDestination(tc.dest, tc.value); err != nil {
				t.Fatalf("assignOutDestination() error = %v", err)
			}
			got := tc.check()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("destination = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestUxAssignOutDestinationNilValueZeroesDestination(t *testing.T) {
	s := "set"
	n := int64(7)
	f := 1.5
	b := true
	if err := assignOutDestination(&s, nil); err != nil || s != "" {
		t.Fatalf("string nil -> %q, %v; want empty, nil", s, err)
	}
	if err := assignOutDestination(&n, nil); err != nil || n != 0 {
		t.Fatalf("int64 nil -> %d, %v; want 0, nil", n, err)
	}
	if err := assignOutDestination(&f, nil); err != nil || f != 0 {
		t.Fatalf("float64 nil -> %v, %v; want 0, nil", f, err)
	}
	if err := assignOutDestination(&b, nil); err != nil || b {
		t.Fatalf("bool nil -> %v, %v; want false, nil", b, err)
	}
}

func TestUxAssignOutDestinationErrors(t *testing.T) {
	var nilInt *int64
	var i64 int64
	var structDest struct{ A int }
	cases := []struct {
		name  string
		dest  any
		value driver.Value
	}{
		{"nil destination is ignored", nil, int64(1)},
		{"nil pointer", nilInt, int64(1)},
		{"not a pointer", i64, int64(1)},
		{"string from time", new(string), time.Unix(0, 0)},
		{"bool from float64", new(bool), 1.0},
		{"int64 from bool", new(int64), true},
		{"float64 from bool", new(float64), true},
		{"bytes from int64", new([]byte), int64(1)},
		{"unsupported struct", &structDest, int64(1)},
		{"unsupported map", new(map[string]int), int64(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assignOutDestination(tc.dest, tc.value)
			if tc.name == "nil destination is ignored" {
				if err != nil {
					t.Fatalf("assignOutDestination(nil) error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("assignOutDestination() error = nil, want error")
			}
		})
	}
}

func TestUxOutputValueConverters(t *testing.T) {
	if s, err := outputValueString(true); err != nil || s != "true" {
		t.Errorf("outputValueString(true) = %q, %v", s, err)
	}
	if s, err := outputValueString(false); err != nil || s != "false" {
		t.Errorf("outputValueString(false) = %q, %v", s, err)
	}
	if s, err := outputValueString(2.5); err != nil || s != "2.5" {
		t.Errorf("outputValueString(2.5) = %q, %v", s, err)
	}
	if _, err := outputValueString(time.Now()); err == nil {
		t.Error("outputValueString(time) error = nil, want error")
	}

	if b, err := outputValueBool(int64(3)); err != nil || !b {
		t.Errorf("outputValueBool(3) = %v, %v; want true", b, err)
	}
	if b, err := outputValueBool(" false "); err != nil || b {
		t.Errorf("outputValueBool(\" false \") = %v, %v; want false", b, err)
	}
	if _, err := outputValueBool("maybe"); err == nil {
		t.Error("outputValueBool(\"maybe\") error = nil, want parse error")
	}
	if _, err := outputValueBool(1.0); err == nil {
		t.Error("outputValueBool(float64) error = nil, want error")
	}

	if n, err := outputValueInt64(float64(2.9)); err != nil || n != 2 {
		t.Errorf("outputValueInt64(2.9) = %d, %v; want 2", n, err)
	}
	if n, err := outputValueInt64([]byte(" 12 ")); err != nil || n != 12 {
		t.Errorf("outputValueInt64(bytes) = %d, %v; want 12", n, err)
	}
	if _, err := outputValueInt64("x"); err == nil {
		t.Error("outputValueInt64(\"x\") error = nil, want parse error")
	}
	if _, err := outputValueInt64(true); err == nil {
		t.Error("outputValueInt64(bool) error = nil, want error")
	}

	if f, err := outputValueFloat64(int64(4)); err != nil || f != 4 {
		t.Errorf("outputValueFloat64(4) = %v, %v; want 4", f, err)
	}
	if f, err := outputValueFloat64("0.25"); err != nil || f != 0.25 {
		t.Errorf("outputValueFloat64(string) = %v, %v; want 0.25", f, err)
	}
	if f, err := outputValueFloat64([]byte("1.5")); err != nil || f != 1.5 {
		t.Errorf("outputValueFloat64(bytes) = %v, %v; want 1.5", f, err)
	}
	if _, err := outputValueFloat64(true); err == nil {
		t.Error("outputValueFloat64(bool) error = nil, want error")
	}

	src := []byte{9, 8}
	out, err := outputValueBytes(src)
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("outputValueBytes() = % x, %v", out, err)
	}
	out[0] = 0
	if src[0] != 9 {
		t.Error("outputValueBytes() did not copy its input")
	}
	if out, err := outputValueBytes("hi"); err != nil || string(out) != "hi" {
		t.Errorf("outputValueBytes(string) = %q, %v", out, err)
	}
	if _, err := outputValueBytes(int64(1)); err == nil {
		t.Error("outputValueBytes(int64) error = nil, want error")
	}
}

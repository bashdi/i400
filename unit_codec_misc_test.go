package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// udFindLLCPErr returns the payload of the first element with codePoint in data.
// It does not use testing.T so it can run on a fake server goroutine.
func udFindLLCPErr(data []byte, codePoint uint16) ([]byte, bool, error) {
	remaining := data
	for len(remaining) > 0 {
		cp, payload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, false, err
		}
		if cp == codePoint {
			return payload, true, nil
		}
		remaining = rest
	}
	return nil, false, nil
}

func udFindLLCP(t *testing.T, data []byte, codePoint uint16) ([]byte, bool) {
	t.Helper()
	payload, found, err := udFindLLCPErr(data, codePoint)
	if err != nil {
		t.Fatalf("ParseLLCP() error = %v", err)
	}
	return payload, found
}

// udReplyPacket wraps payload in a 0x2800 reply with a zero return code.
func udReplyPacket(payload []byte) []byte {
	packet := buildHeader(uint32(40+len(payload)), 0, DatabaseServerID, 20, 0x2800)
	packet = append(packet, make([]byte, 20)...)
	return append(packet, payload...)
}

func TestUDDatabaseTypeNameForEveryType(t *testing.T) {
	tests := []struct {
		name string
		col  columnMeta
		want string
	}{
		{name: "date", col: columnMeta{Type: 384}, want: "DATE"},
		{name: "time", col: columnMeta{Type: 388}, want: "TIME"},
		{name: "timestamp", col: columnMeta{Type: 392}, want: "TIMESTAMP"},
		{name: "datalink", col: columnMeta{Type: 396}, want: "DATALINK"},
		{name: "blob", col: columnMeta{Type: 404}, want: "BLOB"},
		{name: "clob", col: columnMeta{Type: 408}, want: "CLOB"},
		{name: "dbclob", col: columnMeta{Type: 412}, want: "DBCLOB"},
		{name: "varchar", col: columnMeta{Type: 448}, want: "VARCHAR"},
		{name: "char", col: columnMeta{Type: 452}, want: "CHAR"},
		{name: "long varchar", col: columnMeta{Type: 456}, want: "LONGVARCHAR"},
		{name: "vargraphic", col: columnMeta{Type: 464}, want: "VARGRAPHIC"},
		{name: "graphic", col: columnMeta{Type: 468}, want: "GRAPHIC"},
		{name: "long vargraphic", col: columnMeta{Type: 472}, want: "LONGVARGRAPHIC"},
		{name: "float 8 is double", col: columnMeta{Type: 480, Length: 8}, want: "DOUBLE"},
		{name: "float 4 is real", col: columnMeta{Type: 480, Length: 4}, want: "REAL"},
		{name: "decimal", col: columnMeta{Type: 484}, want: "DECIMAL"},
		{name: "numeric", col: columnMeta{Type: 488}, want: "NUMERIC"},
		{name: "bigint", col: columnMeta{Type: 492}, want: "BIGINT"},
		{name: "integer", col: columnMeta{Type: 496}, want: "INTEGER"},
		{name: "smallint", col: columnMeta{Type: 500}, want: "SMALLINT"},
		{name: "rowid", col: columnMeta{Type: 904}, want: "ROWID"},
		{name: "varbinary", col: columnMeta{Type: 908}, want: "VARBINARY"},
		{name: "binary", col: columnMeta{Type: 912}, want: "BINARY"},
		{name: "blob locator", col: columnMeta{Type: 960}, want: "BLOB LOCATOR"},
		{name: "clob locator", col: columnMeta{Type: 964}, want: "CLOB LOCATOR"},
		{name: "dbclob locator", col: columnMeta{Type: 968}, want: "DBCLOB LOCATOR"},
		{name: "xml", col: columnMeta{Type: 988}, want: "SQLXML"},
		{name: "decfloat", col: columnMeta{Type: 996}, want: "DECFLOAT"},
		{name: "boolean", col: columnMeta{Type: 2436}, want: "BOOLEAN"},
		{name: "xml locator", col: columnMeta{Type: 2452}, want: "XML"},
		// The low bit is the nullable flag and does not change the type name.
		{name: "nullable integer", col: columnMeta{Type: 497}, want: "INTEGER"},
		{name: "nullable char", col: columnMeta{Type: 453}, want: "CHAR"},
		// CCSID 65535 means binary: CHAR is BINARY and VARCHAR/LONGVARCHAR are VARBINARY.
		{name: "char 65535", col: columnMeta{Type: 452, CCSID: 65535}, want: "BINARY"},
		{name: "varchar 65535", col: columnMeta{Type: 448, CCSID: 65535}, want: "VARBINARY"},
		{name: "long varchar 65535", col: columnMeta{Type: 456, CCSID: 65535}, want: "VARBINARY"},
		{name: "graphic 65535 unchanged", col: columnMeta{Type: 468, CCSID: 65535}, want: "GRAPHIC"},
		{name: "unknown code", col: columnMeta{Type: 1234}, want: "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.col.databaseTypeName(); got != tt.want {
				t.Fatalf("databaseTypeName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUDDecfloatParameterSize(t *testing.T) {
	tests := []struct {
		name    string
		field   parameterMarkerField
		want    int
		wantErr bool
	}{
		{name: "explicit 8", field: parameterMarkerField{Length: 8}, want: 8},
		{name: "explicit 16", field: parameterMarkerField{Length: 16}, want: 16},
		{name: "precision 34 is 16 bytes", field: parameterMarkerField{Precision: 34}, want: 16},
		{name: "precision 16 is 8 bytes", field: parameterMarkerField{Precision: 16}, want: 8},
		{name: "no length or precision", field: parameterMarkerField{}, want: 8},
		{name: "unsupported length", field: parameterMarkerField{Length: 4}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decfloatParameterSize(tt.field)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("decfloatParameterSize() = %d, nil; want error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("decfloatParameterSize() = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestUDErrorStrings(t *testing.T) {
	cause := errors.New("boom")

	if got := (&BindingError{Parameter: 3, Operation: "parameter encoding"}).Error(); got != "as400 parameter binding error at parameter 3" {
		t.Fatalf("BindingError without cause = %q", got)
	}
	if got := (&BindingError{Parameter: 3, Operation: "parameter encoding", Err: cause}).Error(); got != "as400 parameter binding error at parameter 3 during parameter encoding: boom" {
		t.Fatalf("BindingError with cause = %q", got)
	}
	if got := (*BindingError)(nil).Error(); got != "<nil>" {
		t.Fatalf("nil BindingError = %q", got)
	}
	if !errors.Is(&BindingError{Err: cause}, cause) {
		t.Fatalf("BindingError does not unwrap to its cause")
	}

	if got := (&WireError{Operation: "read packet body", Err: io.ErrUnexpectedEOF}).Error(); got != "as400 wire error during read packet body: unexpected EOF" {
		t.Fatalf("WireError = %q", got)
	}
	if got := (*WireError)(nil).Error(); got != "<nil>" {
		t.Fatalf("nil WireError = %q", got)
	}
	wire := &WireError{Operation: "read", Err: io.EOF}
	if !errors.Is(wire, io.EOF) || errors.Unwrap(wire) != io.EOF {
		t.Fatalf("WireError does not unwrap")
	}

	if got := (&TransportError{Operation: "dial", Err: cause}).Error(); got != "as400 transport error during dial: boom" {
		t.Fatalf("TransportError = %q", got)
	}
	if got := (*TransportError)(nil).Error(); got != "<nil>" {
		t.Fatalf("nil TransportError = %q", got)
	}
	transport := &TransportError{Operation: "dial", Err: io.EOF}
	if !errors.Is(transport, io.EOF) || errors.Unwrap(transport) != io.EOF {
		t.Fatalf("TransportError does not unwrap")
	}
}

func TestUDBuildOpenAndDescribeRequestLayout(t *testing.T) {
	request, err := buildOpenAndDescribeRequest("STMT0001", "CRSR0001")
	if err != nil {
		t.Fatalf("buildOpenAndDescribeRequest() error = %v", err)
	}
	if got := binary.BigEndian.Uint32(request[0:4]); got != uint32(len(request)) {
		t.Fatalf("request length field = %d, want %d", got, len(request))
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionOpenDescribe {
		t.Fatalf("ReqRepID = 0x%x, want open/describe 0x%x", header.ReqRepID, FunctionOpenDescribe)
	}

	bitmap := binary.BigEndian.Uint32(request[20:24])
	for name, bit := range map[string]uint32{
		"send reply immediately":      orsSendReplyImmediately,
		"data format":                 orsDataFormat,
		"sqlca":                       orsSQLCA,
		"result set attributes":       orsReturnResultSetAttributes,
		"extended column descriptors": orsExtendedColumnDescriptors,
	} {
		if bitmap&bit == 0 {
			t.Fatalf("ORS bitmap 0x%x missing %s bit 0x%x", bitmap, name, bit)
		}
	}
	if got := binary.BigEndian.Uint16(request[38:40]); got != 6 {
		t.Fatalf("parameter count = %d, want 6", got)
	}

	body := request[40:]
	name, found := udFindLLCP(t, body, 0x3806)
	if !found || !bytes.Equal(name[:2], []byte{0x00, 37}) || !bytes.Equal(name[4:], udEBCDIC(t, "STMT0001")) {
		t.Fatalf("statement name element = % X, found %v", name, found)
	}
	cursor, found := udFindLLCP(t, body, 0x380B)
	if !found || !bytes.Equal(cursor[4:], udEBCDIC(t, "CRSR0001")) {
		t.Fatalf("cursor name element = % X, found %v", cursor, found)
	}
	for cp, want := range map[uint16][]byte{
		0x3809: {0x80},
		0x3833: {0xE8},
		0x380A: {0xD5},
		0x380D: {0x00, 0x00},
	} {
		got, found := udFindLLCP(t, body, cp)
		if !found || !bytes.Equal(got, want) {
			t.Fatalf("element 0x%x = % X, found %v; want % X", cp, got, found, want)
		}
	}
}

// udScrollStep is one expected fetch and the reply the fake server sends for it.
type udScrollStep struct {
	option   uint16
	relative uint32
	rows     [][]byte // one 4-byte integer per row, all not null
}

// udScrollServer runs on its own goroutine, so it reports the first problem on
// errCh instead of calling testing.T methods after the test may have finished.
func udScrollServer(server net.Conn, steps []udScrollStep, errCh chan<- error, done chan<- struct{}) {
	defer close(done)
	fail := func(format string, args ...any) {
		errCh <- fmt.Errorf(format, args...)
	}
	for i, step := range steps {
		packet, err := readPacket(server)
		if err != nil {
			fail("step %d: read fetch request: %v", i, err)
			return
		}
		payload, found, err := udFindLLCPErr(packet[40:], CodePointFetchScrollOption)
		if err != nil || !found {
			fail("step %d: fetch request has no scroll option element (err %v)", i, err)
			return
		}
		if got := binary.BigEndian.Uint16(payload[:2]); got != step.option {
			fail("step %d: scroll option = %d, want %d", i, got, step.option)
			return
		}
		if len(payload) == 6 {
			if got := binary.BigEndian.Uint32(payload[2:6]); got != step.relative {
				fail("step %d: relative = %d, want %d", i, got, step.relative)
				return
			}
		}
		indicators := make([]byte, 0, 2*len(step.rows))
		data := make([]byte, 0, 4*len(step.rows))
		for _, row := range step.rows {
			indicators = append(indicators, 0x00, 0x00)
			data = append(data, row...)
		}
		block := udOriginalPayload(len(step.rows), 1, 2, 4, indicators, data)
		if _, err := server.Write(udReplyPacket(udLLCP(0x3806, block))); err != nil {
			fail("step %d: write reply: %v", i, err)
			return
		}
	}
}

func udInt32Row(v uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, v)
}

func TestUDScrollableCursorPositioning(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	client.SetDeadline(time.Now().Add(10 * time.Second))
	server.SetDeadline(time.Now().Add(10 * time.Second))

	steps := []udScrollStep{
		{option: fetchFirst, rows: [][]byte{udInt32Row(7), udInt32Row(8)}},
		{option: fetchAfterLast, rows: nil},
		{option: fetchDirect, relative: 3, rows: [][]byte{udInt32Row(9)}},
	}
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go udScrollServer(server, steps, errCh, done)
	defer func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("fake server did not finish")
		}
		select {
		case err := <-errCh:
			t.Errorf("fake server: %v", err)
		default:
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	rows := &queryRows{
		conn:            conn,
		ctx:             context.Background(),
		meta:            &resultSetMeta{columns: []columnMeta{{Name: "V", Type: db2TypeInteger, Length: 4}}, RowSize: 4},
		statementHandle: 7,
		statementName:   "STMT0001",
		cursorName:      "CRSR0001",
		fetchBufferSize: 256 * 1024,
		scrollable:      true,
		resultSetCount:  1,
	}
	dest := make([]driver.Value, 1)

	if err := rows.First(); err != nil {
		t.Fatalf("First() error = %v", err)
	}
	if got := rows.Row(); got != 0 {
		t.Fatalf("Row() after First() = %d, want 0 (before the first fetched row)", got)
	}
	if err := rows.Next(dest); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if dest[0] != int64(7) || rows.Row() != 1 {
		t.Fatalf("first row = %v at Row() %d, want 7 at 1", dest[0], rows.Row())
	}

	if err := rows.AfterLast(); err != nil {
		t.Fatalf("AfterLast() error = %v", err)
	}
	// JTOpen AS400JDBCResultSet.getRow returns 0 when the cursor is after the last row.
	if got := rows.Row(); got != 0 {
		t.Errorf("Row() after AfterLast() = %d, want 0 as in JTOpen getRow", got)
	}

	if err := rows.Absolute(3); err != nil {
		t.Fatalf("Absolute(3) error = %v", err)
	}
	if err := rows.Next(dest); err != nil {
		t.Fatalf("Next() after Absolute(3) error = %v", err)
	}
	if dest[0] != int64(9) || rows.Row() != 3 {
		t.Fatalf("absolute row = %v at Row() %d, want 9 at 3", dest[0], rows.Row())
	}
}

func TestUDScrollOnClosedRowsIsEOF(t *testing.T) {
	rows := &queryRows{scrollable: true, closed: true}
	if err := rows.First(); err != io.EOF {
		t.Fatalf("First() on closed rows = %v, want io.EOF", err)
	}
	if err := (*queryRows)(nil).Last(); err != io.EOF {
		t.Fatalf("Last() on nil rows = %v, want io.EOF", err)
	}
}

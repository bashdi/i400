package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestExpandSQLQuery(t *testing.T) {
	query := "select ? as a, '?' as b, col from t where x = ? and y = 'it''s ?' and z = ? /* ? */ -- ?\n"
	args := []driver.NamedValue{
		{Ordinal: 1, Value: "O'Reilly"},
		{Ordinal: 2, Value: []byte{0x0A, 0xFF}},
		{Ordinal: 3, Value: true},
	}

	got, err := expandSQLQuery(query, args)
	if err != nil {
		t.Fatalf("expandSQLQuery() error = %v", err)
	}

	want := "select 'O''Reilly' as a, '?' as b, col from t where x = X'0AFF' and y = 'it''s ?' and z = 1 /* ? */ -- ?\n"
	if got != want {
		t.Fatalf("expandSQLQuery() = %q, want %q", got, want)
	}
}

func TestExpandSQLQueryTimestamp(t *testing.T) {
	when := time.Date(2026, time.April, 20, 13, 14, 15, 123456000, time.FixedZone("UTC+2", 2*60*60))
	got, err := expandSQLQuery("values (?)", []driver.NamedValue{{Ordinal: 1, Value: when}})
	if err != nil {
		t.Fatalf("expandSQLQuery() error = %v", err)
	}

	want := "values (TIMESTAMP('2026-04-20-13.14.15.123456'))"
	if got != want {
		t.Fatalf("expandSQLQuery() = %q, want %q", got, want)
	}
}

func TestCountSQLPlaceholders(t *testing.T) {
	count, err := countSQLPlaceholders("select ? from t where a = '?' and b = ? /* ? */")
	if err != nil {
		t.Fatalf("countSQLPlaceholders() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("countSQLPlaceholders() = %d, want 2", count)
	}
}

func TestStmtCloseClosesPreparedStatementOnServer(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read close request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionDeleteRPB {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionDeleteRPB)
			return
		}
		// DeleteRPB names the statement by its RPB handle in the template.
		if handle := binary.BigEndian.Uint16(packet[34:36]); handle != 1 {
			t.Errorf("RPB handle = %d, want 1", handle)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}
	}()

	stmt := &Stmt{
		conn:            &Conn{netConn: client, autoCommit: true},
		statementName:   "STMT0001",
		statementHandle: 1,
	}

	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("second stmt.Close() error = %v", err)
	}
	if err := stmt.checkClosed(); err != ErrConnectionClosed {
		t.Fatalf("checkClosed() error = %v, want %v", err, ErrConnectionClosed)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestStmtCloseIgnoresInvalidConnection(t *testing.T) {
	stmt := &Stmt{
		conn:          &Conn{},
		statementName: "STMT0001",
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}
	if err := stmt.checkClosed(); err != ErrConnectionClosed {
		t.Fatalf("checkClosed() error = %v, want %v", err, ErrConnectionClosed)
	}
	_ = context.Background()
}

func TestStmtCloseWaitsForActiveOperation(t *testing.T) {
	stmt := &Stmt{conn: &Conn{}}
	if err := stmt.acquire(); err != nil {
		t.Fatalf("acquire() error = %v", err)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- stmt.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close() returned early with %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	stmt.release()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not finish after operation release")
	}
}

func TestEncodeParameterValueDecfloatUsesBinaryEncoding(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeDecfloat, CCSID: 1208, Length: 8}
	encoded, err := encodeParameterValue(field, "1", false)
	if err != nil {
		t.Fatalf("encodeParameterValue() error = %v", err)
	}
	want := []byte{0x22, 0x38, 0, 0, 0, 0, 0, 0x01}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encodeParameterValue() = % x, want % x", encoded, want)
	}

	field.Length = 16
	encoded, err = encodeParameterValue(field, "1", false)
	if err != nil {
		t.Fatalf("encodeParameterValue() error = %v", err)
	}
	want = []byte{0x22, 0x08, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}
	if !bytes.Equal(encoded, want) {
		t.Fatalf("encodeParameterValue() = % x, want % x", encoded, want)
	}
}

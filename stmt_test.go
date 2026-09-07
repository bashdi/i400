package i400

import (
	"context"
	"database/sql/driver"
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
		if header.ReqRepID != FunctionClose {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		statementPayload, found, err := readTestLLCPValue(packet[40:], CodePointPrepareStatementName)
		if err != nil {
			t.Errorf("parse close request body: %v", err)
			return
		}
		if !found {
			t.Errorf("close request missing prepare statement name")
			return
		}
		if len(statementPayload) < 4 {
			t.Errorf("statement payload length = %d, want >= 4", len(statementPayload))
			return
		}
		name, err := DecodeEBCDIC37(statementPayload[4:])
		if err != nil {
			t.Errorf("decode statement name: %v", err)
			return
		}
		if name != "STMT0001" {
			t.Errorf("statement name = %q, want %q", name, "STMT0001")
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}
	}()

	stmt := &Stmt{
		conn:          &Conn{netConn: client, autoCommit: true},
		statementName: "STMT0001",
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

func TestEncodeParameterValueDecfloatUsesTextEncoding(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeDecfloat, CCSID: 1208, Length: 0}
	encoded, err := encodeParameterValue(field, "123.45", false)
	if err != nil {
		t.Fatalf("encodeParameterValue() error = %v", err)
	}
	if string(encoded) != "123.45" {
		t.Fatalf("encodeParameterValue() = %q, want %q", string(encoded), "123.45")
	}
}

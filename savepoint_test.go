package i400

import (
	"context"
	"database/sql/driver"
	"net"
	"testing"
	"time"
)

// readExecuteImmediateSQLText reads one packet from server, asserts it is an
// FunctionExecuteImmediate request, and returns the SQL text it contains.
func readExecuteImmediateSQLText(t *testing.T, server net.Conn) string {
	t.Helper()
	packet, err := readPacket(server)
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	header, err := ParseHeader(packet)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if header.ReqRepID != FunctionExecuteImmediate {
		t.Fatalf("ReqRepID = 0x%04x, want FunctionExecuteImmediate (0x%04x)", header.ReqRepID, FunctionExecuteImmediate)
	}
	sqlPayload, found, err := readTestLLCPValue(packet[40:], 0x3831)
	if err != nil {
		t.Fatalf("readTestLLCPValue(0x3831): %v", err)
	}
	if !found {
		t.Fatal("SQL text attribute (0x3831) not found in execute-immediate request")
	}
	// stmtPayload layout: CCSID(2) + byteLength(4) + UTF-16BE text
	text, err := DecodeUTF16BE(sqlPayload[6:])
	if err != nil {
		t.Fatalf("DecodeUTF16BE: %v", err)
	}
	return text
}

// ---------------------------------------------------------------------------
// classifySQLStatement — savepoint keywords
// ---------------------------------------------------------------------------

func TestClassifySQLStatementSavepoint(t *testing.T) {
	stmts := []string{
		"SAVEPOINT sp1 ON ROLLBACK RETAIN CURSORS",
		"savepoint sp1 on rollback retain cursors",
		"  SAVEPOINT  sp1  ON ROLLBACK RETAIN CURSORS  ",
		"ROLLBACK TO SAVEPOINT sp1",
		"rollback to savepoint sp1",
		"RELEASE SAVEPOINT sp1",
		"release savepoint sp1",
	}
	for _, s := range stmts {
		got := classifySQLStatement(s)
		if got == sqlStatementTypeSelect {
			t.Errorf("classifySQLStatement(%q) = SELECT, want non-SELECT", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Full wire-level tests
// ---------------------------------------------------------------------------

// savepointTestConn returns a Conn backed by a net.Pipe() mock with autocommit
// disabled (simulating an active transaction).
func savepointTestConn(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	c := &Conn{netConn: client, autoCommit: false}
	return c, server
}

// TestExecSavepointSendsCorrectSQL verifies that executing a SAVEPOINT statement
// sends a FunctionExecuteImmediate request with the correct SQL text.
func TestExecSavepointSendsCorrectSQL(t *testing.T) {
	c, server := savepointTestConn(t)

	const sql = "SAVEPOINT sp1 ON ROLLBACK RETAIN CURSORS"
	done := make(chan struct{})
	go func() {
		defer close(done)
		got := readExecuteImmediateSQLText(t, server)
		if got != sql {
			t.Errorf("SQL text = %q, want %q", got, sql)
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}()

	_, err := c.execContext(context.Background(), sql, []driver.NamedValue{})
	if err != nil {
		t.Fatalf("execContext: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

// TestExecRollbackToSavepointSendsCorrectSQL verifies ROLLBACK TO SAVEPOINT.
func TestExecRollbackToSavepointSendsCorrectSQL(t *testing.T) {
	c, server := savepointTestConn(t)

	const sql = "ROLLBACK TO SAVEPOINT sp1"
	done := make(chan struct{})
	go func() {
		defer close(done)
		got := readExecuteImmediateSQLText(t, server)
		if got != sql {
			t.Errorf("SQL text = %q, want %q", got, sql)
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}()

	_, err := c.execContext(context.Background(), sql, []driver.NamedValue{})
	if err != nil {
		t.Fatalf("execContext: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

// TestExecReleaseSavepointSendsCorrectSQL verifies RELEASE SAVEPOINT.
func TestExecReleaseSavepointSendsCorrectSQL(t *testing.T) {
	c, server := savepointTestConn(t)

	const sql = "RELEASE SAVEPOINT sp1"
	done := make(chan struct{})
	go func() {
		defer close(done)
		got := readExecuteImmediateSQLText(t, server)
		if got != sql {
			t.Errorf("SQL text = %q, want %q", got, sql)
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}()

	_, err := c.execContext(context.Background(), sql, []driver.NamedValue{})
	if err != nil {
		t.Fatalf("execContext: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

// TestSavepointFullFlow verifies the full sequence:
//
//  1. SAVEPOINT sp1 ON ROLLBACK RETAIN CURSORS
//  2. ROLLBACK TO SAVEPOINT sp1
//  3. RELEASE SAVEPOINT sp1
//
// Each statement must arrive as a FunctionExecuteImmediate with the correct SQL
// text. The test also verifies that the connection is not invalidated during this
// sequence (the transaction remains open).
func TestSavepointFullFlow(t *testing.T) {
	c, server := savepointTestConn(t)

	stmts := []string{
		"SAVEPOINT sp1 ON ROLLBACK RETAIN CURSORS",
		"ROLLBACK TO SAVEPOINT sp1",
		"RELEASE SAVEPOINT sp1",
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, want := range stmts {
			got := readExecuteImmediateSQLText(t, server)
			if got != want {
				t.Errorf("SQL text = %q, want %q", got, want)
			}
			if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
				t.Errorf("write reply for %q: %v", want, err)
				return
			}
		}
	}()

	for _, sql := range stmts {
		if _, err := c.execContext(context.Background(), sql, []driver.NamedValue{}); err != nil {
			t.Fatalf("execContext(%q): %v", sql, err)
		}
		if !c.IsValid() {
			t.Fatalf("connection invalidated after %q", sql)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

// TestSavepointErrorPropagatesToCaller verifies that an SQL error reply (e.g.
// savepoint name not found) is propagated as a *SQLError with the correct code
// and state.
func TestSavepointErrorPropagatesToCaller(t *testing.T) {
	c, server := savepointTestConn(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = readPacket(server) // consume the ROLLBACK TO SAVEPOINT request
		// Reply with a non-zero sqlCode and state, simulating "savepoint not found".
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, -30102, "428BY", 0))); err != nil {
			t.Errorf("write error reply: %v", err)
		}
	}()

	_, err := c.execContext(context.Background(), "ROLLBACK TO SAVEPOINT nosuch", []driver.NamedValue{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	sqlErr, ok := err.(*SQLError)
	if !ok {
		t.Fatalf("error type = %T, want *SQLError", err)
	}
	if sqlErr.Code != -30102 {
		t.Errorf("SQLError.Code = %d, want -30102", sqlErr.Code)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine timed out")
	}
}

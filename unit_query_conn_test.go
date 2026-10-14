package i400

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

// TestUQPingSuccess checks that the server's test-connection answer (class 7,
// code -201) counts as alive.
func TestUQPingSuccess(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionTestConnection, uqReplyRC(7, -201, nil))
	c := f.conn(Config{})

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if !c.IsValid() {
		t.Fatal("IsValid() = false after a successful Ping")
	}
	uqCheckIDs(t, f, FunctionTestConnection)
}

// TestUQPingWrongReplyIsBadConn checks that any other answer fails the ping
// with driver.ErrBadConn. The connection itself is still open, since the
// server did answer.
func TestUQPingWrongReplyIsBadConn(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionTestConnection, uqReplyRC(7, -100, nil))
	c := f.conn(Config{})

	if err := c.Ping(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("Ping() error = %v, want driver.ErrBadConn", err)
	}
	if !c.IsValid() {
		t.Fatal("IsValid() = false although the server answered")
	}
}

// TestUQPingTransportFailureInvalidatesConn checks that once the server side
// goes away, Ping reports driver.ErrBadConn, IsValid turns false, and later
// calls fail without touching the wire.
func TestUQPingTransportFailureInvalidatesConn(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})
	if err := f.server.Close(); err != nil {
		t.Fatalf("close server side: %v", err)
	}

	if err := c.Ping(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("Ping() error = %v, want driver.ErrBadConn", err)
	}
	if c.IsValid() {
		t.Fatal("IsValid() = true after a transport failure")
	}
	if err := c.Ping(context.Background()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("second Ping() error = %v, want driver.ErrBadConn", err)
	}
}

// TestUQPingCanceledContextSendsNothing checks that a canceled context is
// reported as such before any request is written.
func TestUQPingCanceledContextSendsNothing(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping() error = %v, want context.Canceled", err)
	}
	if ids := f.ids(); len(ids) != 0 {
		t.Fatalf("requests sent = %x, want none", ids)
	}
	if !c.IsValid() {
		t.Fatal("a canceled context must not invalidate the connection")
	}
}

// TestUQIsValidFalseAfterFailedQuery checks that a query failing on a dead
// network connection marks the connection invalid.
func TestUQIsValidFalseAfterFailedQuery(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})
	if !c.IsValid() {
		t.Fatal("new connection is not valid")
	}
	if err := f.server.Close(); err != nil {
		t.Fatalf("close server side: %v", err)
	}
	if _, err := c.QueryContext(context.Background(), "SELECT 1 FROM SYSIBM.SYSDUMMY1", nil); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("QueryContext() error = %v, want driver.ErrBadConn", err)
	}
	if c.IsValid() {
		t.Fatal("IsValid() = true after a failed query on a dead pipe")
	}
}

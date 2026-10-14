package i400

import (
	"context"
	"errors"
	"testing"
)

// sqlcaReplyPayloadForTest returns a reply payload with an SQLCA whose SQLSTATE
// is set (EBCDIC bytes); the SQLCODE in the SQLCA is left at zero, as the reply return code
// carries the SQLCODE.
func sqlcaReplyPayloadForTest(t *testing.T, stateEBCDIC []byte) []byte {
	t.Helper()
	sqlca := make([]byte, 136)
	copy(sqlca[131:136], stateEBCDIC)
	return llcpForTest(CodePointSQLCA, sqlca)
}

// TestCheckReplySQLClassIsSQLError covers JTOpen's error path: an error class
// with a negative return code and an SQLCA is an SQL error whose SQLCODE is the
// return code.
func TestCheckReplySQLClassIsSQLError(t *testing.T) {
	var c *Conn
	envelope := replyEnvelope{RCClass: 1, RCCode: -204}
	err := c.checkReply(context.Background(), 0, "prepare", envelope, sqlcaReplyPayloadForTest(t, []byte{0xF4, 0xF2, 0xF7, 0xF0, 0xF4}))

	var sqlErr *SQLError
	if !errors.As(err, &sqlErr) {
		t.Fatalf("checkReply() error = %T (%v), want *SQLError", err, err)
	}
	if sqlErr.Code != -204 {
		t.Fatalf("SQLError.Code = %d, want -204", sqlErr.Code)
	}
	if sqlErr.State != "42704" {
		t.Fatalf("SQLError.State = %q, want 42704", sqlErr.State)
	}
}

// TestCheckReplyWithoutSQLCAIsProtocolError keeps host server errors that carry
// no SQL diagnostics as *ProtocolError.
func TestCheckReplyWithoutSQLCAIsProtocolError(t *testing.T) {
	var c *Conn
	err := c.checkReply(context.Background(), 0, "set client info", replyEnvelope{RCClass: 7, RCCode: -901}, nil)

	var protoErr *ProtocolError
	if !errors.As(err, &protoErr) {
		t.Fatalf("checkReply() error = %T (%v), want *ProtocolError", err, err)
	}
	if protoErr.Class != 7 || protoErr.Code != -901 {
		t.Fatalf("ProtocolError = class %d code %d, want class 7 code -901", protoErr.Class, protoErr.Code)
	}
}

// TestCheckReplyPositiveCodeIsNotAnError checks that a positive return code
// (a JTOpen warning) does not fail the call.
func TestCheckReplyPositiveCodeIsNotAnError(t *testing.T) {
	c := &Conn{}
	if err := c.checkReply(context.Background(), 0, "commit", replyEnvelope{RCClass: 2, RCCode: 1}, nil); err != nil {
		t.Fatalf("checkReply() error = %v, want nil for a warning", err)
	}
	if c.lastWarning == nil || c.lastWarning.Code != 1 {
		t.Fatalf("lastWarning = %+v, want code 1 recorded", c.lastWarning)
	}
}

// TestCheckReplyCursorClosedIsNotWarning keeps class 2 / code 700, the cursor
// closed signal that fetch reads, out of the warnings.
func TestCheckReplyCursorClosedIsNotWarning(t *testing.T) {
	c := &Conn{}
	if err := c.checkReply(context.Background(), 0, "fetch", replyEnvelope{RCClass: 2, RCCode: 700}, nil); err != nil {
		t.Fatalf("checkReply() error = %v, want nil", err)
	}
	if c.lastWarning != nil {
		t.Fatalf("lastWarning = %+v, want none", c.lastWarning)
	}
}

// TestCheckReplyFetchEndOfDataIsNotWarning mirrors JTOpen's JDServerRowCache:
// on a fetch, class 1 / 100 and class 2 / 701 mean end of data, not a warning.
// The same codes from another operation are still warnings.
func TestCheckReplyFetchEndOfDataIsNotWarning(t *testing.T) {
	for _, env := range []replyEnvelope{{RCClass: 1, RCCode: 100}, {RCClass: 2, RCCode: 701}} {
		c := &Conn{}
		if err := c.checkReply(context.Background(), 0, "fetch", env, nil); err != nil {
			t.Fatalf("fetch %d/%d: error = %v, want nil", env.RCClass, env.RCCode, err)
		}
		if c.lastWarning != nil {
			t.Fatalf("fetch %d/%d: lastWarning = %+v, want none", env.RCClass, env.RCCode, c.lastWarning)
		}
		c = &Conn{}
		if err := c.checkReply(context.Background(), 0, "execute", env, nil); err != nil {
			t.Fatalf("execute %d/%d: error = %v, want nil", env.RCClass, env.RCCode, err)
		}
		if c.lastWarning == nil {
			t.Fatalf("execute %d/%d: want a recorded warning", env.RCClass, env.RCCode)
		}
	}
}

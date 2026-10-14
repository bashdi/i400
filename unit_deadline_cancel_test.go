package i400

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"testing"
	"time"
)

// udcShortGrace shortens nativeCancelGracePeriod for one test.
func udcShortGrace(t *testing.T, d time.Duration) {
	t.Helper()
	saved := nativeCancelGracePeriod
	nativeCancelGracePeriod = d
	t.Cleanup(func() { nativeCancelGracePeriod = saved })
}

// udcAssertDeadlineError checks that err is the context error and never
// driver.ErrBadConn, which database/sql would retry on another connection.
func udcAssertDeadlineError(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("error = %v, must not be driver.ErrBadConn for a deadline", err)
	}
}

// udcStatementCall runs doStatementRequest and waits for its result, failing
// the test if it does not return.
func udcStatementCall(t *testing.T, c *Conn, ctx context.Context) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0102)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("doStatementRequest did not return")
		return nil
	}
}

// TestUdcDeadlineDuringOpenKeepsConnection: the deadline expires while the
// server runs the statement. The native cancel is sent, the server answers the
// interrupted request with SQL -952, and the connection stays usable.
func TestUdcDeadlineDuringOpenKeepsConnection(t *testing.T) {
	host := uxStartCancelHost(t, false)
	client, server := net.Pipe()
	defer server.Close()
	received := make(chan struct{})
	go uxMainServerHoldsExecute(t, server, host, received, true)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config(), systemInfo: uxNativeInfo()}

	start := time.Now()
	err := udcStatementCall(t, c, ctx)
	udcAssertDeadlineError(t, err)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("call took %v after a 200ms deadline", elapsed)
	}
	if !c.IsValid() {
		t.Fatal("connection invalidated after the native cancel reply, want it kept usable")
	}
	if n := len(host.cancelPackets()); n != 1 {
		t.Fatalf("cancel requests = %d, want 1", n)
	}

	// The next request on the same connection gets its reply normally.
	go func() {
		if _, err := readPacket(server); err != nil {
			return
		}
		_, _ = server.Write(uxReply(nil))
	}()
	if _, _, err := c.doRequest(context.Background(), buildTestConnectionRequest()); err != nil {
		t.Fatalf("request after the cancelled statement: %v", err)
	}
	_ = client.Close()
}

// TestUdcDeadlineGraceExpiryInvalidates: the native cancel is accepted but the
// server never answers the interrupted request. After the grace period the
// connection is dropped and ctx.Err() is returned.
func TestUdcDeadlineGraceExpiryInvalidates(t *testing.T) {
	udcShortGrace(t, 150*time.Millisecond)
	host := uxStartCancelHost(t, false)
	client, server := net.Pipe()
	defer server.Close()
	received := make(chan struct{})
	go uxMainServerHoldsExecute(t, server, host, received, false)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config(), systemInfo: uxNativeInfo()}

	err := udcStatementCall(t, c, ctx)
	udcAssertDeadlineError(t, err)
	if c.IsValid() {
		t.Fatal("connection still valid after the grace period expired, want it invalidated")
	}
	if n := len(host.cancelPackets()); n != 1 {
		t.Fatalf("cancel requests = %d, want 1", n)
	}
}

// TestUdcDeadlineCancelFailureInvalidates: the native cancel fails, either
// rejected by the server or because the cancel connection cannot be made. The
// connection is invalidated and ctx.Err() is returned without waiting for the
// grace period.
func TestUdcDeadlineCancelFailureInvalidates(t *testing.T) {
	cases := []struct {
		name     string
		rejected bool // the server rejects the cancel request
		cfg      func(host *uxCancelHost) Config
	}{
		{name: "cancel rejected", rejected: true, cfg: func(h *uxCancelHost) Config { return h.config() }},
		{name: "cancel connection refused", cfg: func(*uxCancelHost) Config { return Config{Host: "127.0.0.1", Port: -1} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := uxStartCancelHost(t, tc.rejected)
			client, server := net.Pipe()
			defer server.Close()
			received := make(chan struct{})
			go uxMainServerHoldsExecute(t, server, host, received, false)

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			c := &Conn{netConn: client, autoCommit: true, cfg: tc.cfg(host), systemInfo: uxNativeInfo()}
			start := time.Now()
			err := udcStatementCall(t, c, ctx)
			udcAssertDeadlineError(t, err)
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Fatalf("call took %v; a failed cancel should not wait for the grace period", elapsed)
			}
			if c.IsValid() {
				t.Fatal("connection still valid after a failed cancel, want it invalidated")
			}
		})
	}
}

// TestUdcDeadlineWithoutCancelMetadataInvalidates: without server metadata
// there is no native cancel. The deadline is the socket deadline, the
// connection is dropped and ctx.Err() is returned. No cancel is sent.
func TestUdcDeadlineWithoutCancelMetadataInvalidates(t *testing.T) {
	host := uxStartCancelHost(t, false)
	client, server := net.Pipe()
	defer server.Close()
	go func() { _, _ = readPacket(server) }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config()}
	err := udcStatementCall(t, c, ctx)
	udcAssertDeadlineError(t, err)
	if c.IsValid() {
		t.Fatal("connection still valid without native cancel, want it invalidated")
	}
	if n := len(host.cancelPackets()); n != 0 {
		t.Fatalf("cancel requests = %d, want none without metadata", n)
	}
}

// TestUdcNonStatementDeadlineKeepsDeadlineBehaviour: requests that are not
// statement requests (doRequest) keep the deadline behaviour even when native
// cancel metadata is present: no cancel is sent, the connection is dropped and
// ctx.Err() is returned.
func TestUdcNonStatementDeadlineKeepsDeadlineBehaviour(t *testing.T) {
	host := uxStartCancelHost(t, false)
	client, server := net.Pipe()
	defer server.Close()
	go func() { _, _ = readPacket(server) }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config(), systemInfo: uxNativeInfo()}
	_, _, err := c.doRequest(ctx, buildTestConnectionRequest())
	udcAssertDeadlineError(t, err)
	if c.IsValid() {
		t.Fatal("connection still valid after a non-statement deadline, want it invalidated")
	}
	if n := len(host.cancelPackets()); n != 0 {
		t.Fatalf("cancel requests = %d, want none for non-statement requests", n)
	}
}

// TestUdcCancelStatementIsBoundedWithoutDeadline: cancelStatement with a
// context that has no deadline still gives up after nativeCancelTimeout when
// the server accepts the connection but never answers.
func TestUdcCancelStatementIsBoundedWithoutDeadline(t *testing.T) {
	saved := nativeCancelTimeout
	nativeCancelTimeout = 200 * time.Millisecond
	t.Cleanup(func() { nativeCancelTimeout = saved })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 512)
				for { // read and discard, never answer
					if _, err := conn.Read(buf); err != nil {
						return
					}
				}
			}()
		}
	}()
	c := &Conn{
		cfg:        Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, User: "QSECOFR", Password: "PW"},
		systemInfo: uxNativeInfo(),
	}
	start := time.Now()
	if err := c.cancelStatement(context.Background(), 0x0102); err == nil {
		t.Fatal("cancelStatement() error = nil against a silent server, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancelStatement() took %v, want it bounded by nativeCancelTimeout", elapsed)
	}
}

// TestUdcDeadlineDuringOpenThenQueryWorks runs the statement path end to end:
// the open is held until the native cancel arrives, answered with SQL -952,
// and the next query on the same Conn opens and returns rows.
func TestUdcDeadlineDuringOpenThenQueryWorks(t *testing.T) {
	host := uxStartCancelHost(t, false)
	db := &uxDB2{}
	opens := 0
	handle := func(s *uxSession, req uxRequest) []byte {
		if req.id == FunctionOpenDescribe {
			opens++
			if opens == 1 {
				select {
				case <-host.cancelled:
				case <-time.After(5 * time.Second):
				}
				return buildTestReplyPacketWithRC(uxSQLCA(-952, 0, 0), 2, -952)
			}
		}
		return db.handle(s, req)
	}

	var firstErr, followErr error
	var validAfter bool
	var followCols int
	uxRunSession(t, func(c *Conn) {
		c.cfg = host.config()
		c.systemInfo = uxNativeInfo()
	}, handle, func(c *Conn, _ *uxSession) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, firstErr = c.QueryContext(ctx, "SELECT * FROM T", nil)
		validAfter = c.IsValid()
		if !validAfter {
			return
		}
		rows, err := c.QueryContext(context.Background(), "SELECT * FROM T", nil)
		followErr = err
		if err == nil {
			followCols = len(rows.Columns())
			_ = rows.Close()
		}
	})
	udcAssertDeadlineError(t, firstErr)
	if !validAfter {
		t.Fatal("connection invalidated after the cancelled open, want it kept usable")
	}
	if followErr != nil || followCols == 0 {
		t.Fatalf("query after the cancelled open: columns=%d err=%v", followCols, followErr)
	}
}

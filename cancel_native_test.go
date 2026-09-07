package i400

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// canCancelNatively
// ---------------------------------------------------------------------------

func TestCanCancelNativelyReturnsFalseForNilReceiver(t *testing.T) {
	var c *Conn
	if c.canCancelNatively() {
		t.Error("canCancelNatively() = true for nil receiver, want false")
	}
}

func TestCanCancelNativelyReturnsFalseWithoutSystemInfo(t *testing.T) {
	c := &Conn{}
	if c.canCancelNatively() {
		t.Error("canCancelNatively() = true with nil systemInfo, want false")
	}
}

func TestCanCancelNativelyReturnsFalseForOldFunctionalLevel(t *testing.T) {
	c := &Conn{systemInfo: &SystemInfo{
		ServerFunctionalLevel: 4, // must be >= 5
		ServerJobIdentifier:   "QSQSRVR   123456USER      ",
	}}
	if c.canCancelNatively() {
		t.Error("canCancelNatively() = true for level 4, want false")
	}
}

func TestCanCancelNativelyReturnsFalseForEmptyJobIdentifier(t *testing.T) {
	c := &Conn{systemInfo: &SystemInfo{
		ServerFunctionalLevel: 5,
		ServerJobIdentifier:   "   ", // blank-only is treated as empty
	}}
	if c.canCancelNatively() {
		t.Error("canCancelNatively() = true with blank job identifier, want false")
	}
}

func TestCanCancelNativelyReturnsTrueWithMetadata(t *testing.T) {
	c := &Conn{systemInfo: &SystemInfo{
		ServerFunctionalLevel: 5,
		ServerJobIdentifier:   "QSQSRVR   123456USER      ",
	}}
	if !c.canCancelNatively() {
		t.Error("canCancelNatively() = false with valid metadata, want true")
	}
}

// ---------------------------------------------------------------------------
// doStatementRequest – basic happy path (no cancel)
// ---------------------------------------------------------------------------

// TestDoStatementRequestHappyPath verifies that when no cancel occurs,
// doStatementRequest behaves identically to doRequest.
func TestDoStatementRequestHappyPath(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := readPacket(server)
		if err != nil {
			t.Errorf("server read: %v", err)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("server write: %v", err)
		}
	}()

	// Without systemInfo, doStatementRequest delegates to doRequest.
	c := &Conn{netConn: client, autoCommit: true}
	env, _, err := c.doStatementRequest(context.Background(), buildTestConnectionRequest(), 0x0001)
	if err != nil {
		t.Fatalf("doStatementRequest() error = %v", err)
	}
	_ = env

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

// ---------------------------------------------------------------------------
// doStatementRequest – context already cancelled
// ---------------------------------------------------------------------------

// TestDoStatementRequestContextAlreadyCancelled verifies that when the context
// is cancelled before the call, doStatementRequest returns context.Canceled
// immediately without writing a packet to the server.
func TestDoStatementRequestContextAlreadyCancelled(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done

	// Connection with valid systemInfo (canCancelNatively = true).
	c := &Conn{
		netConn: client, autoCommit: true,
		systemInfo: &SystemInfo{
			ServerFunctionalLevel: 5,
			ServerJobIdentifier:   "QSQSRVR   123456USER      ",
		},
	}
	_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0001)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("doStatementRequest() error = %v, want context.Canceled", err)
	}
	// No packet should arrive at the server because we returned before writing.
	serverGotPacket := make(chan bool, 1)
	go func() {
		_ = server.SetDeadline(time.Now().Add(50 * time.Millisecond))
		_, err := readPacket(server)
		serverGotPacket <- (err == nil)
	}()
	if got := <-serverGotPacket; got {
		t.Error("server received a packet, but expected none for pre-cancelled context")
	}
}

// ---------------------------------------------------------------------------
// doStatementRequest – native cancel metadata present, cancelStatement fails
// (fallback to deadline-kill, connection is invalidated)
// ---------------------------------------------------------------------------

// TestDoStatementRequestNativeCancelFallbackInvalidatesConnection verifies that
// when the context is cancelled during a statement request, canCancelNatively()
// is true but cancelStatement fails fast (bad port), the function falls back to
// deadline-based interruption and the connection is marked invalid.
func TestDoStatementRequestNativeCancelFallbackInvalidatesConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	// The "request received" channel lets us cancel the context only AFTER the
	// server has read the outgoing request.  This ensures the AfterFunc cancel
	// path is exercised, not the early ctx.Err() check.
	requestReceived := make(chan struct{})

	go func() {
		_, _ = readPacket(server)
		close(requestReceived) // signal that the request arrived
		// Server deliberately never responds → forces the client to wait.
	}()

	ctx, cancel := context.WithCancel(context.Background())

	// cfg.Port = -1 makes connectDatabase() fail immediately ("invalid database
	// port"), so cancelStatement() returns an error at once and the AfterFunc
	// falls back to conn.SetDeadline(time.Now()).
	c := &Conn{
		netConn: client, autoCommit: true,
		systemInfo: &SystemInfo{
			ServerFunctionalLevel: 5,
			ServerJobIdentifier:   "QSQSRVR   123456USER      ",
		},
		cfg: Config{Host: "127.0.0.1", Port: -1},
	}

	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0001)
		errCh <- err
	}()

	// Cancel only after the request has been received by the server.
	select {
	case <-requestReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the request in time")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("doStatementRequest() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("doStatementRequest did not return after cancel")
	}

	// The fallback deadline-kill path invalidates the connection.
	if c.IsValid() {
		t.Error("connection should be invalidated after deadline-based cancel fallback")
	}
}

// ---------------------------------------------------------------------------
// doStatementRequest – no native cancel metadata → falls back to doRequest
// behaviour (connection invalidated)
// ---------------------------------------------------------------------------

// TestDoStatementRequestWithoutMetadataInvalidatesOnCancel verifies that when
// systemInfo is nil (canCancelNatively = false), doStatementRequest delegates to
// doRequest, which uses deadline-based interruption and invalidates the connection.
func TestDoStatementRequestWithoutMetadataInvalidatesOnCancel(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	requestReceived := make(chan struct{})
	go func() {
		_, _ = readPacket(server)
		close(requestReceived)
		// Never respond.
	}()

	ctx, cancel := context.WithCancel(context.Background())

	c := &Conn{netConn: client, autoCommit: true} // systemInfo nil → no native cancel

	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0001)
		errCh <- err
	}()

	select {
	case <-requestReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the request in time")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("doStatementRequest() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("doStatementRequest did not return after cancel")
	}

	if c.IsValid() {
		t.Error("connection should be invalidated (no native cancel metadata)")
	}
}

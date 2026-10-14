package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

const uxJobID = "QSQSRVR   123456USER      "

// uxNativeInfo is server metadata that enables protocol-native cancel.
func uxNativeInfo() *SystemInfo {
	return &SystemInfo{ServerFunctionalLevel: 5, ServerJobIdentifier: uxJobID}
}

// uxSimpleReply builds a reply whose return code is zero and whose payload
// follows the return code, as readSimpleReply expects.
func uxSimpleReply(id uint16, payload []byte) []byte {
	packet := buildHeader(uint32(24+len(payload)), 0, DatabaseServerID, 20, id)
	packet = appendI32(packet, 0)
	return append(packet, payload...)
}

// uxCancelHost is a fake host server. It accepts the signon exchange that
// connectDatabase performs and then answers the cancel requests that
// cancelStatement sends on a second connection.
type uxCancelHost struct {
	port       int
	cancelFail bool          // reply to cancel requests with an error
	cancelled  chan struct{} // closed when the first cancel request arrives
	once       sync.Once

	mu      sync.Mutex
	cancels [][]byte
}

func uxStartCancelHost(t *testing.T, cancelFail bool) *uxCancelHost {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	host := &uxCancelHost{
		port:       ln.Addr().(*net.TCPAddr).Port,
		cancelFail: cancelFail,
		cancelled:  make(chan struct{}),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go host.serve(conn)
		}
	}()
	return host
}

func (h *uxCancelHost) serve(conn net.Conn) {
	defer conn.Close()
	if _, err := readPacket(conn); err != nil { // exchange seeds
		return
	}
	if _, err := conn.Write(uxSimpleReply(ReplyExchangeRandomSeeds, make([]byte, 8))); err != nil {
		return
	}
	if _, err := readPacket(conn); err != nil { // start server
		return
	}
	if _, err := conn.Write(uxSimpleReply(ReplyStartServer, nil)); err != nil {
		return
	}
	for {
		packet, err := readPacket(conn)
		if err != nil {
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			return
		}
		reply := uxReply(nil) // set server attributes, add library list
		if header.ReqRepID == FunctionCancel {
			h.mu.Lock()
			h.cancels = append(h.cancels, packet)
			h.mu.Unlock()
			h.once.Do(func() { close(h.cancelled) })
			if h.cancelFail {
				reply = buildTestReplyPacketWithRC(nil, 3, -1)
			}
		}
		if _, err := conn.Write(reply); err != nil {
			return
		}
	}
}

func (h *uxCancelHost) cancelPackets() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]byte(nil), h.cancels...)
}

func (h *uxCancelHost) config() Config {
	return Config{Host: "127.0.0.1", Port: h.port, User: "QSECOFR", Password: "PW"}
}

// waitCancelled waits for the host to receive a cancel request.
func (h *uxCancelHost) waitCancelled(t *testing.T) {
	t.Helper()
	select {
	case <-h.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("host did not receive a cancel request")
	}
}

func TestUxCancelStatementSendsNativeCancelRequest(t *testing.T) {
	host := uxStartCancelHost(t, false)
	c := &Conn{cfg: host.config(), systemInfo: uxNativeInfo()}
	const handle = 0x0102
	if err := c.cancelStatement(context.Background(), handle); err != nil {
		t.Fatalf("cancelStatement() error = %v", err)
	}
	packets := host.cancelPackets()
	if len(packets) != 1 {
		t.Fatalf("cancel requests = %d, want 1", len(packets))
	}
	// As in JTOpen, the cancel targets the server job, not the statement: the
	// RPB id in the request is always 0.
	want, err := buildCancelRequest(0, uxJobID)
	if err != nil {
		t.Fatalf("buildCancelRequest() error = %v", err)
	}
	if !bytes.Equal(packets[0], want) {
		t.Fatalf("cancel request bytes differ from buildCancelRequest:\n got % x\nwant % x", packets[0], want)
	}
	header, err := ParseHeader(packets[0])
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionCancel {
		t.Fatalf("request id = 0x%x, want FunctionCancel 0x%x", header.ReqRepID, FunctionCancel)
	}
	if got := binary.BigEndian.Uint16(packets[0][34:36]); got != 0 {
		t.Fatalf("RPB id in request = 0x%x, want 0 (JTOpen cancels by job, not by statement 0x%x)", got, handle)
	}
	job, found, err := readTestLLCPValue(packets[0][40:], CodePointJobIdentifier)
	if err != nil || !found {
		t.Fatalf("job identifier attribute found=%v err=%v", found, err)
	}
	// The attribute is a 4-byte header (length and CCSID-style field) followed by the EBCDIC text.
	if !bytes.Equal(job[4:], uxEBCDIC(uxJobID)) {
		t.Fatalf("job identifier = % x, want EBCDIC of %q after a 4-byte header", job, uxJobID)
	}
}

func TestUxCancelStatementReportsRejectedCancel(t *testing.T) {
	host := uxStartCancelHost(t, true)
	c := &Conn{cfg: host.config(), systemInfo: uxNativeInfo()}
	err := c.cancelStatement(context.Background(), 0x0102)
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("cancel request failed")) {
		t.Fatalf("cancelStatement() error = %v, want cancel request failed", err)
	}
}

// TestUxCancelStatementFallbackWithoutMetadata checks that without the server
// metadata the native cancel is refused before any connection is attempted.
// The configured port is invalid, so any attempt to connect would fail with a
// different error.
func TestUxCancelStatementFallbackWithoutMetadata(t *testing.T) {
	cases := []struct {
		name string
		info *SystemInfo
	}{
		{name: "no system info", info: nil},
		{name: "functional level 4", info: &SystemInfo{ServerFunctionalLevel: 4, ServerJobIdentifier: uxJobID}},
		{name: "blank job identifier", info: &SystemInfo{ServerFunctionalLevel: 5, ServerJobIdentifier: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Conn{cfg: Config{Host: "127.0.0.1", Port: -1}, systemInfo: tc.info}
			err := c.cancelStatement(context.Background(), 0x0102)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("cancelStatement() error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestUxCancelStatementHonoursCancelledContext(t *testing.T) {
	host := uxStartCancelHost(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Conn{cfg: host.config(), systemInfo: uxNativeInfo()}
	if err := c.cancelStatement(ctx, 0x0102); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelStatement() error = %v, want context.Canceled", err)
	}
	if n := len(host.cancelPackets()); n != 0 {
		t.Fatalf("cancel requests sent = %d, want none", n)
	}
}

// uxMainServerHoldsExecute reads one request on the main connection, signals
// that it arrived, waits for the host to receive the native cancel, and then
// answers with the error reply IBM i sends for a cancelled statement. If
// answer is false it never replies.
func uxMainServerHoldsExecute(t *testing.T, server net.Conn, host *uxCancelHost, received chan<- struct{}, answer bool) {
	if _, err := readPacket(server); err != nil {
		t.Errorf("read statement request: %v", err)
		return
	}
	close(received)
	select {
	case <-host.cancelled:
	case <-time.After(5 * time.Second):
		return
	}
	if answer {
		_, _ = server.Write(buildTestReplyPacketWithRC(uxSQLCA(-952, 0, 0), 2, -952))
	}
}

func TestUxDoStatementRequestNativeCancelKeepsConnection(t *testing.T) {
	host := uxStartCancelHost(t, false)
	client, server := net.Pipe()
	defer server.Close()
	received := make(chan struct{})
	go uxMainServerHoldsExecute(t, server, host, received, true)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config(), systemInfo: uxNativeInfo()}
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0102)
		errCh <- err
	}()
	<-received
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("doStatementRequest() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("doStatementRequest did not return after native cancel")
	}
	if !c.IsValid() {
		t.Fatal("connection invalidated after native cancel, want it kept usable")
	}
	packets := host.cancelPackets()
	if len(packets) != 1 || binary.BigEndian.Uint16(packets[0][34:36]) != 0 {
		t.Fatalf("cancel requests = %d, want one with RPB id 0 (JTOpen cancels by job)", len(packets))
	}
	_ = client.Close()
}

func TestUxDoStatementRequestRejectedNativeCancelFallsBackToDeadline(t *testing.T) {
	host := uxStartCancelHost(t, true)
	client, server := net.Pipe()
	defer server.Close()
	received := make(chan struct{})
	go uxMainServerHoldsExecute(t, server, host, received, false)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{netConn: client, autoCommit: true, cfg: host.config(), systemInfo: uxNativeInfo()}
	errCh := make(chan error, 1)
	go func() {
		_, _, err := c.doStatementRequest(ctx, buildTestConnectionRequest(), 0x0102)
		errCh <- err
	}()
	<-received
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("doStatementRequest() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("doStatementRequest did not return after failed native cancel")
	}
	if c.IsValid() {
		t.Fatal("connection still valid after the deadline fallback, want it invalidated")
	}
}

// TestUxDeadlineDuringFetchSendsCancel runs a query whose fetch the server
// holds until the native cancel arrives. The context deadline must surface as
// context.DeadlineExceeded from the rows, never as driver.ErrBadConn.
func TestUxDeadlineDuringFetchSendsCancel(t *testing.T) {
	host := uxStartCancelHost(t, false)
	fetchReceived := make(chan struct{})
	db := &uxDB2{}
	db.fetch = func(s *uxSession, req uxRequest) []byte {
		close(fetchReceived)
		select {
		case <-host.cancelled:
		case <-time.After(2 * time.Second):
		}
		return buildTestReplyPacketWithRC(nil, 2, -952)
	}

	var queryErr, afterErr error
	var validAfter bool
	var updated int64
	uxRunSession(t, func(c *Conn) {
		c.cfg = host.config()
		c.systemInfo = uxNativeInfo()
	}, db.handle, func(c *Conn, _ *uxSession) {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		rows, err := c.QueryContext(ctx, "SELECT * FROM T", nil)
		if err != nil {
			queryErr = err
			return
		}
		dest := make([]driver.Value, len(rows.Columns()))
		queryErr = rows.Next(dest)
		_ = rows.Close()
		validAfter = c.IsValid()
		if !validAfter {
			return
		}
		db.updateCount = 1
		res, err := c.ExecContext(context.Background(), "UPDATE T SET A = 1", nil)
		afterErr = err
		if err == nil {
			updated, _ = res.RowsAffected()
		}
	})
	select {
	case <-fetchReceived:
	default:
		t.Fatal("fetch request was never sent")
	}
	if !errors.Is(queryErr, context.DeadlineExceeded) {
		t.Fatalf("rows.Next() error = %v, want context.DeadlineExceeded", queryErr)
	}
	// The deadline no longer sets the socket read deadline, so the native
	// cancel is sent and the -952 reply is consumed: the connection stays valid.
	select {
	case <-host.cancelled:
	case <-time.After(time.Second):
		t.Fatal("native cancel was not sent for the expired deadline")
	}
	if !validAfter {
		t.Fatal("connection invalidated after the native cancel reply, want it kept usable")
	}
	if afterErr != nil || updated != 1 {
		t.Fatalf("statement after the cancelled query: rows=%d err=%v", updated, afterErr)
	}
}

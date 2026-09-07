package i400

import (
	"context"
	"database/sql/driver"
	"net"
	"testing"
	"time"
)

func TestConnCloseIsIdempotentAndInvalidatesConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read end-job request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse end-job request: %v", err)
			return
		}
		if header.ReqRepID != FunctionEndJob {
			t.Errorf("end-job request id = 0x%x, want 0x%x", header.ReqRepID, FunctionEndJob)
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	if err := conn.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if conn.IsValid() {
		t.Fatal("connection is valid after Close()")
	}
	if err := conn.Ping(context.Background()); err != driver.ErrBadConn {
		t.Fatalf("Ping() after Close() error = %v, want driver.ErrBadConn", err)
	}

	<-done
}

func TestConnCloseDoesNotDeadlockWithActiveRequest(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	requestReceived := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		if _, err := readPacket(server); err != nil {
			t.Errorf("read active request: %v", err)
			return
		}
		close(requestReceived)
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write active reply: %v", err)
			return
		}
		if _, err := readPacket(server); err != nil {
			t.Errorf("read end-job request: %v", err)
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	requestDone := make(chan error, 1)
	go func() {
		_, _, err := conn.doRequest(context.Background(), buildTestConnectionRequest())
		requestDone <- err
	}()
	select {
	case <-requestReceived:
	case <-time.After(time.Second):
		t.Fatal("active request was not received")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() deadlocked while request was active")
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("active request error = %v", err)
	}
	<-serverDone
}

func TestDoRequestCanceledContextInvalidatesConnection(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()

	c := &Conn{netConn: client, autoCommit: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.doRequest(ctx, buildTestConnectionRequest())
	if err != context.Canceled {
		t.Fatalf("doRequest() error = %v, want context.Canceled", err)
	}
	if c.IsValid() {
		t.Fatalf("connection should be invalid after canceled request")
	}
	if closeErr := c.Close(); closeErr != context.Canceled {
		t.Fatalf("Close() error = %v, want context.Canceled", closeErr)
	}
}

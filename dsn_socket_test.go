package i400

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestParseDSNSocketOptionDefaults(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pw@host.example.com")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.KeepAlive != nil {
		t.Errorf("KeepAlive = %v, want nil (platform default)", *cfg.KeepAlive)
	}
	if cfg.TCPNoDelay != nil {
		t.Errorf("TCPNoDelay = %v, want nil (Go default)", *cfg.TCPNoDelay)
	}
	if cfg.ReceiveBufferSize != 0 || cfg.SendBufferSize != 0 {
		t.Errorf("buffer sizes = %d/%d, want 0/0", cfg.ReceiveBufferSize, cfg.SendBufferSize)
	}
	if cfg.SocketTimeout != 0 {
		t.Errorf("SocketTimeout = %s, want 0", cfg.SocketTimeout)
	}
}

func TestParseDSNSocketOptionValues(t *testing.T) {
	cfg, err := ParseDSN("as400://host/LIB?keepAlive=false&tcpNoDelay=true&receiveBufferSize=65536&sendBufferSize=32768&socketTimeout=2s")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.KeepAlive == nil || *cfg.KeepAlive {
		t.Errorf("KeepAlive = %v, want explicit false", cfg.KeepAlive)
	}
	if cfg.TCPNoDelay == nil || !*cfg.TCPNoDelay {
		t.Errorf("TCPNoDelay = %v, want explicit true", cfg.TCPNoDelay)
	}
	if cfg.ReceiveBufferSize != 65536 || cfg.SendBufferSize != 32768 {
		t.Errorf("buffer sizes = %d/%d", cfg.ReceiveBufferSize, cfg.SendBufferSize)
	}
	if cfg.SocketTimeout != 2*time.Second {
		t.Errorf("SocketTimeout = %s, want 2s", cfg.SocketTimeout)
	}
}

func TestParseDSNSocketTimeoutBareNumberIsMilliseconds(t *testing.T) {
	cfg, err := ParseDSN("as400://host?socketTimeout=1500")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.SocketTimeout != 1500*time.Millisecond {
		t.Fatalf("SocketTimeout = %s, want 1.5s", cfg.SocketTimeout)
	}
}

func TestParseDSNSocketOptionJTOpenSpelling(t *testing.T) {
	cfg, err := ParseDSN("as400://host?keep%20alive=true&tcp%20no%20delay=false&receive%20buffer%20size=8192&send_buffer_size=4096&socket%20timeout=500")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.KeepAlive == nil || !*cfg.KeepAlive {
		t.Errorf("KeepAlive not parsed from JTOpen spelling")
	}
	if cfg.TCPNoDelay == nil || *cfg.TCPNoDelay {
		t.Errorf("TCPNoDelay not parsed from JTOpen spelling")
	}
	if cfg.ReceiveBufferSize != 8192 || cfg.SendBufferSize != 4096 {
		t.Errorf("buffer sizes = %d/%d", cfg.ReceiveBufferSize, cfg.SendBufferSize)
	}
	if cfg.SocketTimeout != 500*time.Millisecond {
		t.Errorf("SocketTimeout = %s, want 500ms", cfg.SocketTimeout)
	}
}

func TestParseDSNSocketOptionInvalidValues(t *testing.T) {
	cases := []string{
		"keepAlive=maybe",
		"tcpNoDelay=sometimes",
		"receiveBufferSize=-1",
		"receiveBufferSize=abc",
		"receiveBufferSize=1073741824",
		"sendBufferSize=-5",
		"sendBufferSize=1.5",
		"socketTimeout=-1",
		"socketTimeout=-2s",
		"socketTimeout=soon",
	}
	for _, query := range cases {
		_, err := ParseDSN("as400://host?" + query)
		if !errors.Is(err, ErrInvalidDSN) {
			t.Errorf("ParseDSN(%q) error = %v, want ErrInvalidDSN", query, err)
		}
	}
}

func TestConfigValidateRejectsNegativeSocketOptions(t *testing.T) {
	cases := map[string]Config{
		"receive": {ReceiveBufferSize: -1},
		"send":    {SendBufferSize: -1},
		"timeout": {SocketTimeout: -time.Second},
	}
	for name, cfg := range cases {
		cfg.Host = "host"
		cfg.BlockSize = defaultBlockSizeKB
		if err := cfg.Validate(); !errors.Is(err, ErrInvalidDSN) {
			t.Errorf("%s: Validate() = %v, want ErrInvalidDSN", name, err)
		}
	}
}

func listenLoopback(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	_, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	return ln, port
}

func TestDialHostServerAppliesSocketOptions(t *testing.T) {
	ln, port := listenLoopback(t)

	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := ln.Accept(); err == nil {
			accepted <- conn
		}
	}()

	keepAlive := true
	noDelay := false
	cfg := Config{
		Host:              "127.0.0.1",
		ConnectTimeout:    5 * time.Second,
		KeepAlive:         &keepAlive,
		TCPNoDelay:        &noDelay,
		ReceiveBufferSize: 64 * 1024,
		SendBufferSize:    64 * 1024,
		SocketTimeout:     5 * time.Second,
	}
	conn, err := dialHostServer(context.Background(), cfg, port)
	if err != nil {
		t.Fatalf("dialHostServer: %v", err)
	}
	defer conn.Close()

	server := <-accepted
	defer server.Close()

	if _, err := server.Write([]byte("ping")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("payload = %q", buf)
	}
}

func TestDialHostServerSocketTimeoutBoundsRead(t *testing.T) {
	ln, port := listenLoopback(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		if conn, err := ln.Accept(); err == nil {
			accepted <- conn
		}
	}()

	cfg := Config{Host: "127.0.0.1", ConnectTimeout: 5 * time.Second, SocketTimeout: 100 * time.Millisecond}
	conn, err := dialHostServer(context.Background(), cfg, port)
	if err != nil {
		t.Fatalf("dialHostServer: %v", err)
	}
	defer conn.Close()
	defer (<-accepted).Close()

	start := time.Now()
	if _, err := readPacket(conn); err == nil {
		t.Fatal("readPacket succeeded with no server data")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("read took %s; socket timeout not applied", elapsed)
	}
}

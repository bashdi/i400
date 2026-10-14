package i400

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"
)

func TestUCDefaultPortAndServiceName(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		wantPort    int
		wantService string
	}{
		{name: "plain", cfg: Config{}, wantPort: 8471, wantService: "as-database"},
		{name: "TLS", cfg: Config{UseTLS: true}, wantPort: 9471, wantService: "as-database-s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DefaultPort(); got != tt.wantPort {
				t.Errorf("DefaultPort() = %d, want %d", got, tt.wantPort)
			}
			if got := tt.cfg.DatabaseServiceName(); got != tt.wantService {
				t.Errorf("DatabaseServiceName() = %q, want %q", got, tt.wantService)
			}
		})
	}
}

func TestUCResolveAddress(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "default plain port", cfg: Config{Host: "ibmi.example"}, want: "ibmi.example:8471"},
		{name: "default TLS port", cfg: Config{Host: "ibmi.example", UseTLS: true}, want: "ibmi.example:9471"},
		{name: "explicit port wins over TLS", cfg: Config{Host: "ibmi.example", UseTLS: true, Port: 1234}, want: "ibmi.example:1234"},
		{name: "IPv6 host is bracketed", cfg: Config{Host: "::1"}, want: "[::1]:8471"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveAddress(); got != tt.want {
				t.Errorf("ResolveAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUCParseDSNTLSOffByDefault(t *testing.T) {
	cfg, err := ParseDSN("as400://user@ibmi.example/")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.UseTLS || cfg.DefaultPort() != 8471 || cfg.DatabaseServiceName() != "as-database" {
		t.Errorf("plain DSN: UseTLS=%v default port=%d service=%q", cfg.UseTLS, cfg.DefaultPort(), cfg.DatabaseServiceName())
	}
	if got := cfg.ResolveAddress(); got != "ibmi.example:8471" {
		t.Errorf("ResolveAddress() = %q, want ibmi.example:8471", got)
	}

	for _, dsn := range []string{"as400s://user@ibmi.example/", "as400://user@ibmi.example/?tls=true"} {
		cfg, err := ParseDSN(dsn)
		if err != nil {
			t.Fatalf("ParseDSN(%q) error = %v", dsn, err)
		}
		if !cfg.UseTLS || cfg.DefaultPort() != 9471 || cfg.DatabaseServiceName() != "as-database-s" {
			t.Errorf("%q: UseTLS=%v default port=%d service=%q", dsn, cfg.UseTLS, cfg.DefaultPort(), cfg.DatabaseServiceName())
		}
	}

	cfg, err = ParseDSN("as400s://user@ibmi.example/?tls=false")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.UseTLS {
		t.Error("tls=false should turn TLS off even for as400s")
	}
}

func TestUCConfigClone(t *testing.T) {
	original := Config{
		Host:      "h",
		Libraries: []string{"A", "B"},
		RawQuery:  url.Values{"k": {"v"}},
		TLSConfig: &tls.Config{ServerName: "x"},
	}
	cloned := original.clone()

	cloned.Libraries[0] = "CHANGED"
	cloned.RawQuery["k"][0] = "changed"
	cloned.RawQuery["added"] = []string{"1"}
	cloned.TLSConfig.ServerName = "changed"
	cloned.Host = "other"

	if original.Libraries[0] != "A" {
		t.Errorf("original Libraries shared with clone: %v", original.Libraries)
	}
	if original.RawQuery["k"][0] != "v" || len(original.RawQuery) != 1 {
		t.Errorf("original RawQuery shared with clone: %v", original.RawQuery)
	}
	if original.TLSConfig.ServerName != "x" {
		t.Errorf("original TLSConfig shared with clone: %q", original.TLSConfig.ServerName)
	}
	if original.Host != "h" {
		t.Errorf("original Host changed to %q", original.Host)
	}

	empty := Config{Host: "h"}.clone()
	if empty.Libraries != nil || empty.RawQuery != nil || empty.TLSConfig != nil {
		t.Errorf("clone of empty config added values: %+v", empty)
	}
}

func TestUCParseDurationValue(t *testing.T) {
	tests := []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{value: "30s", want: 30 * time.Second},
		{value: "2m", want: 2 * time.Minute},
		{value: "1h30m", want: 90 * time.Minute},
		{value: "1.5s", want: 1500 * time.Millisecond},
		{value: "45", want: 45 * time.Second},
		{value: "0", want: 0},
		{value: "", wantErr: true},
		{value: "abc", wantErr: true},
		{value: "10x", wantErr: true},
		{value: "5 s", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseDurationValue(tt.value)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseDurationValue(%q) = %v, want error", tt.value, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDurationValue(%q) error = %v", tt.value, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDurationValue(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestUCConnectTimeoutOptionParsing(t *testing.T) {
	cfg, err := ParseDSN("as400://user@h/?connectTimeout=10")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout = %v, want 10s", cfg.ConnectTimeout)
	}
	cfg, err = ParseDSN("as400://user@h/")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.ConnectTimeout != 15*time.Second {
		t.Errorf("default ConnectTimeout = %v, want 15s", cfg.ConnectTimeout)
	}
}

func TestUCDriverOpenRejectsBadDSN(t *testing.T) {
	if _, err := (&Driver{}).Open("not a dsn"); !errors.Is(err, ErrInvalidDSN) {
		t.Errorf("Open() error = %v, want ErrInvalidDSN", err)
	}
	if _, err := (&Driver{}).OpenConnector("as400://"); !errors.Is(err, ErrInvalidDSN) {
		t.Errorf("OpenConnector() error = %v, want ErrInvalidDSN", err)
	}
	connector, err := (&Driver{}).OpenConnector("as400://user@h/")
	if err != nil {
		t.Fatalf("OpenConnector() error = %v", err)
	}
	if connector.Driver() != driverInstance {
		t.Errorf("Connector.Driver() is not the registered driver")
	}
}

func TestUCResolvePortValidation(t *testing.T) {
	ctx := context.Background()
	if _, err := ResolvePort(ctx, "  ", "as-database"); !errors.Is(err, ErrConnection) {
		t.Errorf("ResolvePort() with blank host error = %v, want ErrConnection", err)
	}
	if _, err := ResolvePort(ctx, "ibmi.example", " "); !errors.Is(err, ErrConnection) {
		t.Errorf("ResolvePort() with blank service error = %v, want ErrConnection", err)
	}
}

// TestUCResolvePortReplies runs the port mapper protocol against a fake mapper
// on port 449: the service name is sent as-is and the reply is '+' and a
// 4-byte port.
func TestUCResolvePortReplies(t *testing.T) {
	t.Run("reads the port", func(t *testing.T) {
		seen := ucStartMapper(t, func(service string) []byte { return ucMapperPort(8471) })
		port, err := ResolvePort(context.Background(), "127.0.0.1", DatabaseServiceName)
		if err != nil {
			t.Fatalf("ResolvePort() error = %v", err)
		}
		if port != 8471 {
			t.Errorf("ResolvePort() = %d, want 8471", port)
		}
		if got := <-seen; got != DatabaseServiceName {
			t.Errorf("service sent = %q, want %q", got, DatabaseServiceName)
		}
	})
	t.Run("unexpected prefix", func(t *testing.T) {
		ucStartMapper(t, func(service string) []byte { return []byte{'-', 0, 0, 0, 0} })
		if _, err := ResolvePort(context.Background(), "127.0.0.1", DatabaseServiceName); err == nil {
			t.Fatal("ResolvePort() error = nil for a '-' reply")
		}
	})
	for _, port := range []int{0, 70000} {
		t.Run(fmt.Sprintf("port %d out of range", port), func(t *testing.T) {
			ucStartMapper(t, func(service string) []byte { return ucMapperPort(port) })
			if _, err := ResolvePort(context.Background(), "127.0.0.1", DatabaseServiceName); err == nil {
				t.Fatalf("ResolvePort() error = nil for port %d", port)
			}
		})
	}
}

func ucBoolPtr(v bool) *bool { return &v }

func TestUCDialHostServerPlainWithOptions(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	cfg := Config{
		Host:              "127.0.0.1",
		ConnectTimeout:    2 * time.Second,
		KeepAlive:         ucBoolPtr(false),
		TCPNoDelay:        ucBoolPtr(true),
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		SocketTimeout:     2 * time.Second,
	}
	port := ln.Addr().(*net.TCPAddr).Port
	conn, err := dialHostServer(context.Background(), cfg, port)
	if err != nil {
		t.Fatalf("dialHostServer() error = %v", err)
	}
	defer conn.Close()
	timeoutConn, ok := conn.(*socketTimeoutConn)
	if !ok {
		t.Fatalf("conn is %T, want *socketTimeoutConn when SocketTimeout is set", conn)
	}
	if _, ok := timeoutConn.Conn.(*net.TCPConn); !ok {
		t.Errorf("underlying conn is %T, want *net.TCPConn", timeoutConn.Conn)
	}
}

func TestUCDialHostServerErrors(t *testing.T) {
	cfg := Config{Host: "127.0.0.1", ConnectTimeout: 2 * time.Second}

	t.Run("invalid port", func(t *testing.T) {
		if _, err := dialHostServer(context.Background(), cfg, 0); !errors.Is(err, ErrConnection) {
			t.Fatalf("dialHostServer(port 0) error = %v, want ErrConnection", err)
		}
	})

	t.Run("refused TCP connection", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("cannot listen on loopback: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		_, err = dialHostServer(context.Background(), cfg, port)
		var transportErr *TransportError
		if !errors.As(err, &transportErr) || transportErr.Operation != "TCP dial" || !errors.Is(err, ErrConnection) {
			t.Fatalf("dialHostServer() error = %v, want TCP dial TransportError wrapping ErrConnection", err)
		}
	})

	t.Run("TLS handshake against a plain server", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("cannot listen on loopback: %v", err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		tlsCfg := cfg
		tlsCfg.UseTLS = true
		port := ln.Addr().(*net.TCPAddr).Port
		_, err = dialHostServer(context.Background(), tlsCfg, port)
		var transportErr *TransportError
		if !errors.As(err, &transportErr) || transportErr.Operation != "TLS dial" || !errors.Is(err, ErrConnection) {
			t.Fatalf("dialHostServer() error = %v, want TLS dial TransportError wrapping ErrConnection", err)
		}
	})
}

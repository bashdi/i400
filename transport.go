package i400

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// maxSocketBufferSize bounds the receive/send buffer sizes accepted in the DSN.
const maxSocketBufferSize = 16 << 20

func dialHostServer(ctx context.Context, cfg Config, port int) (net.Conn, error) {
	if port <= 0 {
		return nil, fmt.Errorf("%w: invalid port %d", ErrConnection, port)
	}

	dialer := net.Dialer{}
	if cfg.ConnectTimeout > 0 {
		dialer.Timeout = cfg.ConnectTimeout
	}
	if cfg.KeepAlive != nil {
		// net.Dialer: 0 enables keep-alives with the default period, negative disables them.
		if *cfg.KeepAlive {
			dialer.KeepAlive = 0
		} else {
			dialer.KeepAlive = -1
		}
	}
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(port))

	var conn net.Conn
	if cfg.UseTLS {
		tlsConfig := cfg.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{ServerName: cfg.Host}
		} else if tlsConfig.ServerName == "" {
			clone := tlsConfig.Clone()
			clone.ServerName = cfg.Host
			tlsConfig = clone
		}
		raw, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, &TransportError{Operation: "TLS dial", Err: fmt.Errorf("%w: %v", ErrConnection, err)}
		}
		if err := applySocketOptions(raw, cfg); err != nil {
			raw.Close()
			return nil, err
		}
		// Like tls.DialWithDialer, bound the handshake by the connect timeout.
		handshakeCtx := ctx
		if cfg.ConnectTimeout > 0 {
			var cancel context.CancelFunc
			handshakeCtx, cancel = context.WithTimeout(ctx, cfg.ConnectTimeout)
			defer cancel()
		}
		tlsConn := tls.Client(raw, tlsConfig)
		if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
			raw.Close()
			return nil, &TransportError{Operation: "TLS dial", Err: fmt.Errorf("%w: %v", ErrConnection, err)}
		}
		conn = tlsConn
	} else {
		raw, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, &TransportError{Operation: "TCP dial", Err: fmt.Errorf("%w: %v", ErrConnection, err)}
		}
		if err := applySocketOptions(raw, cfg); err != nil {
			raw.Close()
			return nil, err
		}
		conn = raw
	}

	if cfg.SocketTimeout > 0 {
		conn = &socketTimeoutConn{Conn: conn, timeout: cfg.SocketTimeout}
	}
	return conn, nil
}

// applySocketOptions sets the TCP options that net.Dialer does not cover
// (TCP_NODELAY and the socket buffer sizes). Options left unset keep the
// platform default.
func applySocketOptions(conn net.Conn, cfg Config) error {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}
	fail := func(option string, err error) error {
		return &TransportError{Operation: "socket option " + option, Err: fmt.Errorf("%w: %v", ErrConnection, err)}
	}
	if cfg.TCPNoDelay != nil {
		if err := tcp.SetNoDelay(*cfg.TCPNoDelay); err != nil {
			return fail("tcpNoDelay", err)
		}
	}
	if cfg.ReceiveBufferSize > 0 {
		if err := tcp.SetReadBuffer(cfg.ReceiveBufferSize); err != nil {
			return fail("receiveBufferSize", err)
		}
	}
	if cfg.SendBufferSize > 0 {
		if err := tcp.SetWriteBuffer(cfg.SendBufferSize); err != nil {
			return fail("sendBufferSize", err)
		}
	}
	return nil
}

// socketTimeoutConn applies JTOpen's "socket timeout" (SO_TIMEOUT) semantics:
// every Read waits at most timeout for data. A deadline installed through
// SetDeadline (the context deadline used by setConnDeadline) still takes
// precedence when it is earlier, so context cancellation is not delayed.
type socketTimeoutConn struct {
	net.Conn
	timeout time.Duration

	mu          sync.Mutex
	ctxDeadline time.Time
}

func (c *socketTimeoutConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.ctxDeadline = t
	c.mu.Unlock()
	return c.Conn.SetDeadline(t)
}

func (c *socketTimeoutConn) Read(p []byte) (int, error) {
	deadline := time.Now().Add(c.timeout)
	c.mu.Lock()
	ctxDeadline := c.ctxDeadline
	c.mu.Unlock()
	if !ctxDeadline.IsZero() && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := c.Conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

// parseSocketOptions reads the JTOpen socket properties from the DSN:
// keepAlive ("keep alive"), tcpNoDelay ("tcp no delay"), receiveBufferSize
// ("receive buffer size"), sendBufferSize ("send buffer size") and
// socketTimeout ("socket timeout"). A bare integer socketTimeout is in
// milliseconds, as in JTOpen; otherwise a Go duration such as "30s" is accepted.
func parseSocketOptions(config *Config) error {
	q := config.RawQuery

	if value, ok := optionValue(q, "keepAlive"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid keepAlive value %q", ErrInvalidDSN, value)
		}
		config.KeepAlive = &enabled
	}
	if value, ok := optionValue(q, "tcpNoDelay"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid tcpNoDelay value %q", ErrInvalidDSN, value)
		}
		config.TCPNoDelay = &enabled
	}
	if value, ok := optionValue(q, "receiveBufferSize"); ok {
		size, err := strconv.Atoi(value)
		if err != nil || size < 0 || size > maxSocketBufferSize {
			return fmt.Errorf("%w: invalid receiveBufferSize value %q", ErrInvalidDSN, value)
		}
		config.ReceiveBufferSize = size
	}
	if value, ok := optionValue(q, "sendBufferSize"); ok {
		size, err := strconv.Atoi(value)
		if err != nil || size < 0 || size > maxSocketBufferSize {
			return fmt.Errorf("%w: invalid sendBufferSize value %q", ErrInvalidDSN, value)
		}
		config.SendBufferSize = size
	}
	if value, ok := optionValue(q, "socketTimeout"); ok {
		timeout, err := parseSocketTimeoutValue(value)
		if err != nil {
			return fmt.Errorf("%w: invalid socketTimeout value %q", ErrInvalidDSN, value)
		}
		config.SocketTimeout = timeout
	}
	return nil
}

func parseSocketTimeoutValue(value string) (time.Duration, error) {
	if millis, err := strconv.Atoi(value); err == nil {
		if millis < 0 {
			return 0, fmt.Errorf("negative socket timeout %d", millis)
		}
		return time.Duration(millis) * time.Millisecond, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if timeout < 0 {
		return 0, fmt.Errorf("negative socket timeout %s", value)
	}
	return timeout, nil
}

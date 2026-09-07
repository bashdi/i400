package i400

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
)

func dialHostServer(ctx context.Context, cfg Config, port int) (net.Conn, error) {
	if port <= 0 {
		return nil, fmt.Errorf("%w: invalid port %d", ErrConnection, port)
	}

	dialer := net.Dialer{}
	if cfg.ConnectTimeout > 0 {
		dialer.Timeout = cfg.ConnectTimeout
	}
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(port))

	if cfg.UseTLS {
		tlsConfig := cfg.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{ServerName: cfg.Host}
		} else if tlsConfig.ServerName == "" {
			clone := tlsConfig.Clone()
			clone.ServerName = cfg.Host
			tlsConfig = clone
		}
		conn, err := tls.DialWithDialer(&dialer, "tcp", address, tlsConfig)
		if err != nil {
			return nil, &TransportError{Operation: "TLS dial", Err: fmt.Errorf("%w: %v", ErrConnection, err)}
		}
		return conn, nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, &TransportError{Operation: "TCP dial", Err: fmt.Errorf("%w: %v", ErrConnection, err)}
	}
	return conn, nil
}

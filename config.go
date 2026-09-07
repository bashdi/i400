package i400

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NamingConvention string

const (
	NamingSQL    NamingConvention = "sql"
	NamingSystem NamingConvention = "system"

	defaultDateFormat        = 5
	defaultDateSeparator     = 0
	defaultTimeFormat        = 0
	defaultTimeSeparator     = 0
	defaultDecimalSeparator  = 0
	defaultCommitmentControl = 1
)

type Config struct {
	DSN                string
	Host               string
	Port               int
	UseTLS             bool
	User               string
	Password           string
	Schema             string
	Naming             NamingConvention
	DateFormat         int
	DateSeparator      int
	TimeFormat         int
	TimeSeparator      int
	DecimalSeparator   int
	CommitmentControl  int
	Libraries          []string
	RawQuery           url.Values
	ConnectTimeout     time.Duration
	StatementCacheSize int
	ScrollableCursors  bool
	HoldCursors        bool
	TLSConfig          *tls.Config
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidDSN)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("%w: invalid port %d", ErrInvalidDSN, c.Port)
	}
	if c.DateFormat < 0 || c.DateFormat > 7 {
		return fmt.Errorf("%w: invalid dateFormat %d", ErrInvalidDSN, c.DateFormat)
	}
	if c.DateSeparator < 0 || c.DateSeparator > 4 {
		return fmt.Errorf("%w: invalid dateSeparator %d", ErrInvalidDSN, c.DateSeparator)
	}
	if c.TimeFormat < 0 || c.TimeFormat > 4 {
		return fmt.Errorf("%w: invalid timeFormat %d", ErrInvalidDSN, c.TimeFormat)
	}
	if c.TimeSeparator < 0 || c.TimeSeparator > 3 {
		return fmt.Errorf("%w: invalid timeSeparator %d", ErrInvalidDSN, c.TimeSeparator)
	}
	if c.DecimalSeparator < 0 || c.DecimalSeparator > 1 {
		return fmt.Errorf("%w: invalid decimalSeparator %d", ErrInvalidDSN, c.DecimalSeparator)
	}
	if c.CommitmentControl < 0 || c.CommitmentControl > 4 {
		return fmt.Errorf("%w: invalid commitmentControl %d", ErrInvalidDSN, c.CommitmentControl)
	}
	if c.StatementCacheSize < 0 {
		return fmt.Errorf("%w: invalid statementCacheSize %d", ErrInvalidDSN, c.StatementCacheSize)
	}
	return nil
}

func (c Config) DefaultPort() int {
	if c.UseTLS {
		return SecureDatabasePort
	}
	return DatabasePort
}

func (c Config) DatabaseServiceName() string {
	if c.UseTLS {
		return DatabaseServiceSecureName
	}
	return DatabaseServiceName
}

func (c Config) ResolveAddress() string {
	port := c.Port
	if port == 0 {
		port = c.DefaultPort()
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

func (c Config) clone() Config {
	clone := c
	if c.Libraries != nil {
		clone.Libraries = append([]string(nil), c.Libraries...)
	}
	if c.RawQuery != nil {
		clone.RawQuery = make(url.Values, len(c.RawQuery))
		for key, values := range c.RawQuery {
			clone.RawQuery[key] = append([]string(nil), values...)
		}
	}
	if c.TLSConfig != nil {
		clone.TLSConfig = c.TLSConfig.Clone()
	}
	return clone
}

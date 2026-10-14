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

	defaultQueryOptimizeGoal   = 0
	defaultDecimalMaxPrecision = 31
	defaultDecimalMaxScale     = 31
	defaultMinimumDivideScale  = 0
	defaultLOBThreshold        = 1024 * 1024
	maxLOBThreshold            = 15728640
	defaultBlockSizeKB         = 256
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
	// QueryOptimizeGoal: 0 = server default (not sent), 1 = *FIRSTIO, 2 = *ALLIO.
	QueryOptimizeGoal int
	// DecimalMaxPrecision is 31 or 63.
	DecimalMaxPrecision int
	// DecimalMaxScale is 0-63.
	DecimalMaxScale int
	// MinimumDivideScale is 0-9.
	MinimumDivideScale int
	// LOBThreshold is the inline LOB threshold in bytes, 0-15728640.
	LOBThreshold int
	// BlockSize is the fetch block size in KB (JTOpen "block size").
	BlockSize int
	// TrueAutocommit makes autocommit keep the configured commitment control level.
	TrueAutocommit bool
	// UseBlockUpdate is JTOpen's "use block update". When true, ExecBatch sends
	// eligible rows as parameter blocks. Default false.
	UseBlockUpdate bool
	// ReturnGeneratedKeys wraps INSERT statements run through ExecContext as
	// SELECT *SQLGENCOLUMNS FROM NEW TABLE(...) to read the generated key (V6R1+).
	ReturnGeneratedKeys bool
	// DecfloatRoundingMode: 0 half-even, 1 half-up, 2 down, 3 ceiling, 4 floor, 5 half-down, 6 up.
	DecfloatRoundingMode int
	// ExtendedDynamic enables JTOpen-style extended dynamic support (SQL packages).
	ExtendedDynamic bool
	// Package is the user package name; it is normalized to 6 characters plus a 4-character suffix.
	Package string
	// PackageLibrary is the library that holds the package. Empty means QGPL.
	PackageLibrary string
	// PackageCriteria is "default" or "select". Empty means "default".
	PackageCriteria string
	// PackageAddDisabled turns off the "package add" server attribute (default allow).
	PackageAddDisabled bool
	// PackageError is "none", "warning" or "exception". Empty means "warning".
	PackageError string
	// PackageCCSID is the statement-text CCSID for packages. Zero means 13488.
	PackageCCSID int
	// PackageCCSIDSystem is set when packageCCSID=system was requested.
	PackageCCSIDSystem bool
	// PackageCache enables the client-side package cache: an existing package is
	// downloaded with RETURN_PACKAGE and matching packaged statements skip prepare.
	PackageCache bool
	// PackageClear is JTOpen's "package clear" option. It is parsed for
	// compatibility and has no effect: JTOpen stopped sending the clearing
	// threshold, and the server clears full packages on its own.
	PackageClear bool
	// ApplicationName, ClientUser, ClientAccounting, ClientHostname and
	// ClientProgramID are JTOpen client info values. Empty means not set.
	ApplicationName  string
	ClientUser       string
	ClientAccounting string
	ClientHostname   string
	ClientProgramID  string
	// QueryStorageLimit is the query storage limit in bytes; -1 (default) is *NOMAX and is not sent.
	QueryStorageLimit int
	// QueryTimeoutMechanism is "cancel" (default) or "qqrytimlmt" (see clientinfo.go).
	QueryTimeoutMechanism string
	// KeepAlive enables (true) or disables (false) TCP keep-alive probes (JTOpen
	// "keep alive"). Nil keeps the platform default.
	KeepAlive *bool
	// TCPNoDelay sets TCP_NODELAY (JTOpen "tcp no delay"). Nil keeps the Go default.
	TCPNoDelay *bool
	// ReceiveBufferSize is the socket receive buffer in bytes (JTOpen "receive
	// buffer size"). Zero keeps the OS default.
	ReceiveBufferSize int
	// SendBufferSize is the socket send buffer in bytes (JTOpen "send buffer
	// size"). Zero keeps the OS default.
	SendBufferSize int
	// SocketTimeout bounds each read from the server (JTOpen "socket timeout").
	// Zero means no socket timeout.
	SocketTimeout time.Duration
	// ErrorsFull requests second-level message text with SQL errors (JTOpen
	// "errors=full"). False means basic: message ID and first-level text only.
	ErrorsFull bool
	// TimestampString returns TIMESTAMP result columns as IBM i formatted
	// strings with every fractional digit the server sent (DSN option
	// timestampPrecision=string). False (nanoseconds) decodes them to time.Time.
	TimestampString bool
	// BidiStringType is the bidi string type of character data (DSN "bidiStringType"):
	// 0, 4 to 11, or -1 when not set. Not set converts each column with the string type of
	// its CCSID (JTOpen's SQLChar). The default is 5, as in JTOpen. It only applies to the
	// CCSIDs that JTOpen converts with a bidi table, see IsConversionBidiCCSID.
	BidiStringType int
	// BidiImplicitReordering (DSN "bidiImplicitReordering") selects implicit to implicit
	// reordering. The default is true.
	BidiImplicitReordering bool
	// BidiNumericOrdering (DSN "bidiNumericOrdering") selects the numeric ordering round trip.
	// The default is false.
	BidiNumericOrdering bool
	TLSConfig           *tls.Config
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: host is required", ErrInvalidDSN)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("%w: invalid port %d", ErrInvalidDSN, c.Port)
	}
	if !validBidiStringType(c.BidiStringType) {
		return fmt.Errorf("%w: invalid bidiStringType %d", ErrInvalidDSN, c.BidiStringType)
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
	if c.QueryOptimizeGoal < 0 || c.QueryOptimizeGoal > 2 {
		return fmt.Errorf("%w: invalid queryOptimizeGoal %d", ErrInvalidDSN, c.QueryOptimizeGoal)
	}
	if c.DecimalMaxPrecision != 0 && c.DecimalMaxPrecision != 31 && c.DecimalMaxPrecision != 63 {
		return fmt.Errorf("%w: invalid decimalMaxPrecision %d", ErrInvalidDSN, c.DecimalMaxPrecision)
	}
	if c.DecimalMaxScale < 0 || c.DecimalMaxScale > 63 {
		return fmt.Errorf("%w: invalid decimalMaxScale %d", ErrInvalidDSN, c.DecimalMaxScale)
	}
	if c.MinimumDivideScale < 0 || c.MinimumDivideScale > 9 {
		return fmt.Errorf("%w: invalid minimumDivideScale %d", ErrInvalidDSN, c.MinimumDivideScale)
	}
	if c.LOBThreshold < 0 || c.LOBThreshold > maxLOBThreshold {
		return fmt.Errorf("%w: invalid lobThreshold %d", ErrInvalidDSN, c.LOBThreshold)
	}
	if !validBlockSize(c.BlockSize) {
		return fmt.Errorf("%w: invalid blockSize %d", ErrInvalidDSN, c.BlockSize)
	}
	if c.DecfloatRoundingMode < 0 || c.DecfloatRoundingMode > 6 {
		return fmt.Errorf("%w: invalid decfloatRoundingMode %d", ErrInvalidDSN, c.DecfloatRoundingMode)
	}
	if c.ReceiveBufferSize < 0 || c.ReceiveBufferSize > maxSocketBufferSize {
		return fmt.Errorf("%w: invalid receiveBufferSize %d", ErrInvalidDSN, c.ReceiveBufferSize)
	}
	if c.SendBufferSize < 0 || c.SendBufferSize > maxSocketBufferSize {
		return fmt.Errorf("%w: invalid sendBufferSize %d", ErrInvalidDSN, c.SendBufferSize)
	}
	if c.SocketTimeout < 0 {
		return fmt.Errorf("%w: invalid socketTimeout %s", ErrInvalidDSN, c.SocketTimeout)
	}
	return validatePackageConfig(c)
}

// validBlockSize mirrors JTOpen: 0, 8, 16, 32, 64, 128, 256, 512, or any value
// from 513 up to 16000 (KB). Zero means "driver default" (see fetchBufferBytes).
func validBlockSize(kb int) bool {
	switch kb {
	case 0, 8, 16, 32, 64, 128, 256, 512:
		return true
	}
	return kb > 512 && kb <= 16000
}

// fetchBufferBytes returns the fetch buffer size sent with each fetch request.
// A block size of 0 (or unset) falls back to the driver default of 256 KiB.
func (c Config) fetchBufferBytes() uint32 {
	if c.BlockSize <= 0 {
		return defaultBlockSizeKB * 1024
	}
	return uint32(c.BlockSize) * 1024
}

// lobThresholdValue returns the threshold sent to the server, clamped the way
// JTOpen does it: values <= 0 send 0, values above the maximum send the maximum.
func (c Config) lobThresholdValue() uint32 {
	if c.LOBThreshold <= 0 {
		return 0
	}
	if c.LOBThreshold > maxLOBThreshold {
		return maxLOBThreshold
	}
	return uint32(c.LOBThreshold)
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

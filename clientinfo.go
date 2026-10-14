package i400

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"database/sql/driver"
)

// Client info and query limits. These follow JTOpen's
// AS400JDBCConnectionImpl.setClientInfo, DBSQLAttributesDS and the query
// timeout mechanism in AS400JDBCStatement.

// clientInfoMinVRM is V6R1M0 in JTOpen's VRM encoding ((V<<16)|(R<<8)|M).
// JTOpen only sends client info and the query storage limit from V6R1.
const clientInfoMinVRM = 0x060100

const (
	// clientInfoMaxBytes is the length limit of each client info value (JTOpen: 255 bytes).
	clientInfoMaxBytes = 255

	codePointClientAccounting = 0x3837
	codePointApplicationName  = 0x3838
	codePointClientUser       = 0x3839
	codePointClientHostname   = 0x383A
	codePointClientProgramID  = 0x383B

	codePointQueryStorageLimit = 0x3834
	codePointQueryTimeout      = 0x3817

	// defaultQueryStorageLimit is JTOpen's *NOMAX (-1); it is not sent.
	defaultQueryStorageLimit = -1
	// maxQueryStorageLimit is AS400JDBCDataSource.MAX_STORAGE_LIMIT.
	maxQueryStorageLimit = 2147352578

	queryTimeoutMechanismCancel     = "cancel"
	queryTimeoutMechanismQQRYTIMLMT = "qqrytimlmt"
)

// ErrUnknownClientInfo is returned by SetClientInfo for a name that is not one
// of the client info keys (JTOpen raises SQLClientInfoException).
var ErrUnknownClientInfo = errors.New("as400 driver: unknown client info name")

// clientInfoAttributes lists the client info keys in JTOpen's order.
var clientInfoAttributes = []struct {
	name      string
	codePoint uint16
}{
	{"ApplicationName", codePointApplicationName},
	{"ClientUser", codePointClientUser},
	{"ClientAccounting", codePointClientAccounting},
	{"ClientHostname", codePointClientHostname},
	{"ClientProgramID", codePointClientProgramID},
}

func clientInfoCodePoint(name string) (uint16, bool) {
	for _, attr := range clientInfoAttributes {
		if attr.name == name {
			return attr.codePoint, true
		}
	}
	return 0, false
}

// clientInfoDSNValue returns the DSN value for a client info key, or "" if unset or unknown.
func clientInfoDSNValue(cfg Config, name string) string {
	switch name {
	case "ApplicationName":
		return cfg.ApplicationName
	case "ClientUser":
		return cfg.ClientUser
	case "ClientAccounting":
		return cfg.ClientAccounting
	case "ClientHostname":
		return cfg.ClientHostname
	case "ClientProgramID":
		return cfg.ClientProgramID
	}
	return ""
}

// parseClientInfoOptions reads the client info, queryStorageLimit and
// queryTimeoutMechanism DSN options.
func parseClientInfoOptions(config *Config) error {
	q := config.RawQuery
	for _, attr := range clientInfoAttributes {
		key := strings.ToLower(attr.name[:1]) + attr.name[1:]
		value, ok := optionValue(q, key)
		if !ok {
			continue
		}
		if len(value) > clientInfoMaxBytes {
			return fmt.Errorf("%w: %s longer than %d bytes", ErrInvalidDSN, key, clientInfoMaxBytes)
		}
		switch attr.name {
		case "ApplicationName":
			config.ApplicationName = value
		case "ClientUser":
			config.ClientUser = value
		case "ClientAccounting":
			config.ClientAccounting = value
		case "ClientHostname":
			config.ClientHostname = value
		case "ClientProgramID":
			config.ClientProgramID = value
		}
	}

	if value, ok := optionValue(q, "queryStorageLimit"); ok {
		limit, err := parseIntOption(value)
		if err != nil {
			return fmt.Errorf("%w: invalid queryStorageLimit value %q", ErrInvalidDSN, value)
		}
		config.QueryStorageLimit = limit
	}

	if value, ok := optionValue(q, "queryTimeoutMechanism"); ok {
		switch strings.ToLower(value) {
		case queryTimeoutMechanismCancel:
			config.QueryTimeoutMechanism = queryTimeoutMechanismCancel
		case queryTimeoutMechanismQQRYTIMLMT:
			config.QueryTimeoutMechanism = queryTimeoutMechanismQQRYTIMLMT
		default:
			return fmt.Errorf("%w: invalid queryTimeoutMechanism value %q", ErrInvalidDSN, value)
		}
	}
	return nil
}

func parseIntOption(value string) (int, error) {
	return strconv.Atoi(value)
}

// clampQueryStorageLimit mirrors JTOpen: values below -1 become -1 and values
// above the maximum become the maximum.
func clampQueryStorageLimit(limit int) int {
	switch {
	case limit < -1:
		return -1
	case limit > maxQueryStorageLimit:
		return maxQueryStorageLimit
	}
	return limit
}

// appendClientInfoAttribute appends one client info attribute as an EBCDIC
// (CCSID 37) string, the same encoding the driver uses for other text attributes.
func appendClientInfoAttribute(dst []byte, codePoint uint16, value string) ([]byte, error) {
	encoded, err := EncodeEBCDIC37(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > clientInfoMaxBytes {
		return nil, fmt.Errorf("client info value longer than %d bytes", clientInfoMaxBytes)
	}
	dst = appendU32(dst, uint32(10+len(encoded)))
	dst = appendU16(dst, codePoint)
	dst = appendU16(dst, 37)
	dst = appendU16(dst, uint16(len(encoded)))
	return appendBytes(dst, encoded), nil
}

// appendVRMOptionAttributes appends the client info values and the query
// storage limit to a set-server-attributes body. Both need a server VRM of at
// least V6R1 (serverVRM 0 means unknown and omits them). Unset client info
// values are not sent; the storage limit is sent only when it is not -1.
func appendVRMOptionAttributes(dst []byte, cfg Config, serverVRM int) ([]byte, int, error) {
	if serverVRM < clientInfoMinVRM {
		return dst, 0, nil
	}
	count := 0
	for _, attr := range clientInfoAttributes {
		value := clientInfoDSNValue(cfg, attr.name)
		if value == "" {
			continue
		}
		var err error
		dst, err = appendClientInfoAttribute(dst, attr.codePoint, value)
		if err != nil {
			return nil, 0, err
		}
		count++
	}
	if cfg.QueryStorageLimit != defaultQueryStorageLimit {
		dst = appendIntAttribute(dst, codePointQueryStorageLimit, uint32(int32(clampQueryStorageLimit(cfg.QueryStorageLimit))))
		count++
	}
	return dst, count, nil
}

// buildSetClientInfoRequest builds a set-attributes request carrying one client info value.
func buildSetClientInfoRequest(codePoint uint16, value string) ([]byte, error) {
	body, err := appendClientInfoAttribute(make([]byte, 0, 64), codePoint, value)
	if err != nil {
		return nil, err
	}
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionSetAttributes)
	request = appendRequestTemplate(request, orsSendReplyImmediately|0x01000000, 0, 0, 1)
	return append(request, body...), nil
}

// buildChangeRPBQueryTimeoutRequest sets the server-side query timeout (0x3817,
// seconds, -1 for none) in the RPB of a statement.
func buildChangeRPBQueryTimeoutRequest(statementHandle uint16, seconds int32) []byte {
	body := appendIntAttribute(make([]byte, 0, 10), codePointQueryTimeout, uint32(seconds))
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionChangeRPB)
	request = appendRequestTemplate(request, 0, statementHandle, 0, 1)
	return append(request, body...)
}

// ClientInfo returns the current value of a client info key ("ApplicationName",
// "ClientUser", "ClientAccounting", "ClientHostname" or "ClientProgramID").
// It returns "" for an unknown name.
func (c *Conn) ClientInfo(name string) string {
	if _, ok := clientInfoCodePoint(name); !ok {
		return ""
	}
	c.mu.RLock()
	value, ok := c.clientInfo[name]
	c.mu.RUnlock()
	if ok {
		return value
	}
	return clientInfoDSNValue(c.cfg, name)
}

// SetClientInfo sets a client info key on the server for this connection, as
// JTOpen's setClientInfo does. It is usable through sql.Conn.Raw. Below V6R1
// the value is kept on the client only. An unknown name returns ErrUnknownClientInfo.
func (c *Conn) SetClientInfo(ctx context.Context, name, value string) error {
	codePoint, ok := clientInfoCodePoint(name)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownClientInfo, name)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var request []byte
	if c.serverVRMAtLeast(clientInfoMinVRM) {
		if !c.IsValid() {
			return driver.ErrBadConn
		}
		var err error
		request, err = buildSetClientInfoRequest(codePoint, value)
		if err != nil {
			return err
		}
	}

	c.mu.Lock()
	if c.clientInfo == nil {
		c.clientInfo = make(map[string]string)
	}
	previous, hadPrevious := c.clientInfo[name]
	c.clientInfo[name] = value
	c.mu.Unlock()

	restore := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if hadPrevious {
			c.clientInfo[name] = previous
		} else {
			delete(c.clientInfo, name)
		}
	}

	if request == nil {
		return nil
	}
	envelope, payload, err := c.doRequest(ctx, request)
	if err != nil {
		restore()
		return err
	}
	if err := c.checkReply(ctx, 0, "set client info", envelope, payload); err != nil {
		restore()
		return err
	}
	return nil
}

// restoreClientInfo sends the DSN values back to the server for every client
// info key changed with SetClientInfo, and forgets those runtime values. It
// runs from ResetSession, so a pooled connection starts with its DSN values.
func (c *Conn) restoreClientInfo(ctx context.Context) error {
	c.mu.Lock()
	changed := c.clientInfo
	c.clientInfo = nil
	c.mu.Unlock()
	if len(changed) == 0 || !c.serverVRMAtLeast(clientInfoMinVRM) {
		return nil
	}
	for _, attr := range clientInfoAttributes {
		if _, ok := changed[attr.name]; !ok {
			continue
		}
		request, err := buildSetClientInfoRequest(attr.codePoint, clientInfoDSNValue(c.cfg, attr.name))
		if err != nil {
			return err
		}
		envelope, payload, err := c.doRequest(ctx, request)
		if err != nil {
			return err
		}
		if err := c.checkReply(ctx, 0, "restore client info", envelope, payload); err != nil {
			return err
		}
	}
	return nil
}

// syncQueryTimeoutRPB applies the query timeout mechanism qqrytimlmt before a
// statement is opened or executed. When the context has a deadline, the
// remaining time (rounded up to whole seconds, at least 1) is set in the
// statement's RPB as 0x3817, so the server also limits the query. When the
// context has no deadline, a timeout set earlier is cleared with -1. With the
// default cancel mechanism nothing is sent, and the driver relies on the
// client-side cancel alone.
func (c *Conn) syncQueryTimeoutRPB(ctx context.Context, statementHandle uint16) error {
	if c == nil || c.cfg.QueryTimeoutMechanism != queryTimeoutMechanismQQRYTIMLMT {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	want := int32(-1)
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		want = remainingSeconds(time.Until(deadline))
	}

	c.mu.RLock()
	previous, seen := c.rpbTimeouts[statementHandle]
	c.mu.RUnlock()
	if !hasDeadline && (!seen || previous == -1) {
		return nil
	}
	if seen && previous == want {
		return nil
	}

	// JTOpen sends the RPB change with connection.send and does not receive a
	// reply; the request asks for none (ORS bitmap 0), so waiting would block
	// until the deadline.
	if err := c.sendRequestNoReply(ctx, buildChangeRPBQueryTimeoutRequest(statementHandle, want)); err != nil {
		return err
	}
	c.mu.Lock()
	if c.rpbTimeouts == nil {
		c.rpbTimeouts = make(map[uint16]int32)
	}
	c.rpbTimeouts[statementHandle] = want
	c.mu.Unlock()
	return nil
}

// sendRequestNoReply writes a request that the server does not answer, as
// JTOpen's connection.send does for the RPB create and change requests. It
// takes the same op lock as doRequest and reads nothing back.
func (c *Conn) sendRequestNoReply(ctx context.Context, request []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		c.invalidateConnection(err)
		return err
	}
	c.mu.RLock()
	conn := c.netConn
	closed := c.closed
	c.mu.RUnlock()
	if closed || conn == nil {
		return driver.ErrBadConn
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)
	if _, err := conn.Write(request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.invalidateConnection(ctxErr)
			return ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return driver.ErrBadConn
	}
	return nil
}

// remainingSeconds rounds a positive duration up to whole seconds, with a minimum of 1.
func remainingSeconds(d time.Duration) int32 {
	secs := math.Ceil(d.Seconds())
	if secs < 1 {
		return 1
	}
	if secs > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(secs)
}

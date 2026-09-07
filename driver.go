package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"
)

type Driver struct{}

type Connector struct {
	cfg Config
}

type Conn struct {
	cfg                      Config
	netConn                  net.Conn
	systemInfo               *SystemInfo
	jobName                  string
	autoCommit               bool
	currentCommitmentControl int
	lastWarning              *SQLWarning
	mu                       sync.RWMutex
	opMu                     sync.Mutex
	nameMu                   sync.Mutex
	statementCacheMu         sync.Mutex
	statementCache           map[string]*cachedStatement
	statementCacheOrder      []string
	statementSeq             uint32
	descriptorSeq            uint32
	cursorSeq                uint32
	closed                   bool
	closeErr                 error
}

type cachedStatement struct {
	statementType         int
	statementHandle       uint16
	statementName         string
	parameterMarkerFormat *parameterMarkerFormat
	refs                  int
	evicted               bool
}

var _ driver.Driver = (*Driver)(nil)
var _ driver.DriverContext = (*Driver)(nil)
var _ driver.Connector = (*Connector)(nil)
var _ driver.Conn = (*Conn)(nil)
var _ driver.ConnPrepareContext = (*Conn)(nil)
var _ driver.Pinger = (*Conn)(nil)
var _ driver.SessionResetter = (*Conn)(nil)
var _ driver.Validator = (*Conn)(nil)
var _ driver.ExecerContext = (*Conn)(nil)
var _ driver.QueryerContext = (*Conn)(nil)
var _ driver.NamedValueChecker = (*Conn)(nil)
var _ driver.ConnBeginTx = (*Conn)(nil)

func (d *Driver) Open(name string) (driver.Conn, error) {
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

func (d *Driver) OpenConnector(name string) (driver.Connector, error) {
	cfg, err := ParseDSN(name)
	if err != nil {
		return nil, err
	}
	return &Connector{cfg: *cfg}, nil
}

func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	return openConn(ctx, c.cfg)
}

func (c *Connector) Driver() driver.Driver {
	return driverInstance
}

func openConn(ctx context.Context, cfg Config) (*Conn, error) {
	info, err := connectSystemInfo(ctx, cfg, cfg.User, cfg.Password)
	if err != nil {
		return nil, err
	}

	dbConn, jobName, err := connectDatabase(ctx, cfg, info, cfg.User, cfg.Password)
	if err != nil {
		return nil, err
	}

	return &Conn{cfg: cfg, netConn: dbConn, systemInfo: info, jobName: jobName, autoCommit: true, currentCommitmentControl: cfg.CommitmentControl, statementCache: make(map[string]*cachedStatement)}, nil
}

func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	conn := c.netConn
	c.closed = true
	c.netConn = nil
	c.mu.Unlock()

	c.opMu.Lock()
	var closeErr error
	if conn != nil {
		_ = sendEndJobRequest(conn)
		closeErr = conn.Close()
	}
	c.opMu.Unlock()

	c.statementCacheMu.Lock()
	c.statementCache = nil
	c.statementCacheOrder = nil
	c.statementCacheMu.Unlock()
	c.mu.Lock()
	c.closeErr = closeErr
	c.mu.Unlock()
	return closeErr
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return c.prepareStmt(context.Background(), query)
}

func (c *Conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *Conn) Ping(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.IsValid() {
		return driver.ErrBadConn
	}
	envelope, _, err := c.doRequest(ctx, buildTestConnectionRequest())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return driver.ErrBadConn
	}
	// The IBM i database server responds to function 0x0000 (test connection)
	// with RCClass=7 and RCCode=-201 to signal that the connection is alive.
	// Any other response (including genuine errors) means the connection is invalid.
	if envelope.RCClass == 7 && envelope.RCCode == -201 {
		return nil
	}
	return driver.ErrBadConn
}

func (c *Conn) ResetSession(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.IsValid() {
		return driver.ErrBadConn
	}

	c.ClearWarnings()

	c.mu.RLock()
	active := !c.autoCommit
	c.mu.RUnlock()
	if active {
		if err := c.rollbackTransaction(ctx); err != nil {
			return err
		}
	}

	c.setCurrentCommitmentControl(c.cfg.CommitmentControl)
	if err := c.setServerAttributes(ctx, true); err != nil {
		return err
	}
	return c.addConfiguredLibraries(ctx)
}

func (c *Conn) IsValid() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.closed && c.netConn != nil
}

func (c *Conn) Warnings() *SQLWarning {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSQLWarningChain(c.lastWarning)
}

func (c *Conn) ClearWarnings() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.lastWarning = nil
	c.mu.Unlock()
}

func (c *Conn) recordWarning(warning *SQLWarning) {
	if c == nil || warning == nil {
		return
	}
	copyWarning := cloneSQLWarningChain(warning)
	if copyWarning == nil {
		return
	}
	c.mu.Lock()
	if c.lastWarning == nil {
		c.lastWarning = copyWarning
	} else {
		tail := c.lastWarning
		for tail.Next != nil {
			tail = tail.Next
		}
		tail.Next = copyWarning
	}
	c.mu.Unlock()
}

func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if !c.IsValid() {
		return nil, driver.ErrBadConn
	}
	if len(args) == 0 {
		return c.execContext(ctx, query, nil)
	}
	rewritten, paramNames, err := rewriteNamedParams(query)
	if err != nil {
		return nil, err
	}
	ordered, err := reorderNamedArgs(paramNames, args)
	if err != nil {
		return nil, err
	}
	return c.execPreparedCached(ctx, rewritten, ordered)
}

func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !c.IsValid() {
		return nil, driver.ErrBadConn
	}
	if len(args) == 0 {
		return c.queryContext(ctx, query, nil)
	}
	rewritten, paramNames, err := rewriteNamedParams(query)
	if err != nil {
		return nil, err
	}
	ordered, err := reorderNamedArgs(paramNames, args)
	if err != nil {
		return nil, err
	}
	return c.queryPreparedCached(ctx, rewritten, ordered)
}

func (c *Conn) CheckNamedValue(value *driver.NamedValue) error {
	if value == nil {
		return nil
	}
	if out, ok := value.Value.(sql.Out); ok {
		if out.Dest == nil {
			return fmt.Errorf("sql.Out destination must not be nil")
		}
		return nil
	}
	switch v := value.Value.(type) {
	// Native driver.Value types – accepted as-is.
	case nil, int64, float64, bool, string, []byte, time.Time:
		return nil
	// Normalize integer types to int64.
	case int:
		value.Value = int64(v)
	case int8:
		value.Value = int64(v)
	case int16:
		value.Value = int64(v)
	case int32:
		value.Value = int64(v)
	case uint:
		if uint64(v) > uint64(1<<63-1) {
			return fmt.Errorf("uint value %d overflows int64", v)
		}
		value.Value = int64(v) //nolint:gosec
	case uint8:
		value.Value = int64(v)
	case uint16:
		value.Value = int64(v)
	case uint32:
		value.Value = int64(v)
	case uint64:
		if v > uint64(1<<63-1) {
			return fmt.Errorf("uint64 value %d overflows int64", v)
		}
		value.Value = int64(v) //nolint:gosec
	case float32:
		value.Value = float64(v)
	default:
		// Unwrap driver.Valuer implementations.
		if valuer, ok := value.Value.(driver.Valuer); ok {
			dv, err := valuer.Value()
			if err != nil {
				return err
			}
			value.Value = dv
			return nil
		}
		rv := reflect.ValueOf(value.Value)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			value.Value = rv.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			unsigned := rv.Uint()
			if unsigned > uint64(1<<63-1) {
				return fmt.Errorf("unsigned value %d overflows int64", unsigned)
			}
			value.Value = int64(unsigned)
		case reflect.Float32, reflect.Float64:
			value.Value = rv.Float()
		case reflect.String:
			value.Value = rv.String()
		default:
			return driver.ErrSkip
		}
	}
	return nil
}

func (c *Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.prepareStmt(ctx, query)
}

func (c *Conn) deadlineFromContext(ctx context.Context) (time.Time, bool) {
	return ctx.Deadline()
}

func (c *Conn) setCurrentCommitmentControl(mode int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.currentCommitmentControl = mode
	c.mu.Unlock()
}

func (c *Conn) currentCommitmentMode() int {
	if c == nil {
		return defaultCommitmentControl
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.currentCommitmentControl < 0 || c.currentCommitmentControl > 4 {
		return defaultCommitmentControl
	}
	return c.currentCommitmentControl
}

func (c *Conn) setServerAttributes(ctx context.Context, autoCommit bool) error {
	request, err := buildSetServerAttributesRequest(c.cfg, autoCommit, c.currentCommitmentMode())
	if err != nil {
		return err
	}
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return fmt.Errorf("database server attributes failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
	}
	c.mu.Lock()
	c.autoCommit = autoCommit
	c.mu.Unlock()
	return nil
}

func (c *Conn) addConfiguredLibraries(ctx context.Context) error {
	request, ok, err := buildAddLibraryListRequest(c.cfg)
	if err != nil || !ok {
		return err
	}
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return fmt.Errorf("database add library list failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
	}
	return nil
}

func (c *Conn) prepareStmt(ctx context.Context, query string) (driver.Stmt, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if !c.IsValid() {
		return nil, driver.ErrBadConn
	}
	rewritten, paramNames, err := rewriteNamedParams(query)
	if err != nil {
		return nil, err
	}
	statementName, statementHandle := c.nextStatementName()
	statementType := classifySQLStatement(rewritten)
	_, parameterFormat, err := c.prepareAndDescribe(ctx, statementHandle, statementName, rewritten)
	if err != nil {
		return nil, err
	}
	return newStmt(c, rewritten, statementName, statementHandle, statementType, parameterFormat, paramNames), nil
}

func (c *Conn) cachedPreparedStatement(ctx context.Context, query string) (*cachedStatement, error) {
	if c.cfg.StatementCacheSize <= 0 {
		statementType, statementHandle, statementName, format, err := c.prepareTransientStatement(ctx, query)
		if err != nil {
			return nil, err
		}
		return &cachedStatement{statementType: statementType, statementHandle: statementHandle, statementName: statementName, parameterMarkerFormat: format}, nil
	}

	c.statementCacheMu.Lock()
	if c.statementCache == nil {
		c.statementCache = make(map[string]*cachedStatement)
	}
	if cached := c.statementCache[query]; cached != nil {
		cached.refs++
		for i, key := range c.statementCacheOrder {
			if key == query {
				c.statementCacheOrder = append(c.statementCacheOrder[:i], c.statementCacheOrder[i+1:]...)
				break
			}
		}
		c.statementCacheOrder = append(c.statementCacheOrder, query)
		c.statementCacheMu.Unlock()
		return cached, nil
	}
	c.statementCacheMu.Unlock()

	statementType, statementHandle, statementName, format, err := c.prepareTransientStatement(ctx, query)
	if err != nil {
		return nil, err
	}
	cached := &cachedStatement{statementType: statementType, statementHandle: statementHandle, statementName: statementName, parameterMarkerFormat: format}
	c.statementCacheMu.Lock()
	if existing := c.statementCache[query]; existing != nil {
		existing.refs++
		c.statementCacheMu.Unlock()
		_ = c.closePreparedStatement(context.Background(), statementName)
		return existing, nil
	}
	if len(c.statementCache) >= c.cfg.StatementCacheSize {
		key := c.statementCacheOrder[0]
		victim := c.statementCache[key]
		victim.evicted = true
		delete(c.statementCache, key)
		c.statementCacheOrder = c.statementCacheOrder[1:]
		if victim.refs == 0 {
			go func() { _ = c.closePreparedStatement(context.Background(), victim.statementName) }()
		}
	}
	cached.refs = 1
	c.statementCache[query] = cached
	c.statementCacheOrder = append(c.statementCacheOrder, query)
	c.statementCacheMu.Unlock()
	return cached, nil
}

func (c *Conn) releaseCachedStatement(statement *cachedStatement) {
	if c == nil || statement == nil {
		return
	}
	var closeName string
	c.statementCacheMu.Lock()
	if statement.refs > 0 {
		statement.refs--
	}
	if statement.evicted && statement.refs == 0 {
		closeName = statement.statementName
	}
	c.statementCacheMu.Unlock()
	if closeName != "" {
		_ = c.closePreparedStatement(context.Background(), closeName)
	}
}

func (c *Conn) execPreparedCached(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.cfg.StatementCacheSize <= 0 {
		return c.execPreparedTransient(ctx, query, args)
	}
	statement, err := c.cachedPreparedStatement(ctx, query)
	if err != nil {
		return nil, err
	}
	defer c.releaseCachedStatement(statement)
	return c.execPrepared(ctx, statement.statementHandle, statement.statementType, statement.parameterMarkerFormat, args)
}

func (c *Conn) queryPreparedCached(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.cfg.StatementCacheSize <= 0 {
		return c.queryPreparedTransient(ctx, query, args)
	}
	statement, err := c.cachedPreparedStatement(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := c.queryPreparedWithArgs(ctx, statement.statementHandle, statement.statementName, statement.parameterMarkerFormat, args)
	if err != nil {
		c.releaseCachedStatement(statement)
		return nil, err
	}
	if preparedRows, ok := rows.(*queryRows); ok {
		preparedRows.releaseStmt = func() { c.releaseCachedStatement(statement) }
	} else {
		c.releaseCachedStatement(statement)
	}
	return rows, nil
}

func (c *Conn) execPreparedTransient(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	statementType, statementHandle, statementName, parameterFormat, err := c.prepareTransientStatement(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = c.closePreparedStatement(context.Background(), statementName)
	}()
	return c.execPrepared(ctx, statementHandle, statementType, parameterFormat, args)
}

func (c *Conn) queryPreparedTransient(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	_, statementHandle, statementName, parameterFormat, err := c.prepareTransientStatement(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := c.queryPreparedWithArgs(ctx, statementHandle, statementName, parameterFormat, args)
	if err != nil {
		_ = c.closePreparedStatement(context.Background(), statementName)
		return nil, err
	}
	if preparedRows, ok := rows.(*queryRows); ok {
		preparedRows.ownStatement = true
	}
	return rows, nil
}

func (c *Conn) prepareTransientStatement(ctx context.Context, query string) (int, uint16, string, *parameterMarkerFormat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	statementType := classifySQLStatement(query)
	statementName, statementHandle := c.nextStatementName()
	_, parameterFormat, err := c.prepareAndDescribe(ctx, statementHandle, statementName, query)
	if err != nil {
		return 0, 0, "", nil, err
	}
	return statementType, statementHandle, statementName, parameterFormat, nil
}

package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	sqlStatementTypeUnknown            = 0
	sqlStatementTypeInsertUpdateDelete = 1
	sqlStatementTypeSelect             = 2
	sqlStatementTypeCall               = 3

	fetchNext        = 0
	fetchFirst       = 2
	fetchLast        = 3
	fetchBeforeFirst = 4
	fetchAfterLast   = 5
	fetchCurrent     = 6
	fetchRelative    = 7
	fetchDirect      = 8
	holdFalse        = 0xD5
	holdTrue         = 0xE8

	orsSendReplyImmediately      uint32 = 0x80000000
	orsDataFormat                uint32 = 0x08000000
	orsResultData                uint32 = 0x04000000
	orsSQLCA                     uint32 = 0x02000000
	orsParameterMarkerFormat     uint32 = 0x00800000
	orsReplyRLECompressed        uint32 = 0x00040000
	orsExtendedColumnDescriptors uint32 = 0x00020000
	orsReturnResultSetAttributes uint32 = 0x00008000

	db2TypeDate           = 384
	db2TypeTime           = 388
	db2TypeTimestamp      = 392
	db2TypeDatalink       = 396
	db2TypeBlob           = 404
	db2TypeClob           = 408
	db2TypeDbclob         = 412
	db2TypeVarchar        = 448
	db2TypeChar           = 452
	db2TypeLongVarchar    = 456
	db2TypeVarGraphic     = 464
	db2TypeGraphic        = 468
	db2TypeLongVarGraphic = 472
	db2TypeFloatingPoint  = 480
	db2TypeDecimal        = 484
	db2TypeNumeric        = 488
	db2TypeBigint         = 492
	db2TypeInteger        = 496
	db2TypeSmallint       = 500
	db2TypeRowID          = 904
	db2TypeVarbinary      = 908
	db2TypeBinary         = 912
	db2TypeBlobLocator    = 960
	db2TypeClobLocator    = 964
	db2TypeDbclobLocator  = 968
	db2TypeXML            = 988
	db2TypeDecfloat       = 996
	db2TypeXMLLocator     = 2452
)

type columnMeta struct {
	Name                 string
	Label                string
	BaseColumnName       string
	BaseTableName        string
	BaseSchemaName       string
	SQLFromTable         string
	SQLFromSchema        string
	UDTName              string
	Type                 int
	Length               int
	Scale                int
	Precision            int
	CCSID                int
	Offset               int
	LobMaxSize           int
	Updateable           int
	Searchable           int
	IsIdentity           bool
	IsAlwaysGenerated    bool
	IsPartOfAnyIndex     bool
	IsLoneUniqueIndex    bool
	IsPartOfUniqueIndex  bool
	IsExpression         bool
	IsPrimaryKey         bool
	IsNamed              bool
	IsRowID              bool
	IsRowChangeTimestamp bool
}

func (c columnMeta) db2Type() int {
	typ := c.Type & 0xFFFE
	if c.CCSID == 65535 {
		switch typ {
		case db2TypeChar:
			return db2TypeBinary
		case db2TypeVarchar, db2TypeLongVarchar:
			return db2TypeVarbinary
		}
	}
	return typ
}

func (c columnMeta) nullable() bool {
	return (c.Type & 0x1) != 0
}

func (c columnMeta) displayName() string {
	if name := strings.TrimSpace(c.Label); name != "" {
		return name
	}
	if name := strings.TrimSpace(c.Name); name != "" {
		return name
	}
	if name := strings.TrimSpace(c.BaseColumnName); name != "" {
		return name
	}
	return ""
}

func (c columnMeta) databaseTypeName() string {
	switch c.db2Type() {
	case db2TypeDate:
		return "DATE"
	case db2TypeTime:
		return "TIME"
	case db2TypeTimestamp:
		return "TIMESTAMP"
	case db2TypeDatalink:
		return "DATALINK"
	case db2TypeBlob:
		return "BLOB"
	case db2TypeClob:
		return "CLOB"
	case db2TypeDbclob:
		return "DBCLOB"
	case db2TypeVarchar:
		return "VARCHAR"
	case db2TypeChar:
		return "CHAR"
	case db2TypeLongVarchar:
		return "LONGVARCHAR"
	case db2TypeVarGraphic:
		return "VARGRAPHIC"
	case db2TypeGraphic:
		return "GRAPHIC"
	case db2TypeLongVarGraphic:
		return "LONGVARGRAPHIC"
	case db2TypeFloatingPoint:
		if c.Length == 4 {
			return "REAL"
		}
		return "DOUBLE"
	case db2TypeDecimal:
		return "DECIMAL"
	case db2TypeNumeric:
		return "NUMERIC"
	case db2TypeBigint:
		return "BIGINT"
	case db2TypeInteger:
		return "INTEGER"
	case db2TypeSmallint:
		return "SMALLINT"
	case db2TypeRowID:
		return "ROWID"
	case db2TypeVarbinary:
		return "VARBINARY"
	case db2TypeBinary:
		return "BINARY"
	case db2TypeBlobLocator:
		return "BLOB LOCATOR"
	case db2TypeClobLocator:
		return "CLOB LOCATOR"
	case db2TypeDbclobLocator:
		return "DBCLOB LOCATOR"
	case db2TypeXML:
		return "SQLXML"
	case db2TypeXMLLocator:
		return "XML"
	case db2TypeDecfloat:
		return "DECFLOAT"
	default:
		return "UNKNOWN"
	}
}

type resultSetMeta struct {
	columns       []columnMeta
	RowSize       int
	DateFormat    int
	TimeFormat    int
	DateSeparator int
	TimeSeparator int
}

func (m *resultSetMeta) ensureColumns(count int) error {
	if count < 0 {
		return fmt.Errorf("invalid column count %d", count)
	}
	if len(m.columns) == 0 {
		m.columns = make([]columnMeta, count)
		return nil
	}
	if len(m.columns) != count {
		return fmt.Errorf("column count mismatch: got %d want %d", count, len(m.columns))
	}
	return nil
}

func (m *resultSetMeta) columnNames() []string {
	if len(m.columns) == 0 {
		return nil
	}
	names := make([]string, len(m.columns))
	for i := range m.columns {
		name := m.columns[i].displayName()
		if name == "" {
			name = fmt.Sprintf("COL%d", i+1)
		}
		names[i] = name
	}
	return names
}

type fetchBlock struct {
	RowCount      int
	ColumnCount   int
	IndicatorSize int
	RowSize       int
	Nulls         []bool
	Data          []byte
}

type parameterMarkerField struct {
	Name          string
	SQLType       int
	Length        int
	Scale         int
	Precision     int
	CCSID         int
	ParameterType int
	LOBLocator    int
	LOBMaxSize    int
}

type parameterMarkerFormat struct {
	codePoint  uint16
	RecordSize int
	Fields     []parameterMarkerField
}

func (f *parameterMarkerFormat) parameterCount() int {
	if f == nil {
		return 0
	}
	return len(f.Fields)
}

func (f *parameterMarkerFormat) field(index int) (parameterMarkerField, bool) {
	if f == nil || index < 0 || index >= len(f.Fields) {
		return parameterMarkerField{}, false
	}
	return f.Fields[index], true
}

func (f *parameterMarkerFormat) usesOriginalFormat() bool {
	return f != nil && f.codePoint == CodePointParameterMarkerFormat
}

func (f *parameterMarkerFormat) usesExtendedFormat() bool {
	if f == nil {
		return false
	}
	return f.codePoint == CodePointExtendedParameterMarker || f.codePoint == CodePointSuperExtendedParameterMarker
}

type sqlcaInfo struct {
	SQLCode         int32
	SQLState        string
	GeneratedKey    int64
	HasGeneratedKey bool
	UpdateCount     int64
	ResultSetsCount int
	Warning         *SQLWarning
}

type execResult struct {
	rowsAffected  int64
	lastInsertID  int64
	hasLastInsert bool
}

func (r *execResult) LastInsertId() (int64, error) {
	if r == nil || !r.hasLastInsert {
		return 0, ErrUnsupported
	}
	return r.lastInsertID, nil
}

func (r *execResult) RowsAffected() (int64, error) {
	if r == nil {
		return 0, ErrUnsupported
	}
	return r.rowsAffected, nil
}

type queryRows struct {
	conn                 *Conn
	ctx                  context.Context
	meta                 *resultSetMeta
	statementHandle      uint16
	statementName        string
	cursorName           string
	fetchBufferSize      uint32
	resultSetCount       int
	resultSetIndex       int
	block                *fetchBlock
	rowIndex             int
	currentRow           int
	currentResultSetDone bool
	scrollable           bool
	ownStatement         bool
	releaseStmt          func()
	closed               bool
}

var _ driver.Rows = (*queryRows)(nil)
var _ driver.RowsNextResultSet = (*queryRows)(nil)
var _ driver.RowsColumnTypeDatabaseTypeName = (*queryRows)(nil)
var _ driver.RowsColumnTypeScanType = (*queryRows)(nil)
var _ driver.RowsColumnTypeNullable = (*queryRows)(nil)
var _ driver.RowsColumnTypeLength = (*queryRows)(nil)
var _ driver.RowsColumnTypePrecisionScale = (*queryRows)(nil)
var _ ScrollableRows = (*queryRows)(nil)

// ScrollableRows is an optional IBM i extension. A query must be opened with
// scrollableCursors=true to provide a scrollable result set. Row returns the
// current driver-known row position and returns zero when no row is current.
type ScrollableRows interface {
	driver.Rows
	BeforeFirst() error
	AfterLast() error
	Current() error
	First() error
	Last() error
	Absolute(row int) error
	Relative(rows int) error
	Row() int
}

func classifySQLStatement(sql string) int {
	text := strings.ToUpper(strings.TrimSpace(sql))
	for len(text) > 0 && text[0] == '(' {
		text = strings.TrimSpace(text[1:])
	}
	switch {
	case strings.HasPrefix(text, "SELECT"), strings.HasPrefix(text, "VALUES"), strings.HasPrefix(text, "WITH"):
		return sqlStatementTypeSelect
	case strings.HasPrefix(text, "INSERT"), strings.HasPrefix(text, "UPDATE"), strings.HasPrefix(text, "DELETE"), strings.HasPrefix(text, "MERGE"):
		return sqlStatementTypeInsertUpdateDelete
	case strings.HasPrefix(text, "CALL"):
		return sqlStatementTypeCall
	default:
		return sqlStatementTypeUnknown
	}
}

func (c *Conn) nextStatementName() (string, uint16) {
	c.nameMu.Lock()
	defer c.nameMu.Unlock()
	c.statementSeq++
	if c.statementSeq == 0 {
		c.statementSeq = 1
	}
	return fmt.Sprintf("STMT%08X", c.statementSeq), uint16(c.statementSeq)
}

func (c *Conn) nextDescriptorHandle() uint16 {
	c.nameMu.Lock()
	defer c.nameMu.Unlock()
	c.descriptorSeq++
	if c.descriptorSeq == 0 {
		c.descriptorSeq = 1
	}
	return uint16(c.descriptorSeq)
}

func (c *Conn) nextCursorName() string {
	c.nameMu.Lock()
	defer c.nameMu.Unlock()
	c.cursorSeq++
	if c.cursorSeq == 0 {
		c.cursorSeq = 1
	}
	return fmt.Sprintf("CRSR%08X", c.cursorSeq)
}

func (c *Conn) doRequest(ctx context.Context, request []byte) (replyEnvelope, []byte, error) {
	return c.doRequestWithPreamble(ctx, nil, request)
}

// doRequestWithPreamble sends an optional preamble packet (fire-and-forget, no reply
// expected) followed by the main request, reading exactly one reply for the main request.
// Both writes are performed under the same opMu lock so they are atomic on the connection.
func (c *Conn) doRequestWithPreamble(ctx context.Context, preamble []byte, request []byte) (replyEnvelope, []byte, error) {
	if c == nil {
		return replyEnvelope{}, nil, driver.ErrBadConn
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		c.invalidateConnection(err)
		return replyEnvelope{}, nil, err
	}
	c.mu.RLock()
	conn := c.netConn
	closed := c.closed
	c.mu.RUnlock()
	if closed || conn == nil {
		return replyEnvelope{}, nil, driver.ErrBadConn
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)
	cancelDeadline := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer cancelDeadline()

	if len(preamble) > 0 {
		if _, err := conn.Write(preamble); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				c.invalidateConnection(ctxErr)
				return replyEnvelope{}, nil, ctxErr
			}
			c.invalidateConnection(driver.ErrBadConn)
			return replyEnvelope{}, nil, driver.ErrBadConn
		}
	}
	if _, err := conn.Write(request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.invalidateConnection(ctxErr)
			return replyEnvelope{}, nil, ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return replyEnvelope{}, nil, driver.ErrBadConn
	}
	packet, err := readPacket(conn)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.invalidateConnection(ctxErr)
			return replyEnvelope{}, nil, ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return replyEnvelope{}, nil, driver.ErrBadConn
	}
	return readReplyEnvelope(packet)
}

func (c *Conn) invalidateConnection(err error) {
	if c == nil {
		return
	}
	if err == nil {
		err = driver.ErrBadConn
	}

	c.mu.Lock()
	conn := c.netConn
	if !c.closed {
		c.closed = true
		c.netConn = nil
	}
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}
}

// canCancelNatively reports whether the connection has the server metadata
// needed to send a protocol-native cancel request on a second connection.
func (c *Conn) canCancelNatively() bool {
	if c == nil || c.systemInfo == nil {
		return false
	}
	return c.systemInfo.ServerFunctionalLevel >= 5 &&
		strings.TrimSpace(c.systemInfo.ServerJobIdentifier) != ""
}

// doStatementRequest sends a database request for a long-running statement
// operation (Execute, OpenDescribe, Fetch) and supports protocol-native context
// cancellation when canCancelNatively() returns true.
//
// When native cancel is available and the context is cancelled, a cancel request
// is sent to IBM i on a second connection while this connection waits for the
// server's reply. IBM i aborts the statement and returns an error reply on the
// main connection; the main connection is NOT invalidated and remains usable.
//
// When native cancel is not available or the cancel connection fails, the function
// falls back to deadline-based interruption (same behaviour as doRequest), which
// invalidates the connection.
func (c *Conn) doStatementRequest(ctx context.Context, request []byte, statementHandle uint16) (replyEnvelope, []byte, error) {
	if !c.canCancelNatively() {
		return c.doRequest(ctx, request)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return replyEnvelope{}, nil, err
	}
	c.mu.RLock()
	conn := c.netConn
	closed := c.closed
	c.mu.RUnlock()
	if closed || conn == nil {
		return replyEnvelope{}, nil, driver.ErrBadConn
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()

	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)

	// connInUse is closed when this function returns. The AfterFunc goroutine
	// checks it before killing the deadline as a fallback, to avoid disturbing
	// the connection after doStatementRequest has already returned.
	connInUse := make(chan struct{})
	defer close(connInUse)

	cancelStop := context.AfterFunc(ctx, func() {
		if err := c.cancelStatement(context.Background(), statementHandle); err == nil {
			// Native cancel sent. IBM i will send an error reply on the main
			// connection; leave the deadline alone so we can read it cleanly.
			return
		}
		// Native cancel failed; fall back to deadline kill only if this function
		// is still blocking on I/O.
		select {
		case <-connInUse:
			// doStatementRequest already returned; don't touch the connection.
		default:
			_ = conn.SetDeadline(time.Now())
		}
	})
	defer cancelStop()

	if _, err := conn.Write(request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.invalidateConnection(ctxErr)
			return replyEnvelope{}, nil, ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return replyEnvelope{}, nil, driver.ErrBadConn
	}

	packet, err := readPacket(conn)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// If native cancel fired and succeeded, we would have received an
			// IBM i error packet (not an I/O error). So we got here because the
			// fallback deadline fired; the connection is no longer usable.
			c.invalidateConnection(ctxErr)
			return replyEnvelope{}, nil, ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return replyEnvelope{}, nil, driver.ErrBadConn
	}

	env, payload, err := readReplyEnvelope(packet)
	if err != nil {
		c.invalidateConnection(driver.ErrBadConn)
		return replyEnvelope{}, nil, driver.ErrBadConn
	}

	// If context was cancelled and IBM i sent a cancel-acknowledgement reply,
	// translate to context.Canceled without invalidating the connection — it is
	// still in a clean state and reusable for subsequent statements.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return replyEnvelope{}, nil, ctxErr
	}

	return env, payload, nil
}

func (c *Conn) queryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return nil, unsupported("QueryContext arguments")
	}
	c.ClearWarnings()
	statementType := classifySQLStatement(query)
	if statementType != sqlStatementTypeSelect && statementType != sqlStatementTypeCall {
		return nil, unsupported("QueryContext only supports SELECT, VALUES, WITH, or CALL statements")
	}

	statementName, statementHandle := c.nextStatementName()

	// Create the RPB on the server as a fire-and-forget preamble (no reply) before prepare.
	creatRPBReq, err := buildCreateRPBRequest(statementHandle, statementName)
	if err != nil {
		return nil, err
	}

	prepareRequest, err := buildPrepareAndDescribeRequest(statementHandle, statementName, query)
	if err != nil {
		return nil, err
	}
	envelope, payload, err := c.doRequestWithPreamble(ctx, creatRPBReq, prepareRequest)
	if err != nil {
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("prepare", envelope)
	}
	prepareSQLCA, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, err
	}
	if prepareSQLCA.SQLCode < 0 {
		message := "SQL execution failed"
		if prepareSQLCA.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", prepareSQLCA.SQLState)
		}
		return nil, &SQLError{Code: int(prepareSQLCA.SQLCode), State: prepareSQLCA.SQLState, Message: message}
	}
	c.recordWarning(prepareSQLCA.Warning)

	return c.queryRowsForStatement(ctx, statementHandle, statementName)
}

func (c *Conn) execContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		return nil, unsupported("ExecContext arguments")
	}
	c.ClearWarnings()
	statementType := classifySQLStatement(query)
	if statementType == sqlStatementTypeSelect {
		return nil, unsupported("ExecContext cannot execute query statements")
	}

	request, err := buildExecuteImmediateRequest(query, statementType)
	if err != nil {
		return nil, err
	}
	envelope, payload, err := c.doRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("execute", envelope)
	}

	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, err
	}
	c.recordWarning(info.Warning)
	if info.SQLCode < 0 {
		message := "SQL execution failed"
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
		}
		return nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
	}
	if info.ResultSetsCount > 0 {
		return nil, fmt.Errorf("statement returned result sets; use QueryContext")
	}

	result := &execResult{rowsAffected: info.UpdateCount}
	if result.rowsAffected < 0 {
		result.rowsAffected = 0
	}
	if info.HasGeneratedKey {
		result.hasLastInsert = true
		result.lastInsertID = info.GeneratedKey
	}
	return result, nil
}

func (c *Conn) execPrepared(ctx context.Context, statementHandle uint16, statementType int, format *parameterMarkerFormat, args []driver.NamedValue) (driver.Result, error) {
	if statementType == sqlStatementTypeSelect {
		return nil, unsupported("ExecContext cannot execute query statements")
	}
	bindings, err := buildParameterBindings(format, args)
	if err != nil {
		return nil, err
	}
	descriptorHandle := uint16(0)
	var envelope replyEnvelope
	var payload []byte
	var info *sqlcaInfo
	if len(bindings) > 0 {
		descriptorHandle = c.nextDescriptorHandle()
		changeRequest, err := buildChangeDescriptorRequest(statementHandle, descriptorHandle, format, bindings)
		if err != nil {
			return nil, err
		}
		defer func() {
			deleteRequest := buildDeleteDescriptorRequest(statementHandle, descriptorHandle)
			if envelope, _, err := c.doRequest(ctx, deleteRequest); err == nil && envelope.hasError() {
				_ = newProtocolError("delete descriptor", envelope)
			}
		}()

		envelope, payload, err = c.doRequest(ctx, changeRequest)
		if err != nil {
			return nil, err
		}
		if envelope.hasError() {
			return nil, newProtocolError("change descriptor", envelope)
		}
		info, err = parseSQLCAFromPayload(payload)
		if err != nil {
			return nil, err
		}
		if info.SQLCode < 0 {
			message := "SQL execution failed"
			if info.SQLState != "" {
				message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
			}
			return nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
		}
		c.recordWarning(info.Warning)
		if err := c.writeLOBDataRequests(ctx, bindings); err != nil {
			return nil, err
		}
	}

	request, err := buildExecutePreparedRequest(statementHandle, descriptorHandle, statementType, format, bindings)
	if err != nil {
		return nil, err
	}
	envelope, payload, err = c.doStatementRequest(ctx, request, statementHandle)
	if err != nil {
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("execute", envelope)
	}

	info, err = parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, err
	}
	c.recordWarning(info.Warning)
	if info.SQLCode < 0 {
		message := "SQL execution failed"
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
		}
		return nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
	}
	if statementType == sqlStatementTypeCall {
		if err := assignCallOutputParameters(ctx, c, payload, bindings); err != nil {
			return nil, err
		}
	}
	if info.ResultSetsCount > 0 {
		return nil, fmt.Errorf("statement returned result sets; use QueryContext")
	}

	result := &execResult{rowsAffected: info.UpdateCount}
	if result.rowsAffected < 0 {
		result.rowsAffected = 0
	}
	if info.HasGeneratedKey {
		result.hasLastInsert = true
		result.lastInsertID = info.GeneratedKey
	}
	return result, nil
}

func (c *Conn) prepareAndDescribe(ctx context.Context, statementHandle uint16, statementName, query string) (*sqlcaInfo, *parameterMarkerFormat, error) {
	// The IBM i server requires an RPB (Request Parameter Block) to exist before
	// a prepare-describe request can reference it. JTOpen calls syncRPB() first,
	// using fire-and-forget (no reply). We send Create RPB as a preamble together
	// with Prepare-Describe under one lock.
	creatRPBReq, err := buildCreateRPBRequest(statementHandle, statementName)
	if err != nil {
		return nil, nil, err
	}

	request, err := buildPrepareAndDescribeRequest(statementHandle, statementName, query)
	if err != nil {
		return nil, nil, err
	}
	envelope, payload, err := c.doRequestWithPreamble(ctx, creatRPBReq, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = c.cancelStatement(context.Background(), statementHandle)
		}
		return nil, nil, err
	}
	if envelope.hasError() {
		return nil, nil, newProtocolError("prepare", envelope)
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, nil, err
	}
	if info.SQLCode < 0 {
		message := "SQL execution failed"
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
		}
		return nil, nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
	}
	c.recordWarning(info.Warning)
	parameterFormat, err := parseParameterMarkerFormatFromPayload(payload)
	if err != nil {
		return nil, nil, err
	}
	return info, parameterFormat, nil
}

func (c *Conn) queryRowsForStatement(ctx context.Context, statementHandle uint16, statementName string) (driver.Rows, error) {
	cursorName := c.nextCursorName()

	meta, sqlca, err := c.openAndDescribe(ctx, statementHandle, statementName, cursorName, 0, nil, nil)
	if err != nil {
		return nil, err
	}
	c.recordWarning(sqlca.Warning)
	resultSetCount := 1
	if sqlca.ResultSetsCount > 0 {
		resultSetCount = sqlca.ResultSetsCount
	}
	if meta == nil {
		meta = &resultSetMeta{}
	}
	if len(meta.columns) == 0 && resultSetCount == 1 {
		return nil, fmt.Errorf("query did not return a result set")
	}
	if meta.RowSize == 0 {
		meta.RowSize = sumColumnLengths(meta.columns)
	}

	return &queryRows{
		conn:                 c,
		ctx:                  ctx,
		meta:                 meta,
		statementHandle:      statementHandle,
		statementName:        statementName,
		cursorName:           cursorName,
		fetchBufferSize:      256 * 1024,
		resultSetCount:       resultSetCount,
		resultSetIndex:       1,
		currentResultSetDone: len(meta.columns) == 0,
		scrollable:           c.cfg.ScrollableCursors,
		ownStatement:         false,
	}, nil
}

func (c *Conn) queryPrepared(ctx context.Context, statementHandle uint16, statementName string) (driver.Rows, error) {
	if strings.TrimSpace(statementName) == "" {
		return nil, driver.ErrBadConn
	}
	c.ClearWarnings()
	return c.queryRowsForStatement(ctx, statementHandle, statementName)
}

func (c *Conn) queryPreparedWithArgs(ctx context.Context, statementHandle uint16, statementName string, format *parameterMarkerFormat, args []driver.NamedValue) (driver.Rows, error) {
	if strings.TrimSpace(statementName) == "" {
		return nil, driver.ErrBadConn
	}
	bindings, err := buildParameterBindings(format, args)
	if err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return c.queryPrepared(ctx, statementHandle, statementName)
	}

	descriptorHandle := c.nextDescriptorHandle()
	changeRequest, err := buildChangeDescriptorRequest(statementHandle, descriptorHandle, format, bindings)
	if err != nil {
		return nil, err
	}
	defer func() {
		deleteRequest := buildDeleteDescriptorRequest(statementHandle, descriptorHandle)
		if envelope, _, err := c.doRequest(ctx, deleteRequest); err == nil && envelope.hasError() {
			_ = fmt.Errorf("delete descriptor request failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
		}
	}()

	envelope, payload, err := c.doRequest(ctx, changeRequest)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = c.cancelStatement(context.Background(), statementHandle)
		}
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("change descriptor", envelope)
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, err
	}
	if info.SQLCode < 0 {
		message := "SQL execution failed"
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
		}
		return nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
	}
	c.recordWarning(info.Warning)
	if err := c.writeLOBDataRequests(ctx, bindings); err != nil {
		return nil, err
	}

	return c.queryRowsForStatementWithBindings(ctx, statementHandle, statementName, descriptorHandle, format, bindings)
}

func (c *Conn) queryRowsForStatementWithBindings(ctx context.Context, statementHandle uint16, statementName string, descriptorHandle uint16, format *parameterMarkerFormat, bindings []parameterBinding) (driver.Rows, error) {
	cursorName := c.nextCursorName()
	meta, sqlca, err := c.openAndDescribe(ctx, statementHandle, statementName, cursorName, descriptorHandle, format, bindings)
	if err != nil {
		return nil, err
	}
	c.recordWarning(sqlca.Warning)
	resultSetCount := 1
	if sqlca.ResultSetsCount > 0 {
		resultSetCount = sqlca.ResultSetsCount
	}
	if meta == nil {
		meta = &resultSetMeta{}
	}
	if len(meta.columns) == 0 && resultSetCount == 1 {
		return nil, fmt.Errorf("query did not return a result set")
	}
	if meta.RowSize == 0 {
		meta.RowSize = sumColumnLengths(meta.columns)
	}

	return &queryRows{
		conn:                 c,
		ctx:                  ctx,
		meta:                 meta,
		statementHandle:      statementHandle,
		statementName:        statementName,
		cursorName:           cursorName,
		fetchBufferSize:      256 * 1024,
		resultSetCount:       resultSetCount,
		resultSetIndex:       1,
		currentResultSetDone: len(meta.columns) == 0,
		scrollable:           c.cfg.ScrollableCursors,
		ownStatement:         false,
	}, nil
}

func (c *Conn) openAndDescribe(ctx context.Context, statementHandle uint16, statementName, cursorName string, descriptorHandle uint16, format *parameterMarkerFormat, bindings []parameterBinding) (*resultSetMeta, *sqlcaInfo, error) {
	request, err := buildOpenDescribePreparedRequest(statementHandle, descriptorHandle, statementName, cursorName, format, bindings, c.cfg.ScrollableCursors, c.cfg.HoldCursors)
	if err != nil {
		return nil, nil, err
	}
	envelope, payload, err := c.doStatementRequest(ctx, request, statementHandle)
	if err != nil {
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && !c.IsValid() {
			// Connection was killed by deadline-based fallback; notify IBM i to
			// stop the statement (native-cancel path already did so).
			_ = c.cancelStatement(context.Background(), statementHandle)
		}
		return nil, nil, err
	}
	if envelope.hasError() {
		return nil, nil, newProtocolError("open", envelope)
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, nil, err
	}
	if info.SQLCode < 0 {
		message := "SQL execution failed"
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
		}
		return nil, nil, &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message}
	}
	meta, err := parseDescribePayload(payload)
	if err != nil {
		return nil, nil, err
	}
	return meta, info, nil
}

func (c *Conn) fetchRows(ctx context.Context, statementHandle uint16, cursorName string, fetchBufferSize uint32, option int, relative int) (*fetchBlock, error) {
	request, err := buildFetchRequest(cursorName, fetchBufferSize, option, relative)
	if err != nil {
		return nil, err
	}
	envelope, payload, err := c.doStatementRequest(ctx, request, statementHandle)
	if err != nil {
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && !c.IsValid() {
			// Connection was killed by deadline-based fallback; notify IBM i to
			// stop the statement (native-cancel path already did so).
			_ = c.cancelStatement(context.Background(), statementHandle)
		}
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("fetch", envelope)
	}
	return parseFetchPayload(payload)
}

func (c *Conn) closeCursor(ctx context.Context, cursorName string, reuseIndicator byte) error {
	if strings.TrimSpace(cursorName) == "" {
		return nil
	}
	request, err := buildCloseCursorRequest(cursorName, reuseIndicator)
	if err != nil {
		return err
	}
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("close cursor", envelope)
	}
	return nil
}

func (c *Conn) closePreparedStatement(ctx context.Context, statementName string) error {
	if strings.TrimSpace(statementName) == "" {
		return nil
	}
	request, err := buildClosePreparedStatementRequest(statementName)
	if err != nil {
		return err
	}
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("close prepared statement", envelope)
	}
	return nil
}

func (r *queryRows) Columns() []string {
	if r == nil || r.meta == nil {
		return nil
	}
	return r.meta.columnNames()
}

func (r *queryRows) ColumnTypeDatabaseTypeName(index int) string {
	col, ok := r.columnMetaAt(index)
	if !ok {
		return ""
	}
	return col.databaseTypeName()
}

func (r *queryRows) ColumnTypeScanType(index int) reflect.Type {
	col, ok := r.columnMetaAt(index)
	if !ok {
		return nil
	}
	return columnScanType(col)
}

func (r *queryRows) ColumnTypeNullable(index int) (bool, bool) {
	col, ok := r.columnMetaAt(index)
	if !ok {
		return false, false
	}
	return col.nullable(), true
}

func (r *queryRows) ColumnTypeLength(index int) (int64, bool) {
	col, ok := r.columnMetaAt(index)
	if !ok {
		return 0, false
	}
	switch col.db2Type() {
	case db2TypeDate, db2TypeTime, db2TypeTimestamp,
		db2TypeChar, db2TypeVarchar, db2TypeLongVarchar,
		db2TypeVarGraphic, db2TypeGraphic, db2TypeLongVarGraphic,
		db2TypeDatalink, db2TypeBinary, db2TypeVarbinary, db2TypeRowID,
		db2TypeBlob, db2TypeClob, db2TypeDbclob, db2TypeXML,
		db2TypeBlobLocator, db2TypeClobLocator, db2TypeDbclobLocator, db2TypeXMLLocator:
		if col.LobMaxSize > 0 {
			return int64(col.LobMaxSize), true
		}
		if col.Length >= 0 {
			return int64(col.Length), true
		}
	}
	return 0, false
}

func (r *queryRows) ColumnTypePrecisionScale(index int) (int64, int64, bool) {
	col, ok := r.columnMetaAt(index)
	if !ok {
		return 0, 0, false
	}
	switch col.db2Type() {
	case db2TypeDecimal, db2TypeNumeric, db2TypeDecfloat:
		return int64(col.Precision), int64(col.Scale), true
	default:
		return 0, 0, false
	}
}

func (r *queryRows) columnMetaAt(index int) (columnMeta, bool) {
	if r == nil || r.meta == nil || index < 0 || index >= len(r.meta.columns) {
		return columnMeta{}, false
	}
	return r.meta.columns[index], true
}

func columnScanType(col columnMeta) reflect.Type {
	switch col.db2Type() {
	case db2TypeDate, db2TypeTime, db2TypeTimestamp:
		return reflect.TypeOf(time.Time{})
	case db2TypeChar, db2TypeVarchar, db2TypeLongVarchar,
		db2TypeVarGraphic, db2TypeGraphic, db2TypeLongVarGraphic,
		db2TypeDatalink:
		return reflect.TypeOf("")
	case db2TypeDecimal, db2TypeNumeric:
		return reflect.TypeOf("")
	case db2TypeFloatingPoint:
		return reflect.TypeOf(float64(0))
	case db2TypeBigint, db2TypeInteger, db2TypeSmallint:
		return reflect.TypeOf(int64(0))
	case db2TypeDecfloat:
		return reflect.TypeOf("")
	case db2TypeBinary, db2TypeVarbinary, db2TypeRowID,
		db2TypeBlob, db2TypeClob, db2TypeDbclob, db2TypeXML,
		db2TypeBlobLocator, db2TypeClobLocator, db2TypeDbclobLocator,
		db2TypeXMLLocator:
		return reflect.TypeOf([]byte(nil))
	default:
		return reflect.TypeOf([]byte(nil))
	}
}

func (r *queryRows) Next(dest []driver.Value) error {
	if r == nil || r.closed {
		return io.EOF
	}
	if r.meta == nil {
		return io.EOF
	}
	if r.conn == nil {
		return io.EOF
	}
	if len(dest) != len(r.meta.columns) {
		return fmt.Errorf("destination column count mismatch: got %d want %d", len(dest), len(r.meta.columns))
	}
	if len(r.meta.columns) == 0 {
		if r.currentResultSetDone {
			return io.EOF
		}
		r.currentResultSetDone = true
		if r.resultSetCount <= 0 || r.resultSetIndex >= r.resultSetCount {
			_ = r.Close()
		}
		return io.EOF
	}
	if r.currentResultSetDone {
		return io.EOF
	}

	for {
		if r.block != nil && r.rowIndex < r.block.RowCount {
			if err := r.decodeCurrentRow(dest); err != nil {
				_ = r.Close()
				return err
			}
			r.rowIndex++
			r.currentRow++
			if r.rowIndex >= r.block.RowCount {
				r.block = nil
				r.rowIndex = 0
			}
			return nil
		}
		if r.block != nil && r.block.RowCount == 0 {
			r.currentResultSetDone = true
			if r.resultSetCount <= 0 || r.resultSetIndex >= r.resultSetCount {
				_ = r.Close()
			}
			return io.EOF
		}

		block, err := r.conn.fetchRows(r.ctx, r.statementHandle, r.cursorName, r.fetchBufferSize, fetchNext, 0)
		if err != nil {
			_ = r.Close()
			return err
		}
		if block == nil || block.RowCount == 0 {
			r.currentResultSetDone = true
			r.block = nil
			r.rowIndex = 0
			if r.resultSetCount <= 0 || r.resultSetIndex >= r.resultSetCount {
				_ = r.Close()
			}
			return io.EOF
		}
		r.block = block
		r.rowIndex = 0
	}
}

func (r *queryRows) scroll(option, relative int) error {
	if r == nil || r.closed {
		return io.EOF
	}
	if !r.scrollable {
		return unsupported("scrollable cursor")
	}
	block, err := r.conn.fetchRows(r.ctx, r.statementHandle, r.cursorName, r.fetchBufferSize, option, relative)
	if err != nil {
		_ = r.Close()
		return err
	}
	r.block = block
	r.rowIndex = 0
	switch option {
	case fetchBeforeFirst:
		r.currentRow = 0
	case fetchAfterLast:
		r.currentRow = -1
	case fetchFirst:
		r.currentRow = 0
	case fetchDirect:
		r.currentRow = relative - 1
	case fetchRelative:
		r.currentRow += relative
	}
	r.currentResultSetDone = block == nil || block.RowCount == 0
	return nil
}

func (r *queryRows) First() error       { return r.scroll(fetchFirst, 0) }
func (r *queryRows) Last() error        { return r.scroll(fetchLast, 0) }
func (r *queryRows) BeforeFirst() error { return r.scroll(fetchBeforeFirst, 0) }
func (r *queryRows) AfterLast() error   { return r.scroll(fetchAfterLast, 0) }
func (r *queryRows) Current() error     { return r.scroll(fetchCurrent, 0) }
func (r *queryRows) Absolute(row int) error {
	if row < 1 {
		return fmt.Errorf("absolute row must be positive: %d", row)
	}
	return r.scroll(fetchDirect, row)
}
func (r *queryRows) Relative(rows int) error { return r.scroll(fetchRelative, rows) }

func (r *queryRows) Row() int {
	if r == nil || r.closed {
		return 0
	}
	return r.currentRow
}

func (r *queryRows) HasNextResultSet() bool {
	if r == nil || r.closed {
		return false
	}
	if r.resultSetCount <= 0 {
		return false
	}
	return r.resultSetIndex < r.resultSetCount
}

func (r *queryRows) NextResultSet() error {
	if r == nil || r.closed {
		return io.EOF
	}
	if !r.HasNextResultSet() {
		if err := r.Close(); err != nil {
			return err
		}
		return io.EOF
	}
	if r.conn == nil {
		r.closed = true
		return driver.ErrBadConn
	}
	if err := r.conn.closeCursor(r.ctx, r.cursorName, cursorReuseResultSet); err != nil {
		r.closed = true
		return err
	}
	meta, sqlca, err := r.conn.openAndDescribe(r.ctx, r.statementHandle, r.statementName, r.cursorName, 0, nil, nil)
	if err != nil {
		r.closed = true
		return err
	}
	r.conn.recordWarning(sqlca.Warning)
	if meta == nil {
		meta = &resultSetMeta{}
	}
	if meta.RowSize == 0 {
		meta.RowSize = sumColumnLengths(meta.columns)
	}
	r.meta = meta
	r.block = nil
	r.rowIndex = 0
	r.currentResultSetDone = len(r.meta.columns) == 0
	r.resultSetIndex++
	return nil
}

func (r *queryRows) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	var closeErr error
	if r.conn != nil {
		closeErr = r.conn.closeCursor(r.ctx, r.cursorName, cursorReuseYes)
		if r.ownStatement && strings.TrimSpace(r.statementName) != "" {
			// Ignore the error: IBM i does not support an explicit
			// "close prepared statement" request (FunctionClose with a
			// statement name returns class=2 code=-103).  The server
			// cleans up the prepared statement when the connection ends.
			_ = r.conn.closePreparedStatement(r.ctx, r.statementName)
		}
	}
	if r.releaseStmt != nil {
		release := r.releaseStmt
		r.releaseStmt = nil
		release()
	}
	return closeErr
}

func (r *queryRows) decodeCurrentRow(dest []driver.Value) error {
	if r.block == nil {
		return io.EOF
	}
	rowOffset := r.rowIndex * r.block.RowSize
	rowEnd := rowOffset + r.block.RowSize
	if rowOffset < 0 || rowEnd > len(r.block.Data) {
		return fmt.Errorf("row offset out of range")
	}
	row := r.block.Data[rowOffset:rowEnd]
	for i := range r.meta.columns {
		if r.block.Nulls != nil && r.block.Nulls[r.rowIndex*len(r.meta.columns)+i] {
			dest[i] = nil
			continue
		}
		value, err := decodeColumnValueWithContext(r.ctx, r.conn, r.meta.columns[i], row)
		if err != nil {
			return err
		}
		dest[i] = value
	}
	return nil
}

// buildCreateRPBRequest builds a "Create RPB" (Request Parameter Block) request.
// JTOpen calls syncRPB() before every prepare; the server requires the RPB to
// exist before a FunctionPrepareDescribe request can reference it.
func buildCreateRPBRequest(statementHandle uint16, statementName string) ([]byte, error) {
	body := make([]byte, 0, 128)
	parms := 0

	nameAttr, err := appendNameAttribute(CodePointPrepareStatementName, statementName)
	if err != nil {
		return nil, err
	}
	body = append(body, nameAttr...)
	parms++

	// Include a cursor name; use a name derived from the statement handle so
	// the server has it even though we may not open a cursor for DML.
	cursorName := fmt.Sprintf("CRSR%08X", uint32(statementHandle))
	cursorAttr, err := appendNameAttribute(0x380B, cursorName)
	if err != nil {
		return nil, err
	}
	body = append(body, cursorAttr...)
	parms++

	length := 40 + len(body)
	// ORS bitmap = 0: no return data requested; reply is just class/returncode.
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionCreateRPB)
	request = appendRequestTemplate(request, 0, statementHandle, 0, parms)
	request = append(request, body...)
	return request, nil
}

func buildPrepareAndDescribeRequest(statementHandle uint16, statementName, query string) ([]byte, error) {
	body := make([]byte, 0, 256)
	parms := 0
	statementType := classifySQLStatement(query)

	nameAttr, err := appendNameAttribute(0x3806, statementName)
	if err != nil {
		return nil, err
	}
	body = append(body, nameAttr...)
	parms++

	if statementType == sqlStatementTypeSelect || statementType == sqlStatementTypeCall {
		body = appendShortAttribute(body, 0x3812, uint16(statementType))
		parms++
		body = appendByteAttribute(body, 0x3809, 0x80)
		parms++
	}

	extendedTextAttr, err := appendExtendedSQLTextAttribute(query)
	if err != nil {
		return nil, err
	}
	body = append(body, extendedTextAttr...)
	parms++

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionPrepareDescribe)
	templateBitmap := orsSendReplyImmediately | orsDataFormat | orsSQLCA | orsParameterMarkerFormat
	if statementType != sqlStatementTypeCall {
		templateBitmap |= orsExtendedColumnDescriptors
	}
	request = appendRequestTemplate(request, templateBitmap, statementHandle, 0, parms)
	request = append(request, body...)
	return request, nil
}

func buildOpenAndDescribeRequest(statementName, cursorName string) ([]byte, error) {
	body := make([]byte, 0, 256)
	parms := 0

	nameAttr, err := appendNameAttribute(0x3806, statementName)
	if err != nil {
		return nil, err
	}
	body = append(body, nameAttr...)
	parms++

	cursorAttr, err := appendNameAttribute(0x380B, cursorName)
	if err != nil {
		return nil, err
	}
	body = append(body, cursorAttr...)
	parms++

	body = appendByteAttribute(body, 0x3809, 0x80)
	parms++
	body = appendByteAttribute(body, 0x3833, 0xE8)
	parms++
	body = appendByteAttribute(body, 0x380A, 0xD5)
	parms++
	body = appendShortAttribute(body, 0x380D, 0)
	parms++

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionOpenDescribe)
	templateBitmap := orsSendReplyImmediately | orsDataFormat | orsSQLCA | orsReturnResultSetAttributes | orsExtendedColumnDescriptors
	request = appendRequestTemplate(request, templateBitmap, 0, 0, parms)
	request = append(request, body...)
	return request, nil
}

func buildFetchRequest(cursorName string, fetchBufferSize uint32, option int, relative int) ([]byte, error) {
	body := make([]byte, 0, 128)
	parms := 0

	cursorAttr, err := appendNameAttribute(0x380B, cursorName)
	if err != nil {
		return nil, err
	}
	body = append(body, cursorAttr...)
	parms++
	body = appendShortAttribute(body, CodePointFetchScrollOption, uint16(option))
	parms++
	if option == fetchRelative || option == fetchDirect {
		body = body[:len(body)-8]
		body = appendShortIntAttribute(body, CodePointFetchScrollOption, uint16(option), uint32(relative))
	}
	body = appendByteAttribute(body, 0x3833, 0xE8)
	parms++
	body = appendIntAttribute(body, 0x3834, fetchBufferSize)
	parms++

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionFetch)
	templateBitmap := orsSendReplyImmediately | orsResultData
	request = appendRequestTemplate(request, templateBitmap, 0, 0, parms)
	request = append(request, body...)
	return request, nil
}

func buildCloseCursorRequest(cursorName string, reuseIndicator byte) ([]byte, error) {
	body := make([]byte, 0, 64)
	parms := 0

	cursorAttr, err := appendNameAttribute(0x380B, cursorName)
	if err != nil {
		return nil, err
	}
	body = append(body, cursorAttr...)
	parms++
	body = appendByteAttribute(body, 0x3810, reuseIndicator)
	parms++

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionClose)
	templateBitmap := orsSendReplyImmediately
	request = appendRequestTemplate(request, templateBitmap, 0, 0, parms)
	request = append(request, body...)
	return request, nil
}

func buildClosePreparedStatementRequest(statementName string) ([]byte, error) {
	body := make([]byte, 0, 64)
	statementAttr, err := appendNameAttribute(CodePointPrepareStatementName, statementName)
	if err != nil {
		return nil, err
	}
	body = append(body, statementAttr...)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionClose)
	request = appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 1)
	request = append(request, body...)
	return request, nil
}

func buildTestConnectionRequest() []byte {
	request := buildHeader(40, 0, DatabaseServerID, 20, FunctionTestConnection)
	return appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 0)
}

func buildExecuteImmediateRequest(query string, statementType int) ([]byte, error) {
	body := make([]byte, 0, 256)
	parms := 0

	if statementType == sqlStatementTypeCall {
		body = appendShortAttribute(body, 0x3812, sqlStatementTypeCall)
		parms++
		body = appendByteAttribute(body, 0x3809, 0x80)
		parms++
		body = appendByteAttribute(body, 0x3808, 0)
		parms++
	}

	extendedTextAttr, err := appendExtendedSQLTextAttribute(query)
	if err != nil {
		return nil, err
	}
	body = append(body, extendedTextAttr...)
	parms++

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionExecuteImmediate)
	templateBitmap := orsSendReplyImmediately | orsSQLCA
	request = appendRequestTemplate(request, templateBitmap, 0, 0, parms)
	request = append(request, body...)
	return request, nil
}

func appendRequestTemplate(dst []byte, bitmap uint32, rpbHandle, pmHandle uint16, parmCount int) []byte {
	dst = appendU32(dst, bitmap)
	dst = appendU32(dst, 0)
	dst = appendU16(dst, 1)
	dst = appendU16(dst, 1)
	dst = appendU16(dst, 0)
	dst = appendU16(dst, rpbHandle)
	dst = appendU16(dst, pmHandle)
	dst = appendU16(dst, uint16(parmCount))
	return dst
}

func appendNameAttribute(codePoint uint16, name string) ([]byte, error) {
	encoded, err := EncodeEBCDIC37(name)
	if err != nil {
		return nil, err
	}
	attr := make([]byte, 0, 10+len(encoded))
	attr = appendU32(attr, uint32(10+len(encoded)))
	attr = appendU16(attr, codePoint)
	attr = appendU16(attr, 37)
	attr = appendU16(attr, uint16(len(encoded)))
	attr = appendBytes(attr, encoded)
	return attr, nil
}

func appendByteAttribute(dst []byte, codePoint uint16, value byte) []byte {
	dst = appendU32(dst, 7)
	dst = appendU16(dst, codePoint)
	return append(dst, value)
}

func appendShortAttribute(dst []byte, codePoint uint16, value uint16) []byte {
	dst = appendU32(dst, 8)
	dst = appendU16(dst, codePoint)
	dst = appendU16(dst, value)
	return dst
}

func appendIntAttribute(dst []byte, codePoint uint16, value uint32) []byte {
	dst = appendU32(dst, 10)
	dst = appendU16(dst, codePoint)
	dst = appendU32(dst, value)
	return dst
}

func appendShortIntAttribute(dst []byte, codePoint, value uint16, relative uint32) []byte {
	dst = appendU32(dst, 12)
	dst = appendU16(dst, codePoint)
	dst = appendU16(dst, value)
	return appendU32(dst, relative)
}

func appendExtendedSQLTextAttribute(query string) ([]byte, error) {
	encoded := EncodeUTF16BE(query)
	attr := make([]byte, 0, 12+len(encoded))
	attr = appendU32(attr, uint32(12+len(encoded)))
	attr = appendU16(attr, 0x3831)
	attr = appendU16(attr, 13488)
	attr = appendU32(attr, uint32(len(encoded)))
	attr = appendBytes(attr, encoded)
	return attr, nil
}

func parseDescribePayload(payload []byte) (*resultSetMeta, error) {
	meta := &resultSetMeta{}
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		switch codePoint {
		case 0x3811:
			if err := parseExtendedColumnDescriptors(cpPayload, meta); err != nil {
				return nil, err
			}
		case 0x3812:
			if err := parseSuperExtendedDataFormat(cpPayload, meta); err != nil {
				return nil, err
			}
		default:
			// Ignore warnings and metadata we do not need.
		}
		remaining = rest
	}
	if meta.RowSize == 0 && len(meta.columns) > 0 {
		meta.RowSize = sumColumnLengths(meta.columns)
	}
	return meta, nil
}

func parseExtendedColumnDescriptors(payload []byte, meta *resultSetMeta) error {
	r := newSliceReader(payload)
	numColumns64, err := r.readU32()
	if err != nil {
		return err
	}
	numColumns := int(numColumns64)
	if err := meta.ensureColumns(numColumns); err != nil {
		return err
	}
	if err := r.skip(6); err != nil {
		return err
	}
	offsets := make([]int, numColumns)
	lengths := make([]int, numColumns)
	for i := 0; i < numColumns; i++ {
		updateable, err := r.readU8()
		if err != nil {
			return err
		}
		searchable, err := r.readU8()
		if err != nil {
			return err
		}
		attributeBitmap, err := r.readU16()
		if err != nil {
			return err
		}
		offset, err := r.readU32()
		if err != nil {
			return err
		}
		length, err := r.readU32()
		if err != nil {
			return err
		}
		if err := r.skip(4); err != nil {
			return err
		}
		meta.columns[i].Updateable = int(updateable)
		meta.columns[i].Searchable = int(searchable)
		meta.columns[i].IsIdentity = attributeBitmap&0x8000 != 0
		meta.columns[i].IsAlwaysGenerated = attributeBitmap&0x4000 == 0
		meta.columns[i].IsPartOfAnyIndex = attributeBitmap&0x2000 != 0
		meta.columns[i].IsLoneUniqueIndex = attributeBitmap&0x1000 != 0
		meta.columns[i].IsPartOfUniqueIndex = attributeBitmap&0x0800 != 0
		meta.columns[i].IsExpression = attributeBitmap&0x0400 != 0
		meta.columns[i].IsPrimaryKey = attributeBitmap&0x0200 != 0
		meta.columns[i].IsNamed = attributeBitmap&0x0100 == 0
		meta.columns[i].IsRowID = attributeBitmap&0x0080 != 0
		meta.columns[i].IsRowChangeTimestamp = attributeBitmap&0x0040 != 0
		offsets[i] = int(offset)
		lengths[i] = int(length)
	}

	for i := 0; i < numColumns; i++ {
		if offsets[i] > r.pos {
			if err := r.skip(offsets[i] - r.pos); err != nil {
				return err
			}
		}
		sectionLength := lengths[i]
		sectionStart := r.pos
		for r.pos-sectionStart < sectionLength {
			if sectionLength-(r.pos-sectionStart) < 6 {
				if err := r.skip(sectionLength - (r.pos - sectionStart)); err != nil {
					return err
				}
				break
			}
			descriptorLength, err := r.readU32()
			if err != nil {
				return err
			}
			codePoint, err := r.readU16()
			if err != nil {
				return err
			}
			payloadLength := int(descriptorLength) - 6
			ccsid := 37
			if codePoint == 0x3902 {
				ccsidValue, err := r.readU16()
				if err != nil {
					return err
				}
				ccsid = int(ccsidValue)
				payloadLength -= 2
				if ccsid == 65535 {
					ccsid = 37
				}
			}
			textBytes, err := r.readBytes(payloadLength)
			if err != nil {
				return err
			}
			text, err := decodeTextByCCSID(textBytes, ccsid)
			if err != nil {
				return err
			}
			switch codePoint {
			case 0x3900:
				meta.columns[i].BaseColumnName = text
			case 0x3901:
				meta.columns[i].BaseTableName = text
			case 0x3902:
				meta.columns[i].Label = text
			case 0x3904:
				meta.columns[i].BaseSchemaName = text
			case 0x3905:
				meta.columns[i].SQLFromTable = text
			case 0x3906:
				meta.columns[i].SQLFromSchema = text
			}
		}
	}
	return nil
}

func parseSuperExtendedDataFormat(payload []byte, meta *resultSetMeta) error {
	r := newSliceReader(payload)
	if _, err := r.readU32(); err != nil {
		return err
	}
	numFields64, err := r.readU32()
	if err != nil {
		return err
	}
	numFields := int(numFields64)
	dateFormat, err := r.readU8()
	if err != nil {
		return err
	}
	timeFormat, err := r.readU8()
	if err != nil {
		return err
	}
	dateSeparator, err := r.readU8()
	if err != nil {
		return err
	}
	timeSeparator, err := r.readU8()
	if err != nil {
		return err
	}
	recordSize64, err := r.readU32()
	if err != nil {
		return err
	}
	meta.DateFormat = int(dateFormat)
	meta.TimeFormat = int(timeFormat)
	meta.DateSeparator = int(dateSeparator)
	meta.TimeSeparator = int(timeSeparator)
	meta.RowSize = int(recordSize64)

	if err := meta.ensureColumns(numFields); err != nil {
		if len(meta.columns) == 0 {
			meta.columns = make([]columnMeta, numFields)
		} else {
			return err
		}
	}

	variableLengths := make([]int, numFields)
	for i := 0; i < numFields; i++ {
		if _, err := r.readU16(); err != nil {
			return err
		}
		fieldType, err := r.readU16()
		if err != nil {
			return err
		}
		fieldLength, err := r.readU32()
		if err != nil {
			return err
		}
		fieldScale, err := r.readU16()
		if err != nil {
			return err
		}
		fieldPrecision, err := r.readU16()
		if err != nil {
			return err
		}
		fieldCCSID, err := r.readU16()
		if err != nil {
			return err
		}
		if _, err := r.readU8(); err != nil {
			return err
		}
		if _, err := r.readU16(); err != nil {
			return err
		}
		if _, err := r.readU32(); err != nil {
			return err
		}
		attributeBitmap, err := r.readU8()
		if err != nil {
			return err
		}
		if _, err := r.readU32(); err != nil {
			return err
		}
		lobMaxSize, err := r.readU32()
		if err != nil {
			return err
		}
		if _, err := r.readU16(); err != nil {
			return err
		}
		if _, err := r.readU32(); err != nil {
			return err
		}
		lengthOfVariableInformation, err := r.readU32()
		if err != nil {
			return err
		}
		if _, err := r.readU32(); err != nil {
			return err
		}
		if _, err := r.readU32(); err != nil {
			return err
		}

		meta.columns[i].Type = int(fieldType)
		meta.columns[i].Length = int(fieldLength)
		meta.columns[i].Scale = int(fieldScale)
		meta.columns[i].Precision = int(fieldPrecision)
		meta.columns[i].CCSID = int(fieldCCSID)
		meta.columns[i].LobMaxSize = int(lobMaxSize)
		meta.columns[i].IsNamed = attributeBitmap&0x80 != 0
		variableLengths[i] = int(lengthOfVariableInformation)
	}

	offset := 0
	for i := range meta.columns {
		meta.columns[i].Offset = offset
		offset += meta.columns[i].Length
	}
	if meta.RowSize == 0 {
		meta.RowSize = offset
	}

	for i := 0; i < numFields; i++ {
		remaining := variableLengths[i]
		for remaining > 0 {
			if remaining < 8 {
				if err := r.skip(remaining); err != nil {
					return err
				}
				break
			}
			fieldLL, err := r.readU32()
			if err != nil {
				return err
			}
			fieldCP, err := r.readU16()
			if err != nil {
				return err
			}
			fieldCCSID, err := r.readU16()
			if err != nil {
				return err
			}
			nameLength := int(fieldLL) - 8
			if nameLength < 0 {
				return fmt.Errorf("invalid variable field length: %d", fieldLL)
			}
			nameBytes, err := r.readBytes(nameLength)
			if err != nil {
				return err
			}
			name, err := decodeTextByCCSID(nameBytes, int(fieldCCSID))
			if err != nil {
				return err
			}
			switch fieldCP {
			case 0x3840:
				meta.columns[i].Name = name
			case 0x3841:
				meta.columns[i].UDTName = name
			}
			remaining -= int(fieldLL)
		}
	}

	return nil
}

func parseFetchPayload(payload []byte) (*fetchBlock, error) {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		switch codePoint {
		case 0x380E: // extended result data (V4R4+)
			return parseResultDataPayload(cpPayload)
		case 0x3806: // original result data (V4R3 and earlier, or server-selected)
			return parseOriginalResultDataPayload(cpPayload)
		}
		remaining = rest
	}
	return &fetchBlock{}, nil
}

// parseOriginalResultDataPayload parses a 0x3806 (original/basic) result data block.
// Header layout (14 bytes):
//
//	[0-3]  consistency token (4 bytes, skipped)
//	[4-7]  row count        (uint32)
//	[8-9]  column count     (uint16)
//	[10-11] indicator size  (uint16)
//	[12-13] row size        (uint16)  ← 2 bytes, no reserved gap
func parseOriginalResultDataPayload(payload []byte) (*fetchBlock, error) {
	r := newSliceReader(payload)
	if _, err := r.readU32(); err != nil { // consistency token
		return nil, err
	}
	rowCount64, err := r.readU32()
	if err != nil {
		return nil, err
	}
	columnCount64, err := r.readU16()
	if err != nil {
		return nil, err
	}
	indicatorSize64, err := r.readU16()
	if err != nil {
		return nil, err
	}
	rowSize64, err := r.readU16() // uint16, no reserved gap before it
	if err != nil {
		return nil, err
	}
	return parseResultDataBlock(r, int(rowCount64), int(columnCount64), int(indicatorSize64), int(rowSize64))
}

func parseResultDataPayload(payload []byte) (*fetchBlock, error) {
	r := newSliceReader(payload)
	if _, err := r.readU32(); err != nil {
		return nil, err
	}
	rowCount64, err := r.readU32()
	if err != nil {
		return nil, err
	}
	columnCount64, err := r.readU16()
	if err != nil {
		return nil, err
	}
	indicatorSize64, err := r.readU16()
	if err != nil {
		return nil, err
	}
	if _, err := r.readU32(); err != nil { // reserved
		return nil, err
	}
	rowSize64, err := r.readU32()
	if err != nil {
		return nil, err
	}
	return parseResultDataBlock(r, int(rowCount64), int(columnCount64), int(indicatorSize64), int(rowSize64))
}

func parseResultDataBlock(r *sliceReader, rowCount, columnCount, indicatorSize, rowSize int) (*fetchBlock, error) {
	block := &fetchBlock{
		RowCount:      rowCount,
		ColumnCount:   columnCount,
		IndicatorSize: indicatorSize,
		RowSize:       rowSize,
		Nulls:         make([]bool, rowCount*columnCount),
		Data:          make([]byte, rowCount*rowSize),
	}

	indicatorBuf := make([]byte, indicatorSize)
	var err error
	for row := 0; row < rowCount; row++ {
		for col := 0; col < columnCount; col++ {
			if indicatorSize > 0 {
				indicatorBuf, err = r.readBytes(indicatorSize)
				if err != nil {
					return nil, err
				}
				if indicatorSize >= 2 && binary.BigEndian.Uint16(indicatorBuf[:2]) == 0xFFFF {
					block.Nulls[row*columnCount+col] = true
				}
			}
		}
	}

	data, err := r.readBytes(rowCount * rowSize)
	if err != nil {
		return nil, err
	}
	copy(block.Data, data)
	return block, nil
}

func parseSQLCAFromPayload(payload []byte) (*sqlcaInfo, error) {
	remaining := payload
	var sqlcaBytes []byte
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		if codePoint == 0x3807 {
			sqlcaBytes = cpPayload
			break
		}
		remaining = rest
	}
	if len(sqlcaBytes) == 0 {
		return &sqlcaInfo{}, nil
	}
	if len(sqlcaBytes) < 136 {
		return nil, fmt.Errorf("sqlca too short: %d", len(sqlcaBytes))
	}
	info := &sqlcaInfo{
		SQLCode:         int32(binary.BigEndian.Uint32(sqlcaBytes[12:16])),
		UpdateCount:     int64(int32(binary.BigEndian.Uint32(sqlcaBytes[104:108]))),
		ResultSetsCount: int(int32(binary.BigEndian.Uint32(sqlcaBytes[100:104]))),
	}
	if state, err := decodeTextByCCSID(sqlcaBytes[131:136], 37); err == nil {
		info.SQLState = strings.TrimSpace(state)
	}
	if info.SQLCode > 0 {
		message := fmt.Sprintf("SQL warning %d", info.SQLCode)
		if info.SQLState != "" {
			message = fmt.Sprintf("SQL warning %d (%s)", info.SQLCode, info.SQLState)
		}
		info.Warning = &SQLWarning{Code: info.SQLCode, State: info.SQLState, Message: message}
	}
	generatedKeyBytes := 16
	if len(sqlcaBytes) >= 72+generatedKeyBytes {
		if key, err := decodePackedDecimalString(sqlcaBytes[72:72+generatedKeyBytes], 30, 0); err == nil {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64); err == nil {
				info.GeneratedKey = parsed
				info.HasGeneratedKey = true
			}
		}
	}
	return info, nil
}

func parseParameterMarkerFormatFromPayload(payload []byte) (*parameterMarkerFormat, error) {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		switch codePoint {
		case CodePointParameterMarkerFormat:
			format, err := parseOriginalParameterMarkerFormat(cpPayload)
			if format != nil {
				format.codePoint = CodePointParameterMarkerFormat
			}
			return format, err
		case CodePointExtendedParameterMarker:
			format, err := parseExtendedParameterMarkerFormat(cpPayload)
			if format != nil {
				format.codePoint = CodePointExtendedParameterMarker
			}
			return format, err
		case CodePointSuperExtendedParameterMarker:
			format, err := parseSuperExtendedParameterMarkerFormat(cpPayload)
			if format != nil {
				format.codePoint = CodePointSuperExtendedParameterMarker
			}
			return format, err
		}
		remaining = rest
	}
	return nil, nil
}

func parseOriginalParameterMarkerFormat(payload []byte) (*parameterMarkerFormat, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if len(payload) < 8 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	count := int(binary.BigEndian.Uint16(payload[4:6]))
	recordSize := int(binary.BigEndian.Uint16(payload[6:8]))
	format := &parameterMarkerFormat{RecordSize: recordSize}
	if count == 0 {
		return format, nil
	}
	if len(payload) < 8+count*54 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	format.Fields = make([]parameterMarkerField, count)
	for i := 0; i < count; i++ {
		base := 8 + i*54
		field := parameterMarkerField{
			SQLType:       int(int16(binary.BigEndian.Uint16(payload[base+2 : base+4]))),
			Length:        int(int16(binary.BigEndian.Uint16(payload[base+4 : base+6]))),
			Scale:         int(int16(binary.BigEndian.Uint16(payload[base+6 : base+8]))),
			Precision:     int(int16(binary.BigEndian.Uint16(payload[base+8 : base+10]))),
			CCSID:         int(binary.BigEndian.Uint16(payload[base+10 : base+12])),
			ParameterType: int(payload[base+12]),
			LOBLocator:    -1,
			LOBMaxSize:    -1,
		}
		nameLength := int(binary.BigEndian.Uint16(payload[base+28 : base+30]))
		nameCCSID := int(binary.BigEndian.Uint16(payload[base+30 : base+32]))
		if nameLength > 0 && base+32+nameLength <= len(payload) {
			if name, err := decodeTextByCCSID(payload[base+32:base+32+nameLength], nameCCSID); err == nil {
				field.Name = strings.TrimSpace(name)
			}
		}
		format.Fields[i] = field
	}
	return format, nil
}

func parseExtendedParameterMarkerFormat(payload []byte) (*parameterMarkerFormat, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if len(payload) < 16 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	recordSize := int(binary.BigEndian.Uint32(payload[12:16]))
	format := &parameterMarkerFormat{RecordSize: recordSize}
	if count == 0 {
		return format, nil
	}
	if len(payload) < 16+count*64 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	format.Fields = make([]parameterMarkerField, count)
	for i := 0; i < count; i++ {
		base := 16 + i*64
		field := parameterMarkerField{
			SQLType:       int(int16(binary.BigEndian.Uint16(payload[base+2 : base+4]))),
			Length:        int(binary.BigEndian.Uint32(payload[base+4 : base+8])),
			Scale:         int(int16(binary.BigEndian.Uint16(payload[base+8 : base+10]))),
			Precision:     int(int16(binary.BigEndian.Uint16(payload[base+10 : base+12]))),
			CCSID:         int(binary.BigEndian.Uint16(payload[base+12 : base+14])),
			ParameterType: int(payload[base+14]),
			LOBLocator:    int(binary.BigEndian.Uint32(payload[base+17 : base+21])),
			LOBMaxSize:    int(binary.BigEndian.Uint32(payload[base+26 : base+30])),
		}
		nameLength := int(binary.BigEndian.Uint16(payload[base+30 : base+32]))
		nameCCSID := int(binary.BigEndian.Uint16(payload[base+32 : base+34]))
		if nameLength > 0 && base+34+nameLength <= len(payload) {
			if name, err := decodeTextByCCSID(payload[base+34:base+34+nameLength], nameCCSID); err == nil {
				field.Name = strings.TrimSpace(name)
			}
		}
		format.Fields[i] = field
	}
	return format, nil
}

func parseSuperExtendedParameterMarkerFormat(payload []byte) (*parameterMarkerFormat, error) {
	if len(payload) == 0 {
		return nil, nil
	}
	if len(payload) < 16 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	count := int(binary.BigEndian.Uint32(payload[4:8]))
	recordSize := int(binary.BigEndian.Uint32(payload[12:16]))
	format := &parameterMarkerFormat{RecordSize: recordSize}
	if count == 0 {
		return format, nil
	}
	if len(payload) < 16+count*48 {
		return nil, fmt.Errorf("parameter marker format too short: %d", len(payload))
	}
	format.Fields = make([]parameterMarkerField, count)
	for i := 0; i < count; i++ {
		base := 16 + i*48
		field := parameterMarkerField{
			SQLType:       int(int16(binary.BigEndian.Uint16(payload[base+2 : base+4]))),
			Length:        int(binary.BigEndian.Uint32(payload[base+4 : base+8])),
			Scale:         int(int16(binary.BigEndian.Uint16(payload[base+8 : base+10]))),
			Precision:     int(int16(binary.BigEndian.Uint16(payload[base+10 : base+12]))),
			CCSID:         int(binary.BigEndian.Uint16(payload[base+12 : base+14])),
			ParameterType: int(payload[base+14]),
			LOBLocator:    int(binary.BigEndian.Uint32(payload[base+17 : base+21])),
			LOBMaxSize:    int(binary.BigEndian.Uint32(payload[base+26 : base+30])),
		}
		format.Fields[i] = field
	}
	return format, nil
}

func decodeColumnValue(col columnMeta, row []byte) (driver.Value, error) {
	if len(row) < col.Offset {
		return nil, fmt.Errorf("row offset out of range")
	}
	offset := col.Offset
	length := col.Length
	if offset+length > len(row) {
		if offset > len(row) {
			return nil, fmt.Errorf("column offset out of range")
		}
		length = len(row) - offset
	}
	raw := row[offset : offset+length]
	switch col.db2Type() {
	case db2TypeDate:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err != nil {
			return nil, err
		}
		s := strings.TrimSpace(text)
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.UTC(), nil
		}
		return s, nil
	case db2TypeTime:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err != nil {
			return nil, err
		}
		s := strings.TrimSpace(text)
		if t, err := time.Parse("15.04.05", s); err == nil {
			return t.UTC(), nil
		}
		return s, nil
	case db2TypeTimestamp:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err != nil {
			return nil, err
		}
		s := strings.TrimSpace(text)
		if t, err := parseIBMiTimestamp(s); err == nil {
			return t.UTC(), nil
		}
		return s, nil
	case db2TypeChar:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err != nil {
			return nil, err
		}
		return text, nil
	case db2TypeVarchar, db2TypeLongVarchar, db2TypeDatalink:
		if len(raw) < 2 {
			return "", nil
		}
		valueLen := int(binary.BigEndian.Uint16(raw[:2]))
		if valueLen > len(raw)-2 {
			valueLen = len(raw) - 2
		}
		text, err := decodeTextByCCSID(raw[2:2+valueLen], col.CCSID)
		if err != nil {
			return nil, err
		}
		return text, nil
	case db2TypeVarGraphic, db2TypeLongVarGraphic:
		if len(raw) < 2 {
			return "", nil
		}
		valueLen := int(binary.BigEndian.Uint16(raw[:2])) * 2
		if valueLen > len(raw)-2 {
			valueLen = len(raw) - 2
		}
		text, err := decodeTextByCCSID(raw[2:2+valueLen], col.CCSID)
		if err != nil {
			return nil, err
		}
		return text, nil
	case db2TypeGraphic:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err != nil {
			return nil, err
		}
		return text, nil
	case db2TypeDecimal:
		return decodePackedDecimalString(raw, col.Precision, col.Scale)
	case db2TypeNumeric:
		return decodeZonedDecimalString(raw, col.Precision, col.Scale)
	case db2TypeFloatingPoint:
		if len(raw) == 4 {
			return float64(math.Float32frombits(binary.BigEndian.Uint32(raw))), nil
		}
		if len(raw) >= 8 {
			return math.Float64frombits(binary.BigEndian.Uint64(raw[:8])), nil
		}
		return nil, fmt.Errorf("floating point column too short")
	case db2TypeBigint:
		if len(raw) < 8 {
			return nil, fmt.Errorf("bigint column too short")
		}
		return int64(binary.BigEndian.Uint64(raw[:8])), nil
	case db2TypeInteger:
		if len(raw) < 4 {
			return nil, fmt.Errorf("integer column too short")
		}
		return int64(int32(binary.BigEndian.Uint32(raw[:4]))), nil
	case db2TypeSmallint:
		if len(raw) < 2 {
			return nil, fmt.Errorf("smallint column too short")
		}
		return int64(int16(binary.BigEndian.Uint16(raw[:2]))), nil
	case db2TypeBinary, db2TypeVarbinary, db2TypeRowID, db2TypeBlobLocator, db2TypeClobLocator, db2TypeDbclobLocator, db2TypeXMLLocator:
		if col.db2Type() == db2TypeVarbinary || col.db2Type() == db2TypeRowID {
			if len(raw) < 2 {
				return []byte(nil), nil
			}
			valueLen := int(binary.BigEndian.Uint16(raw[:2]))
			if valueLen > len(raw)-2 {
				valueLen = len(raw) - 2
			}
			out := make([]byte, valueLen)
			copy(out, raw[2:2+valueLen])
			return out, nil
		}
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, nil
	case db2TypeDecfloat:
		switch len(raw) {
		case 8:
			return decodeDecfloat16(raw)
		case 16:
			return decodeDecfloat34(raw)
		default:
			return nil, fmt.Errorf("unexpected DECFLOAT length %d", len(raw))
		}
	case db2TypeBlob, db2TypeClob, db2TypeDbclob, db2TypeXML:
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, nil
	default:
		text, err := decodeTextByCCSID(raw, col.CCSID)
		if err == nil {
			return text, nil
		}
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, nil
	}
}

func decodeColumnValueWithContext(ctx context.Context, conn *Conn, col columnMeta, row []byte) (driver.Value, error) {
	value, err := decodeColumnValue(col, row)
	if err != nil {
		return nil, err
	}
	return resolveLobLocatorValue(ctx, conn, col, value)
}

func resolveLobLocatorValue(ctx context.Context, conn *Conn, col columnMeta, value driver.Value) (driver.Value, error) {
	if conn == nil {
		return value, nil
	}
	switch col.db2Type() {
	case db2TypeBlobLocator, db2TypeClobLocator, db2TypeDbclobLocator, db2TypeXMLLocator:
	default:
		return value, nil
	}
	raw, ok := value.([]byte)
	if !ok || len(raw) < 4 {
		return value, nil
	}
	locatorHandle := binary.BigEndian.Uint32(raw[:4])
	if locatorHandle == 0 {
		return []byte(nil), nil
	}
	data, err := conn.retrieveLobData(ctx, locatorHandle)
	if freeErr := conn.freeLob(ctx, locatorHandle); err == nil && freeErr != nil {
		err = freeErr
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func decodeTextByCCSID(data []byte, ccsid int) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	switch ccsid {
	case 0, 37, 273, 65535:
		return DecodeEBCDIC37(data)
	case 1200, 13488:
		return DecodeUTF16BE(data)
	case 1208:
		return string(data), nil
	default:
		return "", fmt.Errorf("%w: unsupported CCSID %d", ErrUnsupported, ccsid)
	}
}

// decfloatUnpackDeclet decodes a 10-bit Densely Packed Decimal (DPD) declet
// into 3 decimal digits (returned as an integer 0–999).
func decfloatUnpackDeclet(bits int) int {
	var combination int
	if (bits & 0xe) == 0xe {
		combination = ((bits & 0x60) >> 5) | 4
	} else {
		if (bits & 0x8) == 0x8 {
			combination = ((^bits) & 0x6) >> 1
		}
	}
	var decoded int
	switch combination {
	case 0:
		decoded = ((bits & 0x380) << 1) | (bits & 0x77)
	case 1:
		decoded = ((bits & 0x80) << 1) | (bits & 0x71) | ((bits & 0x300) >> 7) | 0x800
	case 2:
		decoded = ((bits & 0x380) << 1) | (bits & 0x11) | ((bits & 0x60) >> 4) | 0x80
	case 3:
		decoded = ((bits & 0x380) << 1) | (bits & 0x71) | 0x8
	case 4:
		decoded = ((bits & 0x80) << 1) | (bits & 0x11) | ((bits & 0x300) >> 7) | 0x880
	case 5:
		decoded = ((bits & 0x80) << 1) | (bits & 0x11) | ((bits & 0x300) >> 3) | 0x808
	case 6:
		decoded = ((bits & 0x380) << 1) | (bits & 0x11) | 0x88
	case 7:
		decoded = ((bits & 0x80) << 1) | (bits & 0x11) | 0x888
	}
	return ((decoded&0xf00)>>8)*100 + ((decoded&0xf0)>>4)*10 + (decoded & 0xf)
}

// decfloatBitsToDigits converts 30 bits (three 10-bit DPD declets) to 9 decimal digits.
func decfloatBitsToDigits(bits int) int {
	d := 0
	for i := 2; i >= 0; i-- {
		d = d*1000 + decfloatUnpackDeclet((bits>>(i*10))&0x3ff)
	}
	return d
}

const (
	decfloat16Bias     = 398
	decfloat34Bias     = 6176
	decfloat16SignMask = int64(-0x8000000000000000) // 0x8000000000000000 as signed
	decfloat16CombMask = int64(0x7c00000000000000)
	decfloat16ExpCMask = int64(0x03fc000000000000)
	decfloat16CoefMask = int64(0x0003ffffffffffff)
	decfloat16SigMask  = int64(0x0200000000000000)
)

// decodeDecfloat16 decodes 8 bytes of IEEE 754 Decimal64 (DECFLOAT(16)) to a decimal string.
func decodeDecfloat16(data []byte) (string, error) {
	if len(data) < 8 {
		return "", fmt.Errorf("DECFLOAT(16) requires 8 bytes, got %d", len(data))
	}
	bits := int64(binary.BigEndian.Uint64(data[:8]))

	signBit := (bits >> 63) & 1
	sign := 1
	if signBit != 0 {
		sign = -1
	}
	combination := (bits >> 58) & 0x1f

	// Special values
	if combination == 0x1f {
		if (bits & int64(0x0200000000000000)) != 0 {
			if sign < 0 {
				return "-SNaN", nil
			}
			return "SNaN", nil
		}
		if sign < 0 {
			return "-NaN", nil
		}
		return "NaN", nil
	}
	if combination == 0x1e {
		if sign < 0 {
			return "-Infinity", nil
		}
		return "Infinity", nil
	}

	var exponentMSD, coefficientMSD int
	if (combination & 0x18) == 0x18 {
		exponentMSD = int((combination & 0x06) >> 1)
		coefficientMSD = int(8 + (combination & 0x01))
	} else {
		exponentMSD = int((combination & 0x18) >> 3)
		coefficientMSD = int(combination & 0x07)
	}

	exponent := int((bits & int64(0x03fc000000000000)) >> 50)
	exponent |= exponentMSD << 8
	exponent -= decfloat16Bias

	coefCont := bits & int64(0x0003ffffffffffff)
	coefficientLo := decfloatBitsToDigits(int(coefCont & 0x3fffffff))
	coefficientHi := decfloatBitsToDigits(int((coefCont >> 30) & 0xfffff))
	coefficientHi += coefficientMSD * 1000000

	// coefficientHi holds digits 1–7, coefficientLo holds digits 8–16.
	// Represent as a 16-digit integer: coefficientHi * 10^9 + coefficientLo
	coefVal := int64(coefficientHi)*1000000000 + int64(coefficientLo)

	return formatDecfloat(sign, coefVal, exponent, 16), nil
}

// decodeDecfloat34 decodes 16 bytes of IEEE 754 Decimal128 (DECFLOAT(34)) to a decimal string.
func decodeDecfloat34(data []byte) (string, error) {
	if len(data) < 16 {
		return "", fmt.Errorf("DECFLOAT(34) requires 16 bytes, got %d", len(data))
	}
	hi := int64(binary.BigEndian.Uint64(data[:8]))
	lo := int64(binary.BigEndian.Uint64(data[8:16]))

	signBit := (hi >> 63) & 1
	sign := 1
	if signBit != 0 {
		sign = -1
	}
	combination := (hi >> 58) & 0x1f

	if combination == 0x1f {
		if (hi & int64(0x0200000000000000)) != 0 {
			if sign < 0 {
				return "-SNaN", nil
			}
			return "SNaN", nil
		}
		if sign < 0 {
			return "-NaN", nil
		}
		return "NaN", nil
	}
	if combination == 0x1e {
		if sign < 0 {
			return "-Infinity", nil
		}
		return "Infinity", nil
	}

	var exponentMSD, coefficientMSD int
	if (combination & 0x18) == 0x18 {
		exponentMSD = int((combination & 0x06) >> 1)
		coefficientMSD = int(8 + (combination & 0x01))
	} else {
		exponentMSD = int((combination & 0x18) >> 3)
		coefficientMSD = int(combination & 0x07)
	}

	exponent := int((hi & int64(0x03ffc00000000000)) >> 46)
	exponent |= exponentMSD << 12
	exponent -= decfloat34Bias

	coefLo := decfloatBitsToDigits(int(lo & 0x3fffffff))
	coefMeLo := decfloatBitsToDigits(int((lo >> 30) & 0x3fffffff))
	coefMeHi := decfloatBitsToDigits(int(((hi & 0x3ffffff) << 4) | ((lo >> 60) & 0xf)))
	coefHi := decfloatBitsToDigits(int((hi >> 26) & 0xfffff))
	coefHi += coefficientMSD * 1000000

	// Combine the four 9-digit groups into a string without big.Int.
	// coefHi: digits 1–7 (max 9 999 999), coefMeHi/coefMeLo/coefLo: 9 digits each.
	sig := fmt.Sprintf("%d%09d%09d%09d", coefHi, coefMeHi, coefMeLo, coefLo)
	// trim leading zeros but keep at least one digit
	trimmed := strings.TrimLeft(sig, "0")
	if trimmed == "" {
		trimmed = "0"
	}
	coefStr := trimmed

	return formatDecfloatStr(sign, coefStr, exponent), nil
}

// formatDecfloat builds a plain decimal string from sign, integer coefficient, and exponent.
// precision is used only to strip leading zeros.
func formatDecfloat(sign int, coef int64, exponent, _ int) string {
	var digits string
	if coef == 0 {
		digits = "0"
	} else {
		digits = strings.TrimLeft(fmt.Sprintf("%d", coef), "0")
	}
	return formatDecfloatStr(sign, digits, exponent)
}

// formatDecfloatStr builds a plain decimal string from sign, digit string, and base-10 exponent.
// exponent is the power of 10 applied after the implicit decimal point at position 0 of coefDigits.
// e.g. coefDigits="123", exponent=-2 → "1.23"; exponent=1 → "1230"
func formatDecfloatStr(sign int, coefDigits string, exponent int) string {
	n := len(coefDigits) // number of significant digits

	// The actual value is: coefDigits * 10^exponent
	// Position of decimal point from the right of coefDigits:
	//   scale = -exponent (if exponent < 0)
	scale := -exponent // number of fractional digits

	var result string
	if scale <= 0 {
		// Pure integer (possibly with trailing zeros)
		trailingZeros := strings.Repeat("0", -scale)
		result = coefDigits + trailingZeros
	} else if scale >= n {
		// All digits are fractional, needs leading "0."
		leadingZeros := strings.Repeat("0", scale-n)
		result = "0." + leadingZeros + coefDigits
	} else {
		// Decimal point is somewhere inside the digit string
		intPart := coefDigits[:n-scale]
		fracPart := coefDigits[n-scale:]
		result = intPart + "." + fracPart
	}

	if sign < 0 && result != "0" {
		return "-" + result
	}
	return result
}

func decodePackedDecimalString(data []byte, precision, scale int) (string, error) {
	if len(data) == 0 {
		return "0", nil
	}
	if precision < 0 {
		return "", fmt.Errorf("invalid packed decimal precision %d", precision)
	}
	digits := make([]byte, 0, precision+1)
	sign := byte(0x0C)
	for i, b := range data {
		hi := (b >> 4) & 0x0F
		lo := b & 0x0F
		if i < len(data)-1 {
			digits = append(digits, digitNibble(hi), digitNibble(lo))
		} else {
			digits = append(digits, digitNibble(hi))
			sign = lo
		}
	}
	if len(digits) > precision {
		digits = digits[len(digits)-precision:]
	}
	if len(digits) < precision {
		pad := make([]byte, precision-len(digits))
		for i := range pad {
			pad[i] = '0'
		}
		digits = append(pad, digits...)
	}
	if scale > 0 {
		if scale >= len(digits) {
			pad := make([]byte, scale-len(digits)+1)
			for i := range pad {
				pad[i] = '0'
			}
			digits = append(pad, digits...)
		}
		point := len(digits) - scale
		digits = append(digits[:point], append([]byte{'.'}, digits[point:]...)...)
	}
	prefix := ""
	if sign == 0x0D {
		prefix = "-"
	}
	return prefix + string(digits), nil
}

func decodeZonedDecimalString(data []byte, precision, scale int) (string, error) {
	if len(data) == 0 {
		return "0", nil
	}
	digits := make([]byte, 0, len(data))
	sign := byte(0x0C)
	for i, b := range data {
		if i == len(data)-1 {
			sign = (b >> 4) & 0x0F
		}
		digits = append(digits, '0'+(b&0x0F))
	}
	if len(digits) > precision {
		digits = digits[len(digits)-precision:]
	}
	if len(digits) < precision {
		pad := make([]byte, precision-len(digits))
		for i := range pad {
			pad[i] = '0'
		}
		digits = append(pad, digits...)
	}
	if scale > 0 {
		if scale >= len(digits) {
			pad := make([]byte, scale-len(digits)+1)
			for i := range pad {
				pad[i] = '0'
			}
			digits = append(pad, digits...)
		}
		point := len(digits) - scale
		digits = append(digits[:point], append([]byte{'.'}, digits[point:]...)...)
	}
	prefix := ""
	if sign == 0x0D {
		prefix = "-"
	}
	return prefix + string(digits), nil
}

func digitNibble(nibble byte) byte {
	if nibble > 9 {
		return '0'
	}
	return '0' + nibble
}

func sumColumnLengths(columns []columnMeta) int {
	total := 0
	for i := range columns {
		total += columns[i].Length
	}
	return total
}

var errPreparedStatementBindingUnsupported = errors.New("prepared statement parameter binding unsupported")

func bindingError(parameter int, operation string, err error) error {
	if err == nil {
		return nil
	}
	return &BindingError{Parameter: parameter + 1, Operation: operation, Err: err}
}

type parameterBinding struct {
	field    parameterMarkerField
	raw      []byte
	isNull   bool
	outDest  any
	lobWrite *lobWriteRequest
}

type lobWriteRequest struct {
	locatorHandle uint32
	requestedSize int
	data          []byte
}

func buildParameterBindings(format *parameterMarkerFormat, args []driver.NamedValue) ([]parameterBinding, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if format == nil || len(format.Fields) == 0 {
		return nil, errPreparedStatementBindingUnsupported
	}
	if len(args) != len(format.Fields) {
		return nil, fmt.Errorf("parameter count mismatch: got %d values for %d markers", len(args), len(format.Fields))
	}

	bindings := make([]parameterBinding, len(format.Fields))
	for i := range format.Fields {
		field := format.Fields[i]
		parameterType := byte(field.ParameterType & 0xFF)
		if parameterType != 0x00 && parameterType != 0xF0 && parameterType != 0xF1 && parameterType != 0xF2 {
			return nil, bindingError(i, "parameter mode", errPreparedStatementBindingUnsupported)
		}

		argValue := args[i].Value
		var outDest any
		isNull := false
		hasInputValue := true
		if out, ok := argValue.(sql.Out); ok {
			if out.Dest == nil {
				return nil, bindingError(i, "sql.Out", fmt.Errorf("sql.Out destination must not be nil"))
			}
			if reflect.ValueOf(out.Dest).Kind() != reflect.Ptr {
				return nil, bindingError(i, "sql.Out", fmt.Errorf("sql.Out destination must be a pointer"))
			}
			outDest = out.Dest
			switch parameterType {
			case 0xF0:
				return nil, bindingError(i, "sql.Out mode", errPreparedStatementBindingUnsupported)
			case 0xF1:
				argValue = nil
				hasInputValue = false
			case 0xF2:
				if out.In {
					destValue := reflect.ValueOf(out.Dest)
					if destValue.IsNil() {
						return nil, bindingError(i, "sql.Out", fmt.Errorf("sql.Out destination must not be nil"))
					}
					argValue = destValue.Elem().Interface()
				} else {
					argValue = nil
					isNull = true
					hasInputValue = false
				}
			}
		}

		if isLOBParameterField(field) {
			raw, lobWrite, err := encodeLOBParameterValue(field, argValue, hasInputValue, isNull)
			if err != nil {
				return nil, bindingError(i, "LOB parameter", err)
			}
			bindingNull := isNull || (hasInputValue && argValue == nil)
			bindings[i] = parameterBinding{field: field, raw: raw, isNull: bindingNull, outDest: outDest, lobWrite: lobWrite}
			continue
		}

		var normalized driver.Value
		var err error
		if hasInputValue && !isNull {
			normalized, err = driver.DefaultParameterConverter.ConvertValue(argValue)
			if err != nil {
				return nil, bindingError(i, "parameter conversion", err)
			}
		}
		raw, err := encodeParameterValue(field, normalized, isNull || normalized == nil)
		if err != nil {
			return nil, bindingError(i, "parameter encoding", err)
		}
		bindingNull := isNull || normalized == nil
		if !hasInputValue {
			bindingNull = false
		}
		bindings[i] = parameterBinding{field: field, raw: raw, isNull: bindingNull, outDest: outDest}
	}

	return bindings, nil
}

func isLOBParameterField(field parameterMarkerField) bool {
	switch parameterFieldDB2Type(field) {
	case db2TypeBlob, db2TypeClob, db2TypeDbclob, db2TypeXML:
		return true
	default:
		return false
	}
}

func encodeLOBParameterValue(field parameterMarkerField, value any, hasInputValue bool, isNull bool) ([]byte, *lobWriteRequest, error) {
	if field.LOBLocator <= 0 {
		return nil, nil, errPreparedStatementBindingUnsupported
	}
	raw := make([]byte, 4)
	binary.BigEndian.PutUint32(raw, uint32(field.LOBLocator))
	if !hasInputValue || isNull || value == nil {
		return raw, nil, nil
	}

	valueArg, ok := value.(driver.Value)
	if !ok {
		return nil, nil, errPreparedStatementBindingUnsupported
	}

	var data []byte
	var requestedSize int
	var err error
	switch parameterFieldDB2Type(field) {
	case db2TypeBlob:
		data, err = parameterFieldBinaryBytes(valueArg)
		requestedSize = len(data)
	case db2TypeClob, db2TypeXML:
		var text string
		text, err = parameterFieldValueString(valueArg)
		if err == nil {
			data, err = encodeTextBytes(text, field.CCSID)
			requestedSize = len(data)
		}
	case db2TypeDbclob:
		var text string
		text, err = parameterFieldValueString(valueArg)
		if err == nil {
			data, err = encodeTextBytes(text, field.CCSID)
			requestedSize = len(data) / 2
		}
	default:
		return nil, nil, errPreparedStatementBindingUnsupported
	}
	if err != nil {
		return nil, nil, err
	}
	return raw, &lobWriteRequest{locatorHandle: uint32(field.LOBLocator), requestedSize: requestedSize, data: data}, nil
}

func parameterFieldDB2Type(field parameterMarkerField) int {
	typ := field.SQLType & 0xFFFE
	if field.CCSID == 65535 {
		switch typ {
		case db2TypeChar:
			return db2TypeBinary
		case db2TypeVarchar, db2TypeLongVarchar:
			return db2TypeVarbinary
		}
	}
	return typ
}

func parameterFieldValueString(value driver.Value) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case time.Time:
		return v.Format("2006-01-02T15:04:05.999999999"), nil
	default:
		return fmt.Sprint(v), nil
	}
}

func parameterFieldBinaryBytes(value driver.Value) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	case string:
		text := strings.TrimSpace(v)
		if strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X") {
			text = text[2:]
		}
		text = strings.ReplaceAll(text, " ", "")
		if len(text)%2 != 0 {
			return nil, fmt.Errorf("hex string must contain an even number of digits")
		}
		out, err := hex.DecodeString(text)
		if err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, errPreparedStatementBindingUnsupported
	}
}

func parameterFieldDecimalString(value driver.Value) (string, error) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), nil
	case []byte:
		return strings.TrimSpace(string(v)), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	default:
		return "", errPreparedStatementBindingUnsupported
	}
}

func encodeParameterValue(field parameterMarkerField, value driver.Value, isNull bool) ([]byte, error) {
	if isNull || value == nil {
		return make([]byte, parameterFieldNullLength(field)), nil
	}

	switch parameterFieldDB2Type(field) {
	case db2TypeBigint:
		return encodeBigintParameter(value, field)
	case db2TypeInteger:
		return encodeIntegerParameter(value, field)
	case db2TypeSmallint:
		return encodeSmallintParameter(value, field)
	case db2TypeFloatingPoint:
		return encodeFloatingPointParameter(value, field)
	case db2TypeDecimal:
		return encodePackedDecimalParameter(value, field)
	case db2TypeNumeric:
		return encodeZonedDecimalParameter(value, field)
	case db2TypeDate:
		return encodeDateParameter(value, field)
	case db2TypeTime:
		return encodeTimeParameter(value, field)
	case db2TypeTimestamp:
		return encodeTimestampParameter(value, field)
	case db2TypeBinary:
		return encodeFixedBinaryParameter(value, field)
	case db2TypeVarbinary, db2TypeRowID:
		return encodeVarbinaryParameter(value, field)
	case db2TypeChar:
		return encodeFixedTextParameter(value, field)
	case db2TypeVarchar, db2TypeLongVarchar, db2TypeDatalink:
		return encodeVarcharParameter(value, field)
	case db2TypeVarGraphic, db2TypeLongVarGraphic:
		return encodeVarGraphicParameter(value, field)
	case db2TypeGraphic:
		return encodeGraphicParameter(value, field)
	case db2TypeDecfloat:
		return encodeFixedTextParameter(value, field)
	case db2TypeBlob, db2TypeClob, db2TypeDbclob, db2TypeXML,
		db2TypeBlobLocator, db2TypeClobLocator, db2TypeDbclobLocator,
		db2TypeXMLLocator:
		return nil, errPreparedStatementBindingUnsupported
	default:
		return encodeFixedTextParameter(value, field)
	}
}

func parameterFieldNullLength(field parameterMarkerField) int {
	if field.Length > 0 {
		return field.Length
	}
	switch parameterFieldDB2Type(field) {
	case db2TypeBigint:
		return 8
	case db2TypeInteger:
		return 4
	case db2TypeSmallint:
		return 2
	case db2TypeFloatingPoint:
		if field.Length == 4 {
			return 4
		}
		return 8
	case db2TypeDecimal:
		if field.Precision > 0 {
			return (field.Precision + 2) / 2
		}
	case db2TypeNumeric:
		if field.Precision > 0 {
			return field.Precision
		}
	case db2TypeVarbinary, db2TypeRowID, db2TypeVarchar, db2TypeLongVarchar,
		db2TypeDatalink, db2TypeVarGraphic, db2TypeLongVarGraphic:
		return 0
	}
	return 0
}

func hasOutputBindings(bindings []parameterBinding) bool {
	for i := range bindings {
		if bindings[i].outDest != nil {
			return true
		}
	}
	return false
}

func isOutputParameterField(field parameterMarkerField) bool {
	switch byte(field.ParameterType & 0xFF) {
	case 0xF1, 0xF2:
		return true
	default:
		return false
	}
}

func assignCallOutputParameters(ctx context.Context, conn *Conn, payload []byte, bindings []parameterBinding) error {
	if !hasOutputBindings(bindings) {
		return nil
	}
	block, err := parseFetchPayload(payload)
	if err != nil {
		return err
	}
	if block == nil || block.RowCount == 0 || block.ColumnCount == 0 {
		return nil
	}
	outputIndexes := make([]int, 0, len(bindings))
	for i := range bindings {
		if isOutputParameterField(bindings[i].field) {
			outputIndexes = append(outputIndexes, i)
		}
	}
	if block.ColumnCount != len(outputIndexes) && block.ColumnCount != len(bindings) {
		return fmt.Errorf("unexpected output parameter column count: got %d want %d or %d", block.ColumnCount, len(outputIndexes), len(bindings))
	}
	fields := make([]parameterMarkerField, 0, block.ColumnCount)
	bindingIndexes := make([]int, 0, block.ColumnCount)
	if block.ColumnCount == len(outputIndexes) {
		for _, idx := range outputIndexes {
			fields = append(fields, bindings[idx].field)
			bindingIndexes = append(bindingIndexes, idx)
		}
	} else {
		for idx := range bindings {
			fields = append(fields, bindings[idx].field)
			bindingIndexes = append(bindingIndexes, idx)
		}
	}
	if block.RowSize > len(block.Data) {
		return fmt.Errorf("output parameter row size out of range")
	}
	row := block.Data[:block.RowSize]
	offset := 0
	for colIndex, bindingIndex := range bindingIndexes {
		field := fields[colIndex]
		length := parameterFieldResultLength(field)
		if offset+length > len(row) {
			return fmt.Errorf("output parameter row too short")
		}
		if bindings[bindingIndex].outDest != nil {
			col := parameterFieldResultColumnMeta(field, offset, length)
			var value driver.Value
			if block.Nulls != nil && colIndex < len(block.Nulls) && block.Nulls[colIndex] {
				value = nil
			} else {
				value, err = decodeColumnValueWithContext(ctx, conn, col, row)
				if err != nil {
					return err
				}
			}
			if err := assignOutDestination(bindings[bindingIndex].outDest, value); err != nil {
				return err
			}
		}
		offset += length
	}
	return nil
}

func parameterFieldResultLength(field parameterMarkerField) int {
	if isLOBParameterField(field) && field.LOBLocator > 0 {
		return 4
	}
	return parameterFieldNullLength(field)
}

func parameterFieldResultColumnMeta(field parameterMarkerField, offset, length int) columnMeta {
	colType := field.SQLType
	if field.LOBLocator > 0 {
		switch parameterFieldDB2Type(field) {
		case db2TypeBlob:
			colType = db2TypeBlobLocator
		case db2TypeClob:
			colType = db2TypeClobLocator
		case db2TypeDbclob:
			colType = db2TypeDbclobLocator
		case db2TypeXML:
			colType = db2TypeXMLLocator
		}
	}
	return columnMeta{Type: colType, Length: length, Scale: field.Scale, Precision: field.Precision, CCSID: field.CCSID, Offset: offset, LobMaxSize: field.LOBMaxSize}
}

func assignOutDestination(dest any, value driver.Value) error {
	if dest == nil {
		return nil
	}
	if scanner, ok := dest.(interface{ Scan(any) error }); ok {
		return scanner.Scan(value)
	}
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr || destValue.IsNil() {
		return fmt.Errorf("sql.Out destination must be a non-nil pointer")
	}
	elem := destValue.Elem()
	if !elem.CanSet() {
		return fmt.Errorf("sql.Out destination cannot be set")
	}
	if value == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	if elem.Kind() == reflect.Interface {
		elem.Set(reflect.ValueOf(value))
		return nil
	}
	switch elem.Kind() {
	case reflect.String:
		text, err := outputValueString(value)
		if err != nil {
			return err
		}
		elem.SetString(text)
		return nil
	case reflect.Bool:
		boolean, err := outputValueBool(value)
		if err != nil {
			return err
		}
		elem.SetBool(boolean)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		integer, err := outputValueInt64(value)
		if err != nil {
			return err
		}
		elem.SetInt(integer)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		integer, err := outputValueInt64(value)
		if err != nil {
			return err
		}
		elem.SetUint(uint64(integer))
		return nil
	case reflect.Float32, reflect.Float64:
		floating, err := outputValueFloat64(value)
		if err != nil {
			return err
		}
		elem.SetFloat(floating)
		return nil
	case reflect.Slice:
		if elem.Type().Elem().Kind() == reflect.Uint8 {
			bytesValue, err := outputValueBytes(value)
			if err != nil {
				return err
			}
			elem.SetBytes(bytesValue)
			return nil
		}
	}
	valueRef := reflect.ValueOf(value)
	if valueRef.IsValid() && valueRef.Type().AssignableTo(elem.Type()) {
		elem.Set(valueRef)
		return nil
	}
	if valueRef.IsValid() && valueRef.Type().ConvertibleTo(elem.Type()) {
		elem.Set(valueRef.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("unsupported sql.Out destination type %s", elem.Type())
}

func outputValueString(value driver.Value) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("cannot convert %T to string", value)
	}
}

func outputValueBool(value driver.Value) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case int64:
		return v != 0, nil
	case string:
		return strconv.ParseBool(strings.TrimSpace(v))
	case []byte:
		return strconv.ParseBool(strings.TrimSpace(string(v)))
	default:
		return false, fmt.Errorf("cannot convert %T to bool", value)
	}
}

func outputValueInt64(value driver.Value) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	case []byte:
		return strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", value)
	}
}

func outputValueFloat64(value driver.Value) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	case []byte:
		return strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

func outputValueBytes(value driver.Value) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to []byte", value)
	}
}

func encodeBigintParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	number, err := parameterFieldInt64(value)
	if err != nil {
		return nil, err
	}
	size := parameterFieldNullLength(field)
	if size < 8 {
		size = 8
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint64(buf[len(buf)-8:], uint64(number))
	return buf, nil
}

func encodeIntegerParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	number, err := parameterFieldInt64(value)
	if err != nil {
		return nil, err
	}
	if number < math.MinInt32 || number > math.MaxInt32 {
		return nil, fmt.Errorf("integer value out of range")
	}
	size := parameterFieldNullLength(field)
	if size < 4 {
		size = 4
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint32(buf[len(buf)-4:], uint32(int32(number)))
	return buf, nil
}

func encodeSmallintParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	number, err := parameterFieldInt64(value)
	if err != nil {
		return nil, err
	}
	if number < math.MinInt16 || number > math.MaxInt16 {
		return nil, fmt.Errorf("smallint value out of range")
	}
	size := parameterFieldNullLength(field)
	if size < 2 {
		size = 2
	}
	buf := make([]byte, size)
	binary.BigEndian.PutUint16(buf[len(buf)-2:], uint16(int16(number)))
	return buf, nil
}

func encodeFloatingPointParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	number, err := parameterFieldFloat64(value)
	if err != nil {
		return nil, err
	}
	if field.Length == 4 {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, math.Float32bits(float32(number)))
		return buf, nil
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, math.Float64bits(number))
	return buf, nil
}

func encodePackedDecimalParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	return encodePackedOrZonedDecimalParameter(value, field, true)
}

func encodeZonedDecimalParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	return encodePackedOrZonedDecimalParameter(value, field, false)
}

func encodePackedOrZonedDecimalParameter(value driver.Value, field parameterMarkerField, packed bool) ([]byte, error) {
	text, err := parameterFieldDecimalString(value)
	if err != nil {
		return nil, err
	}
	normalized, negative, err := normalizeDecimalText(text, field.Scale)
	if err != nil {
		return nil, err
	}
	if field.Precision > 0 && len(normalized) > field.Precision {
		return nil, fmt.Errorf("decimal value out of range")
	}
	if packed {
		return packDecimalDigits(normalized, negative, field), nil
	}
	return zoneDecimalDigits(normalized, negative, field), nil
}

func encodeDateParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldDateText(value)
	if err != nil {
		return nil, err
	}
	return encodeFixedTextBytes(text, field)
}

func encodeTimeParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldTimeText(value)
	if err != nil {
		return nil, err
	}
	return encodeFixedTextBytes(text, field)
}

func encodeTimestampParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldTimestampText(value)
	if err != nil {
		return nil, err
	}
	return encodeFixedTextBytes(text, field)
}

func encodeFixedBinaryParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	raw, err := parameterFieldBinaryBytes(value)
	if err != nil {
		return nil, err
	}
	return fitBytesToLength(raw, field.Length, 0x00), nil
}

func encodeVarbinaryParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	raw, err := parameterFieldBinaryBytes(value)
	if err != nil {
		return nil, err
	}
	if field.Length > 0 && len(raw)+2 > field.Length {
		return nil, fmt.Errorf("parameter value too long")
	}
	buf := make([]byte, 2+len(raw))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(raw)))
	copy(buf[2:], raw)
	return buf, nil
}

func encodeFixedTextParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldValueString(value)
	if err != nil {
		return nil, err
	}
	return encodeFixedTextBytes(text, field)
}

func encodeVarcharParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldValueString(value)
	if err != nil {
		return nil, err
	}
	raw, err := encodeTextBytes(text, field.CCSID)
	if err != nil {
		return nil, err
	}
	if field.Length > 0 && len(raw)+2 > field.Length {
		return nil, fmt.Errorf("parameter value too long")
	}
	buf := make([]byte, 2+len(raw))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(raw)))
	copy(buf[2:], raw)
	return buf, nil
}

func encodeVarGraphicParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldValueString(value)
	if err != nil {
		return nil, err
	}
	raw, err := encodeTextBytes(text, field.CCSID)
	if err != nil {
		return nil, err
	}
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("graphic parameter must encode to an even number of bytes")
	}
	if field.Length > 0 && len(raw)+2 > field.Length {
		return nil, fmt.Errorf("parameter value too long")
	}
	buf := make([]byte, 2+len(raw))
	binary.BigEndian.PutUint16(buf[:2], uint16(len(raw)/2))
	copy(buf[2:], raw)
	return buf, nil
}

func encodeGraphicParameter(value driver.Value, field parameterMarkerField) ([]byte, error) {
	text, err := parameterFieldValueString(value)
	if err != nil {
		return nil, err
	}
	return encodeFixedTextBytes(text, field)
}

func encodeFixedTextBytes(text string, field parameterMarkerField) ([]byte, error) {
	raw, err := encodeTextBytes(text, field.CCSID)
	if err != nil {
		return nil, err
	}
	if field.Length <= 0 {
		return raw, nil
	}
	if len(raw) > field.Length {
		return nil, fmt.Errorf("parameter value too long")
	}
	return fitTextBytesToLength(raw, field.Length, field.CCSID), nil
}

func encodeTextBytes(text string, ccsid int) ([]byte, error) {
	switch ccsid {
	case 0, 37, 273, 65535:
		return EncodeEBCDIC37(text)
	case 1200, 13488:
		return EncodeUTF16BE(text), nil
	case 1208:
		return []byte(text), nil
	default:
		return nil, fmt.Errorf("%w: unsupported CCSID %d", ErrUnsupported, ccsid)
	}
}

func fitTextBytesToLength(raw []byte, length int, ccsid int) []byte {
	out := make([]byte, length)
	switch ccsid {
	case 1200, 13488:
		for i := range out {
			out[i] = 0x00
		}
		for i := 0; i < length/2; i++ {
			binary.BigEndian.PutUint16(out[i*2:], uint16(' '))
		}
	case 1208:
		for i := range out {
			out[i] = 0x20
		}
	default:
		for i := range out {
			out[i] = 0x40
		}
	}
	copy(out, raw)
	return out
}

func fitBytesToLength(raw []byte, length int, pad byte) []byte {
	if length <= 0 {
		out := make([]byte, len(raw))
		copy(out, raw)
		return out
	}
	out := make([]byte, length)
	for i := range out {
		out[i] = pad
	}
	copy(out, raw)
	return out
}

func parameterFieldInt64(value driver.Value) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case float64:
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		parsed, err := parameterFieldDecimalFloat(v)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	case []byte:
		return parameterFieldInt64(string(v))
	default:
		return 0, errPreparedStatementBindingUnsupported
	}
}

func parameterFieldFloat64(value driver.Value) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	case []byte:
		return parameterFieldFloat64(string(v))
	default:
		return 0, errPreparedStatementBindingUnsupported
	}
}

func parameterFieldDecimalFloat(text string) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	parsed, _, err := big.ParseFloat(text, 10, 256, big.ToZero)
	if err != nil {
		return 0, err
	}
	value, _ := parsed.Int64()
	return value, nil
}

// parseIBMiTimestamp parses the IBM i TIMESTAMP wire format "YYYY-MM-DD-HH.MM.SS[.fraction]"
// into a time.Time. Fractional seconds may be 0–12 digits; precision beyond nanoseconds is
// truncated because Go's time.Time has nanosecond resolution.
func parseIBMiTimestamp(s string) (time.Time, error) {
	const base = "2006-01-02-15.04.05"
	if len(s) == len(base) {
		return time.Parse(base, s)
	}
	if len(s) > len(base) && s[len(base)] == '.' {
		frac := s[len(base)+1:]
		if len(frac) > 9 {
			// Truncate sub-nanosecond digits
			frac = frac[:9]
		}
		layout := base + "." + strings.Repeat("0", len(frac))
		return time.Parse(layout, s[:len(base)+1+len(frac)])
	}
	return time.Parse(base, s)
}

func parameterFieldDateText(value driver.Value) (string, error) {
	switch v := value.(type) {
	case time.Time:
		return v.Format("2006-01-02"), nil
	default:
		return parameterFieldValueString(value)
	}
}

func parameterFieldTimeText(value driver.Value) (string, error) {
	switch v := value.(type) {
	case time.Time:
		return v.Format("15.04.05"), nil
	default:
		return parameterFieldValueString(value)
	}
}

func parameterFieldTimestampText(value driver.Value) (string, error) {
	switch v := value.(type) {
	case time.Time:
		return v.Format("2006-01-02-15.04.05.999999999"), nil
	default:
		return parameterFieldValueString(value)
	}
}

func normalizeDecimalText(text string, scale int) (string, bool, error) {
	parsed, _, err := big.ParseFloat(strings.TrimSpace(text), 10, 256, big.ToZero)
	if err != nil {
		return "", false, err
	}
	formatted := parsed.Text('f', scale)
	negative := strings.HasPrefix(formatted, "-")
	if negative || strings.HasPrefix(formatted, "+") {
		formatted = formatted[1:]
	}
	parts := strings.SplitN(formatted, ".", 2)
	integerPart := strings.TrimLeft(parts[0], "0")
	fractionPart := ""
	if len(parts) == 2 {
		fractionPart = parts[1]
	}
	if integerPart == "" {
		if fractionPart == "" {
			return "0", negative, nil
		}
		return fractionPart, negative, nil
	}
	return integerPart + fractionPart, negative, nil
}

func packDecimalDigits(digits string, negative bool, field parameterMarkerField) []byte {
	if digits == "" {
		digits = "0"
	}
	targetBytes := field.Length
	if targetBytes <= 0 {
		targetBytes = (field.Precision + 2) / 2
	}
	if targetBytes <= 0 {
		targetBytes = (len(digits) + 2) / 2
	}
	digitSlots := targetBytes*2 - 1
	if digitSlots < 1 {
		digitSlots = 1
	}
	if len(digits) > digitSlots {
		digits = digits[len(digits)-digitSlots:]
	}
	if len(digits) < digitSlots {
		digits = strings.Repeat("0", digitSlots-len(digits)) + digits
	}
	buf := make([]byte, targetBytes)
	signNibble := byte(0x0C)
	if negative {
		signNibble = 0x0D
	}
	for i := 0; i < targetBytes-1; i++ {
		hi := digits[i*2] - '0'
		lo := digits[i*2+1] - '0'
		buf[i] = (hi << 4) | lo
	}
	buf[targetBytes-1] = (digits[len(digits)-1]-'0')<<4 | signNibble
	return buf
}

func zoneDecimalDigits(digits string, negative bool, field parameterMarkerField) []byte {
	if digits == "" {
		digits = "0"
	}
	targetBytes := field.Length
	if targetBytes <= 0 {
		targetBytes = field.Precision
	}
	if targetBytes <= 0 {
		targetBytes = len(digits)
	}
	if len(digits) > targetBytes {
		digits = digits[len(digits)-targetBytes:]
	}
	if len(digits) < targetBytes {
		digits = strings.Repeat("0", targetBytes-len(digits)) + digits
	}
	buf := make([]byte, len(digits))
	for i := 0; i < len(digits); i++ {
		buf[i] = 0xF0 | (digits[i] - '0')
	}
	if negative {
		buf[len(buf)-1] = 0xD0 | (digits[len(digits)-1] - '0')
	}
	return buf
}

func parameterMarkerPayloadLayout(format *parameterMarkerFormat, bindings []parameterBinding) bool {
	if format == nil || !format.usesOriginalFormat() {
		return true
	}
	if len(bindings) > 0xFFFF {
		return true
	}
	total := 0
	for i := range bindings {
		if len(bindings[i].raw) > 0xFFFF {
			return true
		}
		total += len(bindings[i].raw)
		if total > 0xFFFF {
			return true
		}
	}
	return false
}

func sumBindingLengths(bindings []parameterBinding) int {
	total := 0
	for i := range bindings {
		total += len(bindings[i].raw)
	}
	return total
}

func buildParameterMarkerFormatPayload(format *parameterMarkerFormat, bindings []parameterBinding) ([]byte, uint16, error) {
	useExtended := parameterMarkerPayloadLayout(format, bindings)
	if useExtended {
		payload := make([]byte, 16+len(bindings)*64)
		binary.BigEndian.PutUint32(payload[0:4], 1)
		binary.BigEndian.PutUint32(payload[4:8], uint32(len(bindings)))
		binary.BigEndian.PutUint32(payload[12:16], uint32(sumBindingLengths(bindings)))
		for i := range bindings {
			base := 16 + i*64
			binary.BigEndian.PutUint16(payload[base:base+2], 64)
			binary.BigEndian.PutUint16(payload[base+2:base+4], uint16(bindings[i].field.SQLType))
			binary.BigEndian.PutUint32(payload[base+4:base+8], uint32(len(bindings[i].raw)))
			binary.BigEndian.PutUint16(payload[base+8:base+10], uint16(bindings[i].field.Scale))
			binary.BigEndian.PutUint16(payload[base+10:base+12], uint16(bindings[i].field.Precision))
			binary.BigEndian.PutUint16(payload[base+12:base+14], uint16(bindings[i].field.CCSID))
			payload[base+14] = byte(bindings[i].field.ParameterType)
			binary.BigEndian.PutUint32(payload[base+17:base+21], uint32(bindings[i].field.LOBLocator))
			binary.BigEndian.PutUint32(payload[base+26:base+30], uint32(bindings[i].field.LOBMaxSize))
		}
		return payload, CodePointExtendedParameterMarker, nil
	}

	payload := make([]byte, 8+len(bindings)*54)
	binary.BigEndian.PutUint32(payload[0:4], 1)
	binary.BigEndian.PutUint16(payload[4:6], uint16(len(bindings)))
	binary.BigEndian.PutUint16(payload[6:8], uint16(sumBindingLengths(bindings)))
	for i := range bindings {
		base := 8 + i*54
		binary.BigEndian.PutUint16(payload[base:base+2], 54)
		binary.BigEndian.PutUint16(payload[base+2:base+4], uint16(bindings[i].field.SQLType))
		binary.BigEndian.PutUint16(payload[base+4:base+6], uint16(len(bindings[i].raw)))
		binary.BigEndian.PutUint16(payload[base+6:base+8], uint16(bindings[i].field.Scale))
		binary.BigEndian.PutUint16(payload[base+8:base+10], uint16(bindings[i].field.Precision))
		binary.BigEndian.PutUint16(payload[base+10:base+12], uint16(bindings[i].field.CCSID))
		payload[base+12] = byte(bindings[i].field.ParameterType)
	}
	return payload, CodePointParameterMarkerFormat, nil
}

func buildParameterMarkerDataPayload(format *parameterMarkerFormat, bindings []parameterBinding) ([]byte, uint16, error) {
	useExtended := parameterMarkerPayloadLayout(format, bindings)
	indicatorSize := len(bindings) * 2
	totalLength := sumBindingLengths(bindings)
	if useExtended {
		payload := make([]byte, 20+indicatorSize+totalLength)
		binary.BigEndian.PutUint32(payload[0:4], 1)
		binary.BigEndian.PutUint32(payload[4:8], 1)
		binary.BigEndian.PutUint16(payload[8:10], uint16(len(bindings)))
		binary.BigEndian.PutUint16(payload[10:12], 2)
		binary.BigEndian.PutUint32(payload[12:16], 0)
		binary.BigEndian.PutUint32(payload[16:20], uint32(totalLength))
		indicatorOffset := 20
		dataOffset := indicatorOffset + indicatorSize
		for i := range bindings {
			indicator := uint16(0)
			if bindings[i].isNull {
				indicator = 0xFFFF
			}
			binary.BigEndian.PutUint16(payload[indicatorOffset+i*2:indicatorOffset+i*2+2], indicator)
		}
		offset := dataOffset
		for i := range bindings {
			copy(payload[offset:], bindings[i].raw)
			offset += len(bindings[i].raw)
		}
		return payload, CodePointParameterMarkerDataExt, nil
	}

	payload := make([]byte, 14+indicatorSize+totalLength)
	binary.BigEndian.PutUint32(payload[0:4], 1)
	binary.BigEndian.PutUint32(payload[4:8], 1)
	binary.BigEndian.PutUint16(payload[8:10], uint16(len(bindings)))
	binary.BigEndian.PutUint16(payload[10:12], 2)
	binary.BigEndian.PutUint16(payload[12:14], uint16(totalLength))
	indicatorOffset := 14
	dataOffset := indicatorOffset + indicatorSize
	for i := range bindings {
		indicator := uint16(0)
		if bindings[i].isNull {
			indicator = 0xFFFF
		}
		binary.BigEndian.PutUint16(payload[indicatorOffset+i*2:indicatorOffset+i*2+2], indicator)
	}
	offset := dataOffset
	for i := range bindings {
		copy(payload[offset:], bindings[i].raw)
		offset += len(bindings[i].raw)
	}
	return payload, CodePointParameterMarkerData, nil
}

func buildChangeDescriptorRequest(statementHandle, descriptorHandle uint16, format *parameterMarkerFormat, bindings []parameterBinding) ([]byte, error) {
	formatPayload, formatCodePoint, err := buildParameterMarkerFormatPayload(format, bindings)
	if err != nil {
		return nil, err
	}
	requestFormatCodePoint := formatCodePoint
	switch formatCodePoint {
	case CodePointParameterMarkerFormat:
		requestFormatCodePoint = 0x3801
	case CodePointExtendedParameterMarker, CodePointSuperExtendedParameterMarker:
		requestFormatCodePoint = 0x381E
	}
	body := AppendLLCP(nil, requestFormatCodePoint, formatPayload)
	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionChangeDescriptor)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsSQLCA, statementHandle, descriptorHandle, 1)
	request = append(request, body...)
	return request, nil
}

func buildDeleteDescriptorRequest(statementHandle, descriptorHandle uint16) []byte {
	request := buildHeader(40, 0, DatabaseServerID, 20, FunctionDeleteDescriptor)
	return appendRequestTemplate(request, orsSendReplyImmediately|orsSQLCA, statementHandle, descriptorHandle, 0)
}

func buildExecutePreparedRequest(statementHandle, descriptorHandle uint16, statementType int, format *parameterMarkerFormat, bindings []parameterBinding) ([]byte, error) {
	dataPayload, dataCodePoint, err := buildParameterMarkerDataPayload(format, bindings)
	if err != nil {
		return nil, err
	}
	body := make([]byte, 0, len(dataPayload)+16)
	body = appendShortAttribute(body, 0x3812, uint16(statementType))
	body = append(body, AppendLLCP(nil, dataCodePoint, dataPayload)...)
	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionExecute)
	templateBitmap := orsSendReplyImmediately | orsSQLCA
	if statementType == sqlStatementTypeCall && hasOutputBindings(bindings) {
		templateBitmap |= orsResultData
	}
	request = appendRequestTemplate(request, templateBitmap, statementHandle, descriptorHandle, 2)
	request = append(request, body...)
	return request, nil
}

func collectLOBWriteRequests(bindings []parameterBinding) []*lobWriteRequest {
	requests := make([]*lobWriteRequest, 0, len(bindings))
	for i := range bindings {
		if bindings[i].lobWrite != nil {
			requests = append(requests, bindings[i].lobWrite)
		}
	}
	return requests
}

func buildWriteLOBDataRequest(write *lobWriteRequest) []byte {
	body := make([]byte, 0, 64+len(write.data))
	body = appendIntAttribute(body, CodePointLOBLocatorHandle, write.locatorHandle)
	body = appendIntAttribute(body, CodePointRequestedSize, uint32(write.requestedSize))
	body = appendIntAttribute(body, CodePointStartOffset, 0)
	body = appendByteAttribute(body, CodePointCompressionIndicator, 0xF0)
	body = AppendLLCP(body, 0x381D, write.data)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionWriteLobData)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsResultData, 0, 0, 5)
	return append(request, body...)
}

func (c *Conn) writeLOBData(ctx context.Context, write *lobWriteRequest) error {
	if write == nil {
		return nil
	}
	envelope, _, err := c.doRequest(ctx, buildWriteLOBDataRequest(write))
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("write LOB data", envelope)
	}
	return nil
}

func (c *Conn) writeLOBDataRequests(ctx context.Context, bindings []parameterBinding) error {
	for _, request := range collectLOBWriteRequests(bindings) {
		if err := c.writeLOBData(ctx, request); err != nil {
			return err
		}
	}
	return nil
}

func buildRetrieveLobDataRequest(locatorHandle uint32, requestedSize int) []byte {
	body := make([]byte, 0, 64)
	body = appendIntAttribute(body, CodePointLOBLocatorHandle, locatorHandle)
	body = appendIntAttribute(body, CodePointRequestedSize, uint32(requestedSize))
	body = appendIntAttribute(body, CodePointStartOffset, 0)
	body = appendByteAttribute(body, CodePointCompressionIndicator, 0xF0)
	body = appendByteAttribute(body, 0x3821, 0xF1)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionRetrieveLobData)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsResultData, 0, 0, 5)
	return append(request, body...)
}

func buildFreeLobRequest(locatorHandle uint32) []byte {
	body := make([]byte, 0, 16)
	body = appendIntAttribute(body, CodePointLOBLocatorHandle, locatorHandle)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionFreeLob)
	request = appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 1)
	return append(request, body...)
}

func parseCurrentLobLengthFromPayload(payload []byte) int64 {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return -1
		}
		if codePoint == CodePointCurrentLOBLength {
			if len(cpPayload) == 6 {
				return int64(binary.BigEndian.Uint32(cpPayload[2:6]))
			}
			if len(cpPayload) >= 10 {
				return int64(binary.BigEndian.Uint64(cpPayload[2:10]))
			}
			return 0
		}
		remaining = rest
	}
	return -1
}

func parseLobDataFromPayload(payload []byte) ([]byte, error) {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		if codePoint == CodePointLOBLocatorData {
			if len(cpPayload) < 6 {
				return []byte(nil), nil
			}
			length := int(binary.BigEndian.Uint32(cpPayload[2:6]))
			if length > len(cpPayload)-6 {
				length = len(cpPayload) - 6
			}
			data := make([]byte, length)
			copy(data, cpPayload[6:6+length])
			return data, nil
		}
		remaining = rest
	}
	return []byte(nil), nil
}

func (c *Conn) retrieveLobData(ctx context.Context, locatorHandle uint32) ([]byte, error) {
	lengthEnvelope, lengthPayload, err := c.doRequest(ctx, buildRetrieveLobDataRequest(locatorHandle, 0))
	if err != nil {
		return nil, err
	}
	if lengthEnvelope.hasError() {
		return nil, newProtocolError("retrieve LOB length", lengthEnvelope)
	}
	currentLength := parseCurrentLobLengthFromPayload(lengthPayload)
	if currentLength <= 0 {
		return []byte(nil), nil
	}
	if currentLength > math.MaxInt32 {
		currentLength = math.MaxInt32
	}
	envelope, payload, err := c.doRequest(ctx, buildRetrieveLobDataRequest(locatorHandle, int(currentLength)))
	if err != nil {
		return nil, err
	}
	if envelope.hasError() {
		return nil, newProtocolError("retrieve LOB data", envelope)
	}
	data, err := parseLobDataFromPayload(payload)
	if err != nil {
		return nil, newWireError("parse LOB data", err)
	}
	return data, nil
}

func (c *Conn) freeLob(ctx context.Context, locatorHandle uint32) error {
	if locatorHandle == 0 {
		return nil
	}
	envelope, _, err := c.doRequest(ctx, buildFreeLobRequest(locatorHandle))
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("free LOB", envelope)
	}
	return nil
}

func buildOpenDescribePreparedRequest(statementHandle, descriptorHandle uint16, statementName, cursorName string, format *parameterMarkerFormat, bindings []parameterBinding, scrollable, hold bool) ([]byte, error) {
	body := make([]byte, 0, 256)
	parms := 0

	nameAttr, err := appendNameAttribute(0x3806, statementName)
	if err != nil {
		return nil, err
	}
	body = append(body, nameAttr...)
	parms++

	cursorAttr, err := appendNameAttribute(0x380B, cursorName)
	if err != nil {
		return nil, err
	}
	body = append(body, cursorAttr...)
	parms++

	body = appendByteAttribute(body, 0x3809, 0x80)
	parms++
	body = appendByteAttribute(body, 0x3833, 0xE8)
	parms++
	body = appendByteAttribute(body, 0x380A, 0xD5)
	parms++
	if scrollable {
		body = appendShortAttribute(body, CodePointScrollableCursorFlag, 2)
	} else {
		body = appendShortAttribute(body, CodePointScrollableCursorFlag, 0)
	}
	parms++
	if hold {
		body = appendByteAttribute(body, CodePointHoldIndicator, holdTrue)
		body = appendByteAttribute(body, CodePointResultSetHoldability, holdTrue)
		parms += 2
	}

	if len(bindings) > 0 {
		dataPayload, dataCodePoint, err := buildParameterMarkerDataPayload(format, bindings)
		if err != nil {
			return nil, err
		}
		body = append(body, AppendLLCP(nil, dataCodePoint, dataPayload)...)
		parms++
	}

	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionOpenDescribe)
	templateBitmap := orsSendReplyImmediately | orsDataFormat | orsSQLCA | orsReturnResultSetAttributes
	request = appendRequestTemplate(request, templateBitmap, statementHandle, descriptorHandle, parms)
	request = append(request, body...)
	return request, nil
}

type sliceReader struct {
	data []byte
	pos  int
}

func newSliceReader(data []byte) *sliceReader {
	return &sliceReader{data: data}
}

func (r *sliceReader) remaining() int {
	return len(r.data) - r.pos
}

func (r *sliceReader) readU8() (byte, error) {
	if r.remaining() < 1 {
		return 0, io.EOF
	}
	v := r.data[r.pos]
	r.pos++
	return v, nil
}

func (r *sliceReader) readU16() (uint16, error) {
	if r.remaining() < 2 {
		return 0, io.EOF
	}
	v := binary.BigEndian.Uint16(r.data[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *sliceReader) readU32() (uint32, error) {
	if r.remaining() < 4 {
		return 0, io.EOF
	}
	v := binary.BigEndian.Uint32(r.data[r.pos:])
	r.pos += 4
	return v, nil
}

func (r *sliceReader) readBytes(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, io.EOF
	}
	v := r.data[r.pos : r.pos+n]
	r.pos += n
	return v, nil
}

func (r *sliceReader) skip(n int) error {
	_, err := r.readBytes(n)
	return err
}

package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Batch execution mirrors JTOpen's AS400JDBCPreparedStatementImpl.executeBatch.
// With the "useBlockUpdate" DSN option enabled, consecutive parameter rows that
// have the same shape are sent in one EXECUTE request as a parameter marker
// data block (code point 0x3811 or 0x381F with rowCount > 1). Otherwise, and
// for statements or parameters that cannot be blocked, each row is executed
// separately on the same prepared statement.

const (
	// maxBlockRows matches JTOpen's "maximum blocked input rows" default and cap.
	maxBlockRows = 32000
	// maxBlockBytes bounds the parameter data sent in one block EXECUTE. JTOpen
	// does not cap this explicitly; the limit keeps single requests modest.
	maxBlockBytes = 1 << 20
)

// ExecBatch executes query once for every row in rows and returns the total
// number of rows affected. The statement is prepared once. Values are
// normalized with the same rules as CheckNamedValue. Positional placeholders
// are bound in order; named placeholders bind in order of appearance.
//
// When the DSN sets useBlockUpdate=true, eligible statements are sent in
// blocks (see batchBlockable). Any other statement runs row by row.
func (c *Conn) ExecBatch(ctx context.Context, query string, rows [][]driver.Value) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.IsValid() {
		return 0, driver.ErrBadConn
	}
	if len(rows) == 0 {
		return 0, nil
	}
	rewritten, _, err := rewriteNamedParams(query)
	if err != nil {
		return 0, err
	}
	statementType, statementHandle, statementName, format, err := c.prepareTransientStatement(ctx, rewritten)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = c.closePreparedStatement(context.Background(), statementName, statementHandle)
	}()
	return c.execBatchPrepared(ctx, rewritten, statementHandle, statementType, format, rows)
}

// ExecBatch executes the prepared statement once for every row in rows. It has
// the same semantics as Conn.ExecBatch but reuses the statement prepared by
// the Stmt.
func (s *Stmt) ExecBatch(ctx context.Context, rows [][]driver.Value) (int64, error) {
	if s == nil {
		return 0, ErrConnectionClosed
	}
	if err := s.acquire(); err != nil {
		return 0, err
	}
	defer s.release()
	if s.conn == nil || !s.conn.IsValid() {
		return 0, driver.ErrBadConn
	}
	if s.statementName == "" || s.parameterMarkerFormat == nil {
		return 0, errPreparedStatementBindingUnsupported
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return s.conn.execBatchPrepared(ctx, s.query, s.statementHandle, s.statementType, s.parameterMarkerFormat, rows)
}

func (c *Conn) execBatchPrepared(ctx context.Context, query string, statementHandle uint16, statementType int, format *parameterMarkerFormat, rows [][]driver.Value) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if statementType == sqlStatementTypeSelect {
		return 0, unsupported("ExecBatch cannot execute query statements")
	}
	named, err := batchNamedRows(c, rows)
	if err != nil {
		return 0, err
	}
	if !c.batchBlockable(query, format) {
		return c.execBatchRowByRow(ctx, statementHandle, statementType, format, named)
	}
	return c.execBatchBlocks(ctx, statementHandle, statementType, format, named)
}

// batchNamedRows normalizes every value with CheckNamedValue and rejects
// sql.Out, which has no meaning for a batch.
func batchNamedRows(c *Conn, rows [][]driver.Value) ([][]driver.NamedValue, error) {
	out := make([][]driver.NamedValue, len(rows))
	for r, row := range rows {
		args := make([]driver.NamedValue, len(row))
		for i, value := range row {
			if _, ok := value.(sql.Out); ok {
				return nil, fmt.Errorf("batch row %d: sql.Out is not supported in ExecBatch", r)
			}
			args[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
			if err := c.CheckNamedValue(&args[i]); err != nil && !errors.Is(err, driver.ErrSkip) {
				return nil, fmt.Errorf("batch row %d column %d: %w", r, i+1, err)
			}
		}
		out[r] = args
	}
	return out, nil
}

// batchBlockable reports whether the rows may be sent as parameter blocks:
// the DSN option must be on, the statement must be one JTOpen batches with
// block update, and no parameter may be a LOB locator (JTOpen excludes
// locators because the host server reserves one locator handle per statement).
func (c *Conn) batchBlockable(query string, format *parameterMarkerFormat) bool {
	if !c.cfg.UseBlockUpdate || format == nil || len(format.Fields) == 0 {
		return false
	}
	if !isBlockableStatement(query) {
		return false
	}
	// JTOpen blocks UPDATE and DELETE only on V7R1 and later; older or
	// unknown servers execute them row by row.
	if !isInsertStatement(query) && !c.serverVRMAtLeast(blockUpdateMinVRM) {
		return false
	}
	for _, field := range format.Fields {
		if isLOBParameterField(field) || field.LOBLocator > 0 {
			return false
		}
	}
	return true
}

// isBlockableStatement follows JTOpen's canBeBatched rules: an INSERT whose
// VALUES clause holds only parameter markers, or an UPDATE or DELETE (JTOpen
// allows these only with "use block update").
// blockUpdateMinVRM is V7R1M0, the first release JTOpen sends blocked
// UPDATE/DELETE requests to.
const blockUpdateMinVRM = 0x070100

func isBlockableStatement(text string) bool {
	upper := strings.ToUpper(strings.TrimSpace(text))
	switch {
	case strings.HasPrefix(upper, "UPDATE"), strings.HasPrefix(upper, "DELETE"):
		return true
	case strings.HasPrefix(upper, "INSERT"):
		m := strings.Index(upper, "VALUES")
		if m < 0 {
			return false
		}
		values := upper[m+len("VALUES"):]
		if last := strings.LastIndexByte(values, ')'); last >= 0 {
			values = values[:last]
		}
		tokens := strings.FieldsFunc(values, func(r rune) bool {
			return strings.ContainsRune(", ?()\n\r\t", r)
		})
		return len(tokens) == 0
	default:
		return false
	}
}

// execBatchRowByRow is the fallback: one EXECUTE per row on the same prepared
// statement.
func (c *Conn) execBatchRowByRow(ctx context.Context, statementHandle uint16, statementType int, format *parameterMarkerFormat, rows [][]driver.NamedValue) (int64, error) {
	var total int64
	for r, args := range rows {
		result, err := c.execPrepared(ctx, statementHandle, statementType, format, args)
		if err != nil {
			return total, fmt.Errorf("batch row %d: %w", r, err)
		}
		n, _ := result.RowsAffected()
		total += n
	}
	return total, nil
}

// execBatchBlocks binds every row, groups consecutive rows that share a shape
// (same raw length per column) and sends each group in blocks no larger than
// maxBlockRows rows or maxBlockBytes bytes.
func (c *Conn) execBatchBlocks(ctx context.Context, statementHandle uint16, statementType int, format *parameterMarkerFormat, rows [][]driver.NamedValue) (int64, error) {
	bound := make([][]parameterBinding, len(rows))
	for r, args := range rows {
		bindings, err := buildParameterBindingsWithBidi(format, args, bidiSettingsForConn(c))
		if err != nil {
			return 0, fmt.Errorf("batch row %d: %w", r, err)
		}
		bound[r] = bindings
	}

	var total int64
	for start := 0; start < len(bound); {
		end := blockEnd(bound, start)
		n, err := c.execBlock(ctx, statementHandle, statementType, format, bound[start:end])
		total += n
		if err != nil {
			return total, err
		}
		start = end
	}
	return total, nil
}

// blockEnd returns the exclusive end index of the block that starts at start.
// A block always holds at least one row.
func blockEnd(rows [][]parameterBinding, start int) int {
	end := start + 1
	rowBytes := bindingRowBytes(rows[start])
	for end < len(rows) && end-start < maxBlockRows {
		if !sameBindingShape(rows[start], rows[end]) {
			break
		}
		if (end-start+1)*rowBytes > maxBlockBytes {
			break
		}
		end++
	}
	return end
}

func bindingRowBytes(row []parameterBinding) int {
	return sumBindingLengths(row)
}

func sameBindingShape(a, b []parameterBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i].raw) != len(b[i].raw) {
			return false
		}
	}
	return true
}

// execBlock sends one block of rows in one EXECUTE. It follows execPrepared:
// the descriptor is changed for the block shape, the block is executed, and the
// descriptor is deleted again. It returns the update count reported for the
// block.
func (c *Conn) execBlock(ctx context.Context, statementHandle uint16, statementType int, format *parameterMarkerFormat, rows [][]parameterBinding) (int64, error) {
	descriptorHandle := c.nextDescriptorHandle()
	changeRequest, err := buildChangeDescriptorRequest(statementHandle, descriptorHandle, format, rows[0])
	if err != nil {
		return 0, err
	}
	defer func() {
		deleteRequest := buildDeleteDescriptorRequest(statementHandle, descriptorHandle)
		deleteEnvelope, _, deleteErr := c.doRequest(ctx, deleteRequest)
		c.recordDeleteDescriptorFailure(deleteErr, deleteEnvelope)
	}()

	envelope, payload, err := c.doRequest(ctx, changeRequest)
	if err != nil {
		return 0, err
	}
	if err := c.checkReply(ctx, statementHandle, "change descriptor", envelope, payload); err != nil {
		return 0, err
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return 0, err
	}
	if info.SQLCode < 0 {
		return 0, c.newSQLError(ctx, statementHandle, info)
	}
	c.recordWarning(info.Warning)

	request, err := buildBlockExecuteRequest(statementHandle, descriptorHandle, statementType, format, rows, c.pkg.specForExecute(statementHandle))
	if err != nil {
		return 0, err
	}
	envelope, payload, err = c.doStatementRequest(ctx, request, statementHandle)
	if err != nil {
		return 0, err
	}
	if err := c.checkReply(ctx, statementHandle, "block execute", envelope, payload); err != nil {
		return 0, err
	}
	info, err = parseSQLCAFromPayload(payload)
	if err != nil {
		return 0, err
	}
	c.recordWarning(info.Warning)
	if info.SQLCode < 0 {
		return 0, c.newSQLError(ctx, statementHandle, info)
	}
	if info.ResultSetsCount > 0 {
		return 0, fmt.Errorf("statement returned result sets; use QueryContext")
	}
	if info.UpdateCount < 0 {
		return 0, nil
	}
	return info.UpdateCount, nil
}

// buildBlockParameterMarkerData builds the parameter marker data for a block of
// rows. The layout matches DBOriginalData (0x3811) or DBExtendedData (0x381F)
// in JTOpen with rowCount = len(rows):
//
//	header:     consistency token (4) = 1
//	            row count (4)
//	            column count (2)
//	            indicator size (2) = 2
//	            row size (2 for original, 4 for extended, at offset 16)
//	            extended only: 4 reserved bytes at offset 12 = 0
//	indicators: rowCount * columnCount * 2 bytes; index (row*cols+col); 0xFFFF = NULL
//	data:       rowCount * rowSize bytes; row r starts at r*rowSize
//
// All rows must have the same shape; buildBlockExecuteRequest callers group
// rows with sameBindingShape before calling.
func buildBlockParameterMarkerData(format *parameterMarkerFormat, rows [][]parameterBinding) ([]byte, uint16, error) {
	if len(rows) == 0 {
		return nil, 0, fmt.Errorf("parameter block has no rows")
	}
	first := rows[0]
	columns := len(first)
	rowSize := bindingRowBytes(first)
	for r := range rows {
		if !sameBindingShape(first, rows[r]) {
			return nil, 0, fmt.Errorf("parameter block row %d has a different shape", r)
		}
	}
	if columns > 0xFFFF {
		return nil, 0, fmt.Errorf("parameter block has too many columns")
	}

	useExtended := parameterMarkerPayloadLayout(format, first)
	headerSize := 14
	var codePoint uint16 = CodePointParameterMarkerData
	if useExtended {
		headerSize = 20
		codePoint = CodePointParameterMarkerDataExt
	}
	indicatorTotal := len(rows) * columns * 2
	payload := make([]byte, headerSize+indicatorTotal+len(rows)*rowSize)

	binary.BigEndian.PutUint32(payload[0:4], 1)
	binary.BigEndian.PutUint32(payload[4:8], uint32(len(rows)))
	binary.BigEndian.PutUint16(payload[8:10], uint16(columns))
	binary.BigEndian.PutUint16(payload[10:12], 2)
	if useExtended {
		binary.BigEndian.PutUint32(payload[16:20], uint32(rowSize))
	} else {
		binary.BigEndian.PutUint16(payload[12:14], uint16(rowSize))
	}

	indicatorOffset := headerSize
	dataOffset := headerSize + indicatorTotal
	for r := range rows {
		for c := range rows[r] {
			if rows[r][c].isNull {
				idx := indicatorOffset + (r*columns+c)*2
				binary.BigEndian.PutUint16(payload[idx:idx+2], 0xFFFF)
			}
		}
	}
	offset := dataOffset
	for r := range rows {
		for c := range rows[r] {
			copy(payload[offset:], rows[r][c].raw)
			offset += len(rows[r][c].raw)
		}
	}
	return payload, codePoint, nil
}

// buildBlockExecuteRequest builds an EXECUTE request that carries a parameter
// block. Besides the statement type and the data, it sends the parameter marker
// block indicator (0x3814, value 0), which JTOpen always sends after the data.
func buildBlockExecuteRequest(statementHandle, descriptorHandle uint16, statementType int, format *parameterMarkerFormat, rows [][]parameterBinding, pkg *packageSpec) ([]byte, error) {
	dataPayload, dataCodePoint, err := buildBlockParameterMarkerData(format, rows)
	if err != nil {
		return nil, err
	}
	body := make([]byte, 0, len(dataPayload)+32)
	body = appendShortAttribute(body, 0x3812, uint16(statementType))
	body = append(body, AppendLLCP(nil, dataCodePoint, dataPayload)...)
	body = AppendLLCP(body, CodePointParameterMarkerBlockInd, []byte{0, 0})
	parms := 3
	body, parms = pkg.appendExecuteAttributes(body, parms)
	length := 40 + len(body)
	request := buildHeader(uint32(length), 0, DatabaseServerID, 20, FunctionExecute)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsSQLCA, statementHandle, descriptorHandle, parms)
	request = append(request, body...)
	return request, nil
}

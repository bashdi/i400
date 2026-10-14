package i400

import (
	"context"
	"database/sql/driver"
	"strings"
)

// queryCallRows runs a CALL statement and opens its first result set.
//
// As in JTOpen (AS400JDBCStatement), the procedure is executed first. Its
// returned result sets stay pending on the server and are opened with
// OPEN DESCRIBE on the same statement; the first open here, and later ones
// from queryRows.NextResultSet, both use that path. Opening a CALL statement
// before it has been executed fails with SQLCODE -517 (SQLSTATE 07005).
func (c *Conn) queryCallRows(ctx context.Context, statementHandle uint16, statementName string) (driver.Rows, error) {
	if strings.TrimSpace(statementName) == "" {
		return nil, driver.ErrBadConn
	}
	if err := c.syncQueryTimeoutRPB(ctx, statementHandle); err != nil {
		return nil, err
	}
	envelope, payload, err := c.doStatementRequest(ctx, buildExecuteCallNoParamsRequest(statementHandle, c.pkg.specForExecute(statementHandle)), statementHandle)
	if err != nil {
		return nil, err
	}
	if err := c.checkReply(ctx, statementHandle, "execute", envelope, payload); err != nil {
		return nil, err
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return nil, err
	}
	c.recordWarning(info.Warning)
	if info.SQLCode < 0 {
		return nil, c.newSQLError(ctx, statementHandle, info)
	}

	rows, err := c.queryRowsForStatement(ctx, statementHandle, statementName)
	if err != nil {
		return nil, err
	}
	if qr, ok := rows.(*queryRows); ok {
		// SQLERRD(2) of the execute reply counts the result sets the CALL returned.
		if info.ResultSetsCount > 0 {
			qr.resultSetCount = info.ResultSetsCount
		}
		qr.storedProcResult = true
	}
	return rows, nil
}

// buildExecuteCallNoParamsRequest builds the execute request for a CALL with
// no parameter markers. Unlike buildExecutePreparedRequest it omits the
// parameter marker data block, which the server rejects with PWS0043 (-603)
// when the statement has no markers.
func buildExecuteCallNoParamsRequest(statementHandle uint16, pkg *packageSpec) []byte {
	body := appendShortAttribute(nil, 0x3812, uint16(sqlStatementTypeCall))
	body, parms := pkg.appendExecuteAttributes(body, 1)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionExecute)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsSQLCA|orsReturnResultSetAttributes, statementHandle, 0, parms)
	return append(request, body...)
}

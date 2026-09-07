package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// consumeCreateRPBPreamble reads and discards the Create RPB preamble (0x1D00) that the
// driver sends fire-and-forget before every prepare-describe request. No reply is expected.
func consumeCreateRPBPreamble(t *testing.T, server net.Conn) bool {
	t.Helper()
	packet, err := readPacket(server)
	if err != nil {
		t.Errorf("read create RPB preamble: %v", err)
		return false
	}
	header, err := ParseHeader(packet)
	if err != nil {
		t.Errorf("parse create RPB preamble header: %v", err)
		return false
	}
	if header.ReqRepID != FunctionCreateRPB {
		t.Errorf("expected create RPB preamble (0x%04x), got 0x%04x", FunctionCreateRPB, header.ReqRepID)
		return false
	}
	return true
}

func buildTestSQLCAPayload(t *testing.T, sqlCode int32, sqlState string, resultSetsCount int) []byte {
	t.Helper()
	if len(sqlState) > 5 {
		sqlState = sqlState[:5]
	}
	stateBytes, err := charmap.CodePage037.NewEncoder().Bytes([]byte(sqlState))
	if err != nil {
		t.Fatalf("encode SQL state: %v", err)
	}
	sqlca := make([]byte, 136)
	binary.BigEndian.PutUint32(sqlca[12:16], uint32(sqlCode))
	binary.BigEndian.PutUint32(sqlca[100:104], uint32(resultSetsCount))
	copy(sqlca[131:136], stateBytes)
	return appendTestLLCP(nil, 0x3807, sqlca)
}

func buildTestDescribePayload() []byte {
	data := make([]byte, 0, 64)
	data = appendU32(data, 1)
	data = appendU32(data, 1)
	data = append(data, 5, 0, 0, 0)
	data = appendU32(data, 4)
	data = appendU16(data, 0)
	data = appendU16(data, uint16(db2TypeInteger))
	data = appendU32(data, 4)
	data = appendU16(data, 0)
	data = appendU16(data, 0)
	data = appendU16(data, 37)
	data = append(data, 0)
	data = appendU16(data, 0)
	data = appendU32(data, 0)
	data = append(data, 0)
	data = appendU32(data, 0)
	data = appendU32(data, 0)
	data = appendU16(data, 0)
	data = appendU32(data, 0)
	data = appendU32(data, 0)
	data = appendU32(data, 0)
	data = appendU32(data, 0)
	return appendTestLLCP(nil, 0x3812, data)
}

func buildTestReplyPacket(payload []byte) []byte {
	packet := buildHeader(uint32(40+len(payload)), 0, DatabaseServerID, 20, 0x2800)
	packet = appendU32(packet, 0)
	packet = appendU32(packet, 0)
	packet = appendU16(packet, 0)
	packet = appendU16(packet, 0)
	packet = appendU16(packet, 0)
	packet = appendU16(packet, 0)
	packet = appendI32(packet, 0)
	return append(packet, payload...)
}

func appendTestLLCP(dst []byte, codePoint uint16, data []byte) []byte {
	dst = appendU32(dst, uint32(6+len(data)))
	dst = appendU16(dst, codePoint)
	dst = appendBytes(dst, data)
	return dst
}

func buildTestPrepareParameterMarkerFormatPayload(field parameterMarkerField) []byte {
	data := make([]byte, 8+54)
	binary.BigEndian.PutUint32(data[0:4], 1)
	binary.BigEndian.PutUint16(data[4:6], 1)
	binary.BigEndian.PutUint16(data[6:8], uint16(field.Length))

	base := 8
	binary.BigEndian.PutUint16(data[base+0:], 54)
	binary.BigEndian.PutUint16(data[base+2:], uint16(field.SQLType))
	binary.BigEndian.PutUint16(data[base+4:], uint16(field.Length))
	binary.BigEndian.PutUint16(data[base+6:], uint16(field.Scale))
	binary.BigEndian.PutUint16(data[base+8:], uint16(field.Precision))
	binary.BigEndian.PutUint16(data[base+10:], uint16(field.CCSID))
	data[base+12] = byte(field.ParameterType)
	return appendTestLLCP(nil, CodePointParameterMarkerFormat, data)
}

func readTestLLCPValue(payload []byte, wantCodePoint uint16) ([]byte, bool, error) {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, false, err
		}
		if codePoint == wantCodePoint {
			return cpPayload, true, nil
		}
		remaining = rest
	}
	return nil, false, nil
}

func TestParseSQLCAFromPayloadWarning(t *testing.T) {
	payload := buildTestSQLCAPayload(t, 1, "01004", 3)
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		t.Fatalf("parseSQLCAFromPayload() error = %v", err)
	}
	if info.Warning == nil {
		t.Fatalf("parseSQLCAFromPayload() warning = nil")
	}
	if info.Warning.Code != 1 {
		t.Fatalf("warning code = %d, want 1", info.Warning.Code)
	}
	if info.Warning.State != "01004" {
		t.Fatalf("warning state = %q, want 01004", info.Warning.State)
	}
	if info.ResultSetsCount != 3 {
		t.Fatalf("ResultSetsCount = %d, want 3", info.ResultSetsCount)
	}
}

func TestQueryRowsNextResultSet(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read close request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		reusePayload, found, err := readTestLLCPValue(packet[40:], 0x3810)
		if err != nil {
			t.Errorf("parse close request body: %v", err)
			return
		}
		if !found {
			t.Errorf("close request missing reuse indicator")
			return
		}
		if len(reusePayload) != 1 || reusePayload[0] != cursorReuseResultSet {
			t.Errorf("reuse indicator = %v, want %x", reusePayload, cursorReuseResultSet)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read open request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse open request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionOpenDescribe {
			t.Errorf("open request id = 0x%x, want 0x%x", header.ReqRepID, FunctionOpenDescribe)
			return
		}
		payload := append(buildTestSQLCAPayload(t, 1, "01004", 2), buildTestDescribePayload()...)
		if _, err := server.Write(buildTestReplyPacket(payload)); err != nil {
			t.Errorf("write open reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	rows := &queryRows{
		conn:            conn,
		ctx:             context.Background(),
		meta:            &resultSetMeta{columns: []columnMeta{{Name: "INITIAL", Type: db2TypeInteger, Length: 8}}, RowSize: 8},
		statementName:   "STMT0001",
		cursorName:      "CRSR0001",
		fetchBufferSize: 256 * 1024,
		resultSetCount:  2,
		resultSetIndex:  1,
	}

	if !rows.HasNextResultSet() {
		t.Fatalf("HasNextResultSet() = false, want true")
	}
	if err := rows.NextResultSet(); err != nil {
		t.Fatalf("NextResultSet() error = %v", err)
	}
	if rows.resultSetIndex != 2 {
		t.Fatalf("resultSetIndex = %d, want 2", rows.resultSetIndex)
	}
	if rows.HasNextResultSet() {
		t.Fatalf("HasNextResultSet() = true, want false")
	}
	cols := rows.Columns()
	if len(cols) != 1 || cols[0] != "COL1" {
		t.Fatalf("Columns() = %v, want [COL1]", cols)
	}
	warnings := conn.Warnings()
	if warnings == nil {
		t.Fatalf("Warnings() = nil, want warning")
	}
	if warnings.Code != 1 || warnings.State != "01004" {
		t.Fatalf("Warnings() = %+v, want code 1 state 01004", warnings)
	}
	conn.ClearWarnings()
	if got := conn.Warnings(); got != nil {
		t.Fatalf("Warnings() after ClearWarnings() = %+v, want nil", got)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestQueryContextCallNextResultSet(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read prepare request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepare request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		stmtPayload, found, err := readTestLLCPValue(packet[40:], 0x3812)
		if err != nil {
			t.Errorf("parse prepare request body: %v", err)
			return
		}
		if !found || len(stmtPayload) != 2 || binary.BigEndian.Uint16(stmtPayload) != uint16(sqlStatementTypeCall) {
			t.Errorf("prepare statement type = %v, want CALL", stmtPayload)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read open request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse open request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionOpenDescribe {
			t.Errorf("open request id = 0x%x, want 0x%x", header.ReqRepID, FunctionOpenDescribe)
			return
		}
		openPayload := append(buildTestSQLCAPayload(t, 0, "", 2), buildTestDescribePayload()...)
		if _, err := server.Write(buildTestReplyPacket(openPayload)); err != nil {
			t.Errorf("write open reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		reusePayload, found, err := readTestLLCPValue(packet[40:], 0x3810)
		if err != nil {
			t.Errorf("parse close request body: %v", err)
			return
		}
		if !found || len(reusePayload) != 1 || reusePayload[0] != cursorReuseResultSet {
			t.Errorf("reuse indicator = %v, want %x", reusePayload, cursorReuseResultSet)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read second open request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse second open request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionOpenDescribe {
			t.Errorf("second open request id = 0x%x, want 0x%x", header.ReqRepID, FunctionOpenDescribe)
			return
		}
		secondOpenPayload := append(buildTestSQLCAPayload(t, 0, "", 1), buildTestDescribePayload()...)
		if _, err := server.Write(buildTestReplyPacket(secondOpenPayload)); err != nil {
			t.Errorf("write second open reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	rowsValue, err := conn.queryContext(context.Background(), "CALL QSYS2.DUMMY()", nil)
	if err != nil {
		t.Fatalf("queryContext() error = %v", err)
	}
	rows := rowsValue.(*queryRows)
	if !rows.HasNextResultSet() {
		t.Fatalf("HasNextResultSet() = false, want true")
	}
	if err := rows.NextResultSet(); err != nil {
		t.Fatalf("NextResultSet() error = %v", err)
	}
	if rows.resultSetIndex != 2 {
		t.Fatalf("resultSetIndex = %d, want 2", rows.resultSetIndex)
	}
	if rows.HasNextResultSet() {
		t.Fatalf("HasNextResultSet() = true, want false")
	}
	if cols := rows.Columns(); len(cols) != 1 || cols[0] != "COL1" {
		t.Fatalf("Columns() = %v, want [COL1]", cols)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestPreparedQueryUsesPreparedStatement(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read prepare request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepare request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		bitmap := binary.BigEndian.Uint32(packet[20:24])
		if bitmap&orsParameterMarkerFormat == 0 {
			t.Errorf("prepare request ORS bitmap = 0x%x, want parameter marker format bit", bitmap)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read open request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse open request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionOpenDescribe {
			t.Errorf("open request id = 0x%x, want 0x%x", header.ReqRepID, FunctionOpenDescribe)
			return
		}
		payload := append(buildTestSQLCAPayload(t, 0, "", 1), buildTestDescribePayload()...)
		if _, err := server.Write(buildTestReplyPacket(payload)); err != nil {
			t.Errorf("write open reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read cursor close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse cursor close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("cursor close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write cursor close reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read prepared statement close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepared statement close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("prepared statement close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write prepared statement close reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "values 1")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt, ok := stmtIface.(*Stmt)
	if !ok {
		t.Fatalf("PrepareContext() returned %T, want *Stmt", stmtIface)
	}
	if stmt.statementName == "" {
		t.Fatal("statementName = empty, want prepared statement name")
	}
	if got := stmt.NumInput(); got != 0 {
		t.Fatalf("NumInput() = %d, want 0", got)
	}

	rows, err := stmt.QueryContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestPreparedQueryWithArgsUsesPreparedBind(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		field := parameterMarkerField{SQLType: db2TypeInteger, Length: 4, Precision: 0, Scale: 0, CCSID: 37, ParameterType: 0xF0}

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read prepare request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepare request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(buildTestSQLCAPayload(t, 0, "", 0), buildTestPrepareParameterMarkerFormatPayload(field)...)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse change descriptor request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionChangeDescriptor {
			t.Errorf("change descriptor request id = 0x%x, want 0x%x", header.ReqRepID, FunctionChangeDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write change descriptor reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read open request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse open request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionOpenDescribe {
			t.Errorf("open request id = 0x%x, want 0x%x", header.ReqRepID, FunctionOpenDescribe)
			return
		}
		paramData, ok, err := readTestLLCPValue(packet[40:], CodePointParameterMarkerData)
		if err != nil {
			t.Errorf("read parameter marker data: %v", err)
			return
		}
		if !ok {
			t.Error("open request missing parameter marker data")
			return
		}
		if got := binary.BigEndian.Uint16(paramData[8:10]); got != 1 {
			t.Errorf("parameter marker column count = %d, want 1", got)
			return
		}
		if got := binary.BigEndian.Uint32(paramData[16:20]); got != 42 {
			t.Errorf("parameter marker value = %d, want 42", got)
			return
		}
		openPayload := append(buildTestSQLCAPayload(t, 0, "", 1), buildTestDescribePayload()...)
		if _, err := server.Write(buildTestReplyPacket(openPayload)); err != nil {
			t.Errorf("write open reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read delete descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse delete descriptor request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionDeleteDescriptor {
			t.Errorf("delete descriptor request id = 0x%x, want 0x%x", header.ReqRepID, FunctionDeleteDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write delete descriptor reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read cursor close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse cursor close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("cursor close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write cursor close reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read prepared statement close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepared statement close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("prepared statement close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write prepared statement close reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "values ?")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt, ok := stmtIface.(*Stmt)
	if !ok {
		t.Fatalf("PrepareContext() returned %T, want *Stmt", stmtIface)
	}
	if got := stmt.NumInput(); got != 1 {
		t.Fatalf("NumInput() = %d, want 1", got)
	}

	rows, err := stmt.QueryContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: int64(42)}})
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestPreparedExecWithArgsUsesPreparedBind(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		field := parameterMarkerField{SQLType: db2TypeInteger, Length: 4, Precision: 0, Scale: 0, CCSID: 37, ParameterType: 0xF0}

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read prepare request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepare request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(buildTestSQLCAPayload(t, 0, "", 0), buildTestPrepareParameterMarkerFormatPayload(field)...)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse change descriptor request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionChangeDescriptor {
			t.Errorf("change descriptor request id = 0x%x, want 0x%x", header.ReqRepID, FunctionChangeDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write change descriptor reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read execute request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse execute request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("execute request id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
			return
		}
		paramData, ok, err := readTestLLCPValue(packet[40:], CodePointParameterMarkerData)
		if err != nil {
			t.Errorf("read parameter marker data: %v", err)
			return
		}
		if !ok {
			t.Error("execute request missing parameter marker data")
			return
		}
		if got := binary.BigEndian.Uint32(paramData[16:20]); got != 42 {
			t.Errorf("execute parameter value = %d, want 42", got)
			return
		}
		executePayload := buildTestSQLCAPayload(t, 0, "", 0)
		if _, err := server.Write(buildTestReplyPacket(executePayload)); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read delete descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse delete descriptor request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionDeleteDescriptor {
			t.Errorf("delete descriptor request id = 0x%x, want 0x%x", header.ReqRepID, FunctionDeleteDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write delete descriptor reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read prepared statement close request: %v", err)
			return
		}
		header, err = ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse prepared statement close request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("prepared statement close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write prepared statement close reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "insert into qtemp.t1 values (?)")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt, ok := stmtIface.(*Stmt)
	if !ok {
		t.Fatalf("PrepareContext() returned %T, want *Stmt", stmtIface)
	}
	if got := stmt.NumInput(); got != 1 {
		t.Fatalf("NumInput() = %d, want 1", got)
	}

	result, err := stmt.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: int64(42)}})
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if rowsAffected, err := result.RowsAffected(); err != nil || rowsAffected != 0 {
		t.Fatalf("RowsAffected() = (%d, %v), want (0, nil)", rowsAffected, err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("stmt.Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestConnExecContextCallSetsOutParameter(t *testing.T) {
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
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse prepare header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(buildTestSQLCAPayload(t, 0, "", 0), buildTestPrepareParameterMarkerFormatPayloads(
			parameterMarkerField{SQLType: db2TypeInteger, Length: 4, Precision: 0, Scale: 0, CCSID: 37, ParameterType: 0xF0},
			parameterMarkerField{SQLType: db2TypeInteger, Length: 4, Precision: 0, Scale: 0, CCSID: 37, ParameterType: 0xF1},
		)...)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse change descriptor header: %v", err)
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
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse execute header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("execute request id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
			return
		}
		paramData, found, err := readTestLLCPValue(packet[40:], CodePointParameterMarkerData)
		if err != nil {
			t.Errorf("read parameter marker data: %v", err)
			return
		}
		if !found {
			t.Errorf("execute request missing parameter marker data")
			return
		}
		if got := binary.BigEndian.Uint16(paramData[16:18]); got == 0xFFFF {
			t.Errorf("output parameter indicator = 0x%x, want non-null", got)
			return
		}
		if got := binary.BigEndian.Uint32(paramData[18:22]); got != 7 {
			t.Errorf("input parameter value = %d, want 7", got)
			return
		}
		executePayload := append(buildTestSQLCAPayload(t, 0, "", 0), buildTestResultDataPayload(1, 4, [][]byte{{0, 0, 0, 42}}, []bool{false})...)
		if _, err := server.Write(buildTestReplyPacket(executePayload)); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read delete descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse delete descriptor header: %v", err)
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
			t.Errorf("read close request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse close header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	var outValue int64
	_, err := conn.ExecContext(context.Background(), "CALL MYPROC(?, ?)", []driver.NamedValue{{Ordinal: 1, Value: int64(7)}, {Ordinal: 2, Value: sql.Out{Dest: &outValue}}})
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if outValue != 42 {
		t.Fatalf("outValue = %d, want 42", outValue)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestStmtExecContextWritesBlobLocatorData(t *testing.T) {
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
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse prepare header: %v", err)
			return
		}
		if header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("prepare request id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(buildTestSQLCAPayload(t, 0, "", 0), buildTestExtendedParameterMarkerFormatPayloads(
			parameterMarkerField{SQLType: db2TypeBlob, Length: 4, Precision: 0, Scale: 0, CCSID: 65535, ParameterType: 0xF0, LOBLocator: 0x01020304, LOBMaxSize: 32},
		)...)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse change descriptor header: %v", err)
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
			t.Errorf("read write lob request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse write lob header: %v", err)
			return
		}
		if header.ReqRepID != FunctionWriteLobData {
			t.Errorf("write lob request id = 0x%x, want 0x%x", header.ReqRepID, FunctionWriteLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointLOBLocatorHandle); got != 0x01020304 {
			t.Errorf("lob locator handle = 0x%x, want 0x01020304", got)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); got != 3 {
			t.Errorf("requested size = %d, want 3", got)
			return
		}
		lobData, found, err := readTestLLCPValue(packet[40:], 0x381D)
		if err != nil {
			t.Errorf("read write lob payload: %v", err)
			return
		}
		if !found {
			t.Errorf("write lob request missing LOB payload")
			return
		}
		if string(lobData) != string([]byte{1, 2, 3}) {
			t.Errorf("lob payload = %v, want [1 2 3]", lobData)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write write lob reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read execute request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse execute header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("execute request id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
			return
		}
		paramData, found, err := readTestLLCPValue(packet[40:], CodePointParameterMarkerDataExt)
		if err != nil {
			t.Errorf("read extended parameter marker data: %v", err)
			return
		}
		if !found {
			t.Errorf("execute request missing extended parameter marker data")
			return
		}
		if got := binary.BigEndian.Uint32(paramData[22:26]); got != 0x01020304 {
			t.Errorf("execute locator handle = 0x%x, want 0x01020304", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read delete descriptor request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse delete descriptor header: %v", err)
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
			t.Errorf("read close request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse close header: %v", err)
			return
		}
		if header.ReqRepID != FunctionClose {
			t.Errorf("close request id = 0x%x, want 0x%x", header.ReqRepID, FunctionClose)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write close reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "insert into qtemp.t1 values (?)")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt := stmtIface.(*Stmt)
	_, err = stmt.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: []byte{1, 2, 3}}})
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestAssignCallOutputParametersRetrievesBlobOutput(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read retrieve length request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse retrieve length header: %v", err)
			return
		}
		if header.ReqRepID != FunctionRetrieveLobData {
			t.Errorf("retrieve length request id = 0x%x, want 0x%x", header.ReqRepID, FunctionRetrieveLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); got != 0 {
			t.Errorf("requested size = %d, want 0", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(appendTestLLCP(nil, CodePointCurrentLOBLength, append(appendU16(nil, 4), appendU32(nil, 3)...)))); err != nil {
			t.Errorf("write retrieve length reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read retrieve data request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse retrieve data header: %v", err)
			return
		}
		if header.ReqRepID != FunctionRetrieveLobData {
			t.Errorf("retrieve data request id = 0x%x, want 0x%x", header.ReqRepID, FunctionRetrieveLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); got != 3 {
			t.Errorf("requested size = %d, want 3", got)
			return
		}
		lobPayload := make([]byte, 0, 16)
		lobPayload = appendU16(lobPayload, 37)
		lobPayload = appendU32(lobPayload, 3)
		lobPayload = append(lobPayload, []byte{9, 8, 7}...)
		if _, err := server.Write(buildTestReplyPacket(appendTestLLCP(nil, CodePointLOBLocatorData, lobPayload))); err != nil {
			t.Errorf("write retrieve data reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read free lob request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse free lob header: %v", err)
			return
		}
		if header.ReqRepID != FunctionFreeLob {
			t.Errorf("free lob request id = 0x%x, want 0x%x", header.ReqRepID, FunctionFreeLob)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write free lob reply: %v", err)
			return
		}
	}()

	var out []byte
	bindings := []parameterBinding{{
		field:   parameterMarkerField{SQLType: db2TypeBlob, Length: 16, CCSID: 65535, ParameterType: 0xF1, LOBLocator: 0x01020304, LOBMaxSize: 16},
		outDest: &out,
	}}
	row := make([]byte, 4)
	binary.BigEndian.PutUint32(row, 0x01020304)
	payload := buildTestResultDataPayload(1, 4, [][]byte{row}, []bool{false})
	if err := assignCallOutputParameters(context.Background(), &Conn{netConn: client, autoCommit: true}, payload, bindings); err != nil {
		t.Fatalf("assignCallOutputParameters() error = %v", err)
	}
	if string(out) != string([]byte{9, 8, 7}) {
		t.Fatalf("out = %v, want [9 8 7]", out)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func TestQueryRowsRetrievesBlobLocator(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read retrieve length request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse retrieve length header: %v", err)
			return
		}
		if header.ReqRepID != FunctionRetrieveLobData {
			t.Errorf("retrieve length request id = 0x%x, want 0x%x", header.ReqRepID, FunctionRetrieveLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); got != 0 {
			t.Errorf("requested size = %d, want 0", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(appendTestLLCP(nil, CodePointCurrentLOBLength, append(appendU16(nil, 4), appendU32(nil, 5)...)))); err != nil {
			t.Errorf("write retrieve length reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read retrieve data request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse retrieve data header: %v", err)
			return
		}
		if header.ReqRepID != FunctionRetrieveLobData {
			t.Errorf("retrieve data request id = 0x%x, want 0x%x", header.ReqRepID, FunctionRetrieveLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); got != 5 {
			t.Errorf("requested size = %d, want 5", got)
			return
		}
		lobPayload := make([]byte, 0, 16)
		lobPayload = appendU16(lobPayload, 37)
		lobPayload = appendU32(lobPayload, 5)
		lobPayload = append(lobPayload, []byte("hello")...)
		if _, err := server.Write(buildTestReplyPacket(appendTestLLCP(nil, CodePointLOBLocatorData, lobPayload))); err != nil {
			t.Errorf("write retrieve data reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read free lob request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse free lob header: %v", err)
			return
		}
		if header.ReqRepID != FunctionFreeLob {
			t.Errorf("free lob request id = 0x%x, want 0x%x", header.ReqRepID, FunctionFreeLob)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write free lob reply: %v", err)
			return
		}
	}()

	locatorRow := make([]byte, 4)
	binary.BigEndian.PutUint32(locatorRow, 0x12345678)
	rows := &queryRows{
		conn:  &Conn{netConn: client, autoCommit: true},
		ctx:   context.Background(),
		meta:  &resultSetMeta{columns: []columnMeta{{Name: "LOB", Type: db2TypeBlobLocator, Length: 4, Offset: 0, LobMaxSize: 5}}},
		block: &fetchBlock{RowCount: 1, ColumnCount: 1, IndicatorSize: 2, RowSize: 4, Nulls: []bool{false}, Data: locatorRow},
	}

	var dest [1]driver.Value
	if err := rows.decodeCurrentRow(dest[:]); err != nil {
		t.Fatalf("decodeCurrentRow() error = %v", err)
	}
	bytesValue, ok := dest[0].([]byte)
	if !ok {
		t.Fatalf("dest[0] type = %T, want []byte", dest[0])
	}
	if string(bytesValue) != "hello" {
		t.Fatalf("dest[0] = %q, want %q", string(bytesValue), "hello")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not finish")
	}
}

func buildTestPrepareParameterMarkerFormatPayloads(fields ...parameterMarkerField) []byte {
	data := make([]byte, 8+len(fields)*54)
	binary.BigEndian.PutUint32(data[0:4], 1)
	binary.BigEndian.PutUint16(data[4:6], uint16(len(fields)))
	recordSize := 0
	for _, field := range fields {
		recordSize += field.Length
	}
	binary.BigEndian.PutUint16(data[6:8], uint16(recordSize))
	for i, field := range fields {
		base := 8 + i*54
		binary.BigEndian.PutUint16(data[base+0:], 54)
		binary.BigEndian.PutUint16(data[base+2:], uint16(field.SQLType))
		binary.BigEndian.PutUint16(data[base+4:], uint16(field.Length))
		binary.BigEndian.PutUint16(data[base+6:], uint16(field.Scale))
		binary.BigEndian.PutUint16(data[base+8:], uint16(field.Precision))
		binary.BigEndian.PutUint16(data[base+10:], uint16(field.CCSID))
		data[base+12] = byte(field.ParameterType)
	}
	return appendTestLLCP(nil, CodePointParameterMarkerFormat, data)
}

func buildTestExtendedParameterMarkerFormatPayloads(fields ...parameterMarkerField) []byte {
	data := make([]byte, 16+len(fields)*64)
	binary.BigEndian.PutUint32(data[0:4], 1)
	binary.BigEndian.PutUint32(data[4:8], uint32(len(fields)))
	recordSize := 0
	for _, field := range fields {
		recordSize += field.Length
	}
	binary.BigEndian.PutUint32(data[12:16], uint32(recordSize))
	for i, field := range fields {
		base := 16 + i*64
		binary.BigEndian.PutUint16(data[base+0:], 64)
		binary.BigEndian.PutUint16(data[base+2:], uint16(field.SQLType))
		binary.BigEndian.PutUint32(data[base+4:], uint32(field.Length))
		binary.BigEndian.PutUint16(data[base+8:], uint16(field.Scale))
		binary.BigEndian.PutUint16(data[base+10:], uint16(field.Precision))
		binary.BigEndian.PutUint16(data[base+12:], uint16(field.CCSID))
		data[base+14] = byte(field.ParameterType)
		binary.BigEndian.PutUint32(data[base+17:], uint32(field.LOBLocator))
		binary.BigEndian.PutUint32(data[base+26:], uint32(field.LOBMaxSize))
	}
	return appendTestLLCP(nil, CodePointExtendedParameterMarker, data)
}

func buildTestResultDataPayload(columnCount int, rowSize int, rows [][]byte, nulls []bool) []byte {
	data := make([]byte, 0, 64)
	data = appendU32(data, 1)
	data = appendU32(data, uint32(len(rows)))
	data = appendU16(data, uint16(columnCount))
	data = appendU16(data, 2)
	data = appendU32(data, 0)
	data = appendU32(data, uint32(rowSize))
	for _, isNull := range nulls {
		indicator := uint16(0)
		if isNull {
			indicator = 0xFFFF
		}
		data = appendU16(data, indicator)
	}
	for _, row := range rows {
		data = append(data, row...)
	}
	return appendTestLLCP(nil, CodePointExtendedResultData, data)
}

func requestBodyIntValue(t *testing.T, packet []byte, codePoint uint16) uint32 {
	t.Helper()
	payload, found, err := readTestLLCPValue(packet[40:], codePoint)
	if err != nil {
		t.Fatalf("readTestLLCPValue(0x%x) error = %v", codePoint, err)
	}
	if !found {
		t.Fatalf("attribute 0x%x not found", codePoint)
	}
	if len(payload) != 4 {
		t.Fatalf("attribute 0x%x payload length = %d, want 4", codePoint, len(payload))
	}
	return binary.BigEndian.Uint32(payload)
}

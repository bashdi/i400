package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Unit tests: encodeLOBParameterValue
// ---------------------------------------------------------------------------

// TestEncodeLOBParameterValueBlobBytes verifies that a BLOB parameter with a
// []byte value creates a lobWriteRequest containing the exact bytes and the
// correct locator handle.
func TestEncodeLOBParameterValueBlobBytes(t *testing.T) {
	field := parameterMarkerField{
		SQLType:       db2TypeBlob,
		Length:        4,
		CCSID:         65535,
		ParameterType: 0xF0,
		LOBLocator:    0x0A0B0C0D,
		LOBMaxSize:    1024,
	}
	data := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	raw, lobWrite, err := encodeLOBParameterValue(field, driver.Value(data), true, false)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite == nil {
		t.Fatal("lobWrite is nil, want non-nil")
	}
	if got := binary.BigEndian.Uint32(raw); got != 0x0A0B0C0D {
		t.Errorf("raw locator = 0x%x, want 0x0A0B0C0D", got)
	}
	if lobWrite.locatorHandle != 0x0A0B0C0D {
		t.Errorf("locatorHandle = 0x%x, want 0x0A0B0C0D", lobWrite.locatorHandle)
	}
	if string(lobWrite.data) != string(data) {
		t.Errorf("lobWrite.data = %v, want %v", lobWrite.data, data)
	}
	if lobWrite.requestedSize != len(data) {
		t.Errorf("requestedSize = %d, want %d", lobWrite.requestedSize, len(data))
	}
}

// TestEncodeLOBParameterValueBlobHexString verifies that a BLOB parameter
// accepts a hex-encoded string (e.g. "0x0102") and decodes it to bytes.
func TestEncodeLOBParameterValueBlobHexString(t *testing.T) {
	field := parameterMarkerField{
		SQLType:       db2TypeBlob,
		Length:        4,
		CCSID:         65535,
		ParameterType: 0xF0,
		LOBLocator:    1,
		LOBMaxSize:    1024,
	}
	_, lobWrite, err := encodeLOBParameterValue(field, driver.Value("0x0102"), true, false)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite == nil {
		t.Fatal("lobWrite is nil")
	}
	want := []byte{0x01, 0x02}
	if string(lobWrite.data) != string(want) {
		t.Errorf("lobWrite.data = %v, want %v", lobWrite.data, want)
	}
	if lobWrite.requestedSize != 2 {
		t.Errorf("requestedSize = %d, want 2", lobWrite.requestedSize)
	}
}

// TestEncodeLOBParameterValueClobText verifies that a CLOB parameter encodes
// the string using the field's CCSID and sets requestedSize to the byte length.
func TestEncodeLOBParameterValueClobText(t *testing.T) {
	const text = "Hello"
	const ccsid = 1208 // UTF-8
	field := parameterMarkerField{
		SQLType:       db2TypeClob,
		Length:        4,
		CCSID:         ccsid,
		ParameterType: 0xF0,
		LOBLocator:    7,
		LOBMaxSize:    1024,
	}
	_, lobWrite, err := encodeLOBParameterValue(field, driver.Value(text), true, false)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite == nil {
		t.Fatal("lobWrite is nil")
	}
	want, err := encodeTextBytes(text, ccsid)
	if err != nil {
		t.Fatalf("encodeTextBytes() error = %v", err)
	}
	if string(lobWrite.data) != string(want) {
		t.Errorf("lobWrite.data = %v, want %v", lobWrite.data, want)
	}
	if lobWrite.requestedSize != len(want) {
		t.Errorf("requestedSize = %d, want %d", lobWrite.requestedSize, len(want))
	}
}

// TestEncodeLOBParameterValueXMLText verifies XML parameters are encoded using
// the field's CCSID (typically 1208 = UTF-8).
func TestEncodeLOBParameterValueXMLText(t *testing.T) {
	const text = "<root/>"
	const ccsid = 1208 // UTF-8
	field := parameterMarkerField{
		SQLType:       db2TypeXML,
		Length:        4,
		CCSID:         ccsid,
		ParameterType: 0xF0,
		LOBLocator:    42,
		LOBMaxSize:    65536,
	}
	_, lobWrite, err := encodeLOBParameterValue(field, driver.Value(text), true, false)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite == nil {
		t.Fatal("lobWrite is nil")
	}
	want, err := encodeTextBytes(text, ccsid)
	if err != nil {
		t.Fatalf("encodeTextBytes() error = %v", err)
	}
	if string(lobWrite.data) != string(want) {
		t.Errorf("lobWrite.data = %v, want %v", lobWrite.data, want)
	}
	if lobWrite.requestedSize != len(want) {
		t.Errorf("requestedSize = %d, want %d", lobWrite.requestedSize, len(want))
	}
}

// TestEncodeLOBParameterValueDbclobRequestedSizeIsHalfByteLength verifies that
// DBCLOB requestedSize is measured in double-byte characters, not bytes.
func TestEncodeLOBParameterValueDbclobRequestedSizeIsHalfByteLength(t *testing.T) {
	const text = "Hi"
	const ccsid = 1200 // UTF-16BE
	field := parameterMarkerField{
		SQLType:       db2TypeDbclob,
		Length:        4,
		CCSID:         ccsid,
		ParameterType: 0xF0,
		LOBLocator:    5,
		LOBMaxSize:    65536,
	}
	_, lobWrite, err := encodeLOBParameterValue(field, driver.Value(text), true, false)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite == nil {
		t.Fatal("lobWrite is nil")
	}
	wantBytes, err := encodeTextBytes(text, ccsid)
	if err != nil {
		t.Fatalf("encodeTextBytes() error = %v", err)
	}
	if string(lobWrite.data) != string(wantBytes) {
		t.Errorf("lobWrite.data = %v, want %v", lobWrite.data, wantBytes)
	}
	wantSize := len(wantBytes) / 2
	if lobWrite.requestedSize != wantSize {
		t.Errorf("requestedSize = %d, want %d (len=%d bytes / 2)", lobWrite.requestedSize, wantSize, len(wantBytes))
	}
}

// TestEncodeLOBParameterValueNullInput verifies that a null value (isNull=true)
// produces lobWrite=nil while raw still contains the locator handle.
func TestEncodeLOBParameterValueNullInput(t *testing.T) {
	field := parameterMarkerField{
		SQLType:       db2TypeBlob,
		Length:        4,
		CCSID:         65535,
		ParameterType: 0xF0,
		LOBLocator:    99,
		LOBMaxSize:    1024,
	}
	raw, lobWrite, err := encodeLOBParameterValue(field, nil, false, true)
	if err != nil {
		t.Fatalf("encodeLOBParameterValue() error = %v", err)
	}
	if lobWrite != nil {
		t.Errorf("lobWrite = %v, want nil for null value", lobWrite)
	}
	if len(raw) != 4 {
		t.Errorf("raw length = %d, want 4", len(raw))
	}
	if got := binary.BigEndian.Uint32(raw); got != 99 {
		t.Errorf("raw locator = %d, want 99", got)
	}
}

// TestEncodeLOBParameterValueNoLocatorReturnsUnsupported verifies that a LOB
// field without a server-provided locator handle returns
// errPreparedStatementBindingUnsupported.  This happens when the server
// responds with the original (non-extended) parameter marker format which
// initialises LOBLocator to -1.
func TestEncodeLOBParameterValueNoLocatorReturnsUnsupported(t *testing.T) {
	field := parameterMarkerField{
		SQLType:       db2TypeBlob,
		Length:        4,
		CCSID:         65535,
		ParameterType: 0xF0,
		LOBLocator:    -1, // default when server does not provide a locator
	}
	_, _, err := encodeLOBParameterValue(field, driver.Value([]byte{1}), true, false)
	if err == nil {
		t.Fatal("expected error for LOBLocator=-1, got nil")
	}
	if !errors.Is(err, errPreparedStatementBindingUnsupported) {
		t.Errorf("error = %v, want errPreparedStatementBindingUnsupported", err)
	}
}

// ---------------------------------------------------------------------------
// Unit tests: buildParameterBindings with LOB fields
// ---------------------------------------------------------------------------

// TestBuildParameterBindingsNullLOBSetsNullIndicator verifies that passing nil
// for a LOB parameter sets isNull=true on the binding so the server receives
// the null indicator (0xFFFF) in the Execute request.
func TestBuildParameterBindingsNullLOBSetsNullIndicator(t *testing.T) {
	format := &parameterMarkerFormat{
		codePoint:  CodePointExtendedParameterMarker,
		RecordSize: 4,
		Fields: []parameterMarkerField{
			{SQLType: db2TypeBlob, Length: 4, CCSID: 65535, ParameterType: 0xF0, LOBLocator: 0xCAFEBABE, LOBMaxSize: 1024},
		},
	}
	args := []driver.NamedValue{{Ordinal: 1, Value: nil}}
	bindings, err := buildParameterBindings(format, args)
	if err != nil {
		t.Fatalf("buildParameterBindings() error = %v", err)
	}
	if !bindings[0].isNull {
		t.Error("bindings[0].isNull = false, want true for nil LOB value")
	}
	if bindings[0].lobWrite != nil {
		t.Errorf("bindings[0].lobWrite = %v, want nil for null value", bindings[0].lobWrite)
	}
}

// ---------------------------------------------------------------------------
// Protocol tests
// ---------------------------------------------------------------------------

// TestStmtExecContextWritesClobLocatorData verifies the full wire sequence for
// inserting a CLOB column: PrepareDescribe → ChangeDescriptor → WriteLobData
// (with CCSID-encoded text content) → Execute.
func TestStmtExecContextWritesClobLocatorData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	const clobText = "Hello CLOB"
	const clobCCSID = 1208 // UTF-8
	const locator = uint32(0x11223344)

	expectedBytes, err := encodeTextBytes(clobText, clobCCSID)
	if err != nil {
		t.Fatalf("encodeTextBytes: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		// PrepareDescribe
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
			t.Errorf("prepare id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(
			buildTestSQLCAPayload(t, 0, "", 0),
			buildTestExtendedParameterMarkerFormatPayloads(
				parameterMarkerField{SQLType: db2TypeClob, Length: 4, CCSID: clobCCSID, ParameterType: 0xF0, LOBLocator: int(locator), LOBMaxSize: 65536},
			)...,
		)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		// ChangeDescriptor
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse change descriptor header: %v", err)
			return
		}
		if header.ReqRepID != FunctionChangeDescriptor {
			t.Errorf("change descriptor id = 0x%x, want 0x%x", header.ReqRepID, FunctionChangeDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write change descriptor reply: %v", err)
			return
		}

		// WriteLobData
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
			t.Errorf("write lob id = 0x%x, want 0x%x (got WriteLobData?)", header.ReqRepID, FunctionWriteLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointLOBLocatorHandle); got != locator {
			t.Errorf("lob locator = 0x%x, want 0x%x", got, locator)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointRequestedSize); int(got) != len(expectedBytes) {
			t.Errorf("requested size = %d, want %d", got, len(expectedBytes))
			return
		}
		lobData, found, err := readTestLLCPValue(packet[40:], 0x381D)
		if err != nil {
			t.Errorf("read lob payload: %v", err)
			return
		}
		if !found {
			t.Error("write lob request missing LOB payload")
			return
		}
		if string(lobData) != string(expectedBytes) {
			t.Errorf("lob payload = %v, want %v", lobData, expectedBytes)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write write lob reply: %v", err)
			return
		}

		// Execute
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read execute: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse execute header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("execute id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		// DeleteDescriptor (deferred) + Close (stmt.Close)
		for _, wantID := range []uint16{FunctionDeleteDescriptor, FunctionClose} {
			pkt, err := readPacket(server)
			if err != nil {
				t.Errorf("read 0x%x: %v", wantID, err)
				return
			}
			hdr, err := ParseHeader(pkt)
			if err != nil {
				t.Errorf("parse header: %v", err)
				return
			}
			if hdr.ReqRepID != wantID {
				t.Errorf("request id = 0x%x, want 0x%x", hdr.ReqRepID, wantID)
				return
			}
			if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
				t.Errorf("write reply: %v", err)
				return
			}
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "INSERT INTO t (c) VALUES (?)")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt := stmtIface.(*Stmt)
	_, err = stmt.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: clobText}})
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

// TestStmtExecContextWritesXMLLocatorData verifies that an XML column value is
// transmitted via WriteLobData with the correct CCSID-encoded content before
// the Execute request is sent.
func TestStmtExecContextWritesXMLLocatorData(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	const xmlText = "<root><val>1</val></root>"
	const xmlCCSID = 1208 // UTF-8
	const locator = uint32(0xAABBCCDD)

	expectedBytes, err := encodeTextBytes(xmlText, xmlCCSID)
	if err != nil {
		t.Fatalf("encodeTextBytes: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		// PrepareDescribe
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
			t.Errorf("prepare id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(
			buildTestSQLCAPayload(t, 0, "", 0),
			buildTestExtendedParameterMarkerFormatPayloads(
				parameterMarkerField{SQLType: db2TypeXML, Length: 4, CCSID: xmlCCSID, ParameterType: 0xF0, LOBLocator: int(locator), LOBMaxSize: 65536},
			)...,
		)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		// ChangeDescriptor
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse change descriptor header: %v", err)
			return
		}
		if header.ReqRepID != FunctionChangeDescriptor {
			t.Errorf("change descriptor id = 0x%x, want 0x%x", header.ReqRepID, FunctionChangeDescriptor)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write change descriptor reply: %v", err)
			return
		}

		// WriteLobData
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
			t.Errorf("write lob id = 0x%x, want 0x%x", header.ReqRepID, FunctionWriteLobData)
			return
		}
		if got := requestBodyIntValue(t, packet, CodePointLOBLocatorHandle); got != locator {
			t.Errorf("lob locator = 0x%x, want 0x%x", got, locator)
			return
		}
		lobData, found, err := readTestLLCPValue(packet[40:], 0x381D)
		if err != nil {
			t.Errorf("read lob payload: %v", err)
			return
		}
		if !found {
			t.Error("write lob request missing LOB payload")
			return
		}
		if string(lobData) != string(expectedBytes) {
			t.Errorf("lob payload = %q, want %q", lobData, expectedBytes)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write write lob reply: %v", err)
			return
		}

		// Execute
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read execute: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse execute header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("execute id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		// DeleteDescriptor + Close
		for _, wantID := range []uint16{FunctionDeleteDescriptor, FunctionClose} {
			pkt, err := readPacket(server)
			if err != nil {
				t.Errorf("read 0x%x: %v", wantID, err)
				return
			}
			hdr, err := ParseHeader(pkt)
			if err != nil {
				t.Errorf("parse header: %v", err)
				return
			}
			if hdr.ReqRepID != wantID {
				t.Errorf("request id = 0x%x, want 0x%x", hdr.ReqRepID, wantID)
				return
			}
			if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
				t.Errorf("write reply: %v", err)
				return
			}
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "INSERT INTO t (x) VALUES (?)")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt := stmtIface.(*Stmt)
	_, err = stmt.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: xmlText}})
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

// TestStmtExecContextNullBlobSetsNullIndicator verifies that passing nil for a
// BLOB parameter:
//   - does NOT trigger a WriteLobData request (no data to write), and
//   - sets the null indicator (0xFFFF) in the Execute request's parameter data.
func TestStmtExecContextNullBlobSetsNullIndicator(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	const locator = uint32(0x01020304)

	done := make(chan struct{})
	go func() {
		defer close(done)

		if !consumeCreateRPBPreamble(t, server) {
			return
		}

		// PrepareDescribe
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
			t.Errorf("prepare id = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
			return
		}
		preparePayload := append(
			buildTestSQLCAPayload(t, 0, "", 0),
			buildTestExtendedParameterMarkerFormatPayloads(
				parameterMarkerField{SQLType: db2TypeBlob, Length: 4, CCSID: 65535, ParameterType: 0xF0, LOBLocator: int(locator), LOBMaxSize: 1024},
			)...,
		)
		if _, err := server.Write(buildTestReplyPacket(preparePayload)); err != nil {
			t.Errorf("write prepare reply: %v", err)
			return
		}

		// ChangeDescriptor
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read change descriptor: %v", err)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write change descriptor reply: %v", err)
			return
		}

		// The very next request must be Execute, NOT WriteLobData.
		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read execute: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse execute header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecute {
			t.Errorf("expected Execute (0x%x) immediately after ChangeDescriptor for null BLOB, got 0x%x (WriteLobData = 0x%x)",
				FunctionExecute, header.ReqRepID, FunctionWriteLobData)
			return
		}

		// Verify the null indicator is 0xFFFF in the extended parameter data.
		paramData, found, err := readTestLLCPValue(packet[40:], CodePointParameterMarkerDataExt)
		if err != nil {
			t.Errorf("read parameter marker data ext: %v", err)
			return
		}
		if !found {
			t.Error("execute request missing extended parameter marker data")
			return
		}
		// Extended data layout: [0:4]=version, [4:8]=rows, [8:10]=colCount,
		// [10:12]=indicatorSize(2), [12:16]=pad, [16:20]=dataLength,
		// [20:22]=nullIndicator[0], [22:26]=data[0]
		if len(paramData) < 22 {
			t.Errorf("paramData too short: %d bytes", len(paramData))
			return
		}
		if got := binary.BigEndian.Uint16(paramData[20:22]); got != 0xFFFF {
			t.Errorf("null indicator = 0x%04x, want 0xFFFF for nil BLOB", got)
		}

		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write execute reply: %v", err)
			return
		}

		// DeleteDescriptor + Close
		for _, wantID := range []uint16{FunctionDeleteDescriptor, FunctionClose} {
			pkt, err := readPacket(server)
			if err != nil {
				t.Errorf("read 0x%x: %v", wantID, err)
				return
			}
			hdr, err := ParseHeader(pkt)
			if err != nil {
				t.Errorf("parse header: %v", err)
				return
			}
			if hdr.ReqRepID != wantID {
				t.Errorf("request id = 0x%x, want 0x%x", hdr.ReqRepID, wantID)
				return
			}
			if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
				t.Errorf("write reply: %v", err)
				return
			}
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	stmtIface, err := conn.PrepareContext(context.Background(), "INSERT INTO t (b) VALUES (?)")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	stmt := stmtIface.(*Stmt)
	_, err = stmt.ExecContext(context.Background(), []driver.NamedValue{{Ordinal: 1, Value: nil}})
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

package i400

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestBuildTestConnectionRequest(t *testing.T) {
	request := buildTestConnectionRequest()
	if len(request) != 40 {
		t.Fatalf("len(buildTestConnectionRequest()) = %d, want 40", len(request))
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionTestConnection {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionTestConnection)
	}
	if header.TemplateLength != 20 {
		t.Fatalf("TemplateLength = %d, want 20", header.TemplateLength)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != orsSendReplyImmediately {
		t.Fatalf("ORS bitmap = 0x%x, want 0x%x", got, orsSendReplyImmediately)
	}
}

func TestBuildPrepareAndDescribeRequestIncludesParameterMarkerFormat(t *testing.T) {
	request, err := buildPrepareAndDescribeRequest(0x1234, "STMT0001", "values 1")
	if err != nil {
		t.Fatalf("buildPrepareAndDescribeRequest() error = %v", err)
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionPrepareDescribe {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionPrepareDescribe)
	}
	bitmap := binary.BigEndian.Uint32(request[20:24])
	if bitmap&orsParameterMarkerFormat == 0 {
		t.Fatalf("ORS bitmap = 0x%x, want parameter marker format bit set", bitmap)
	}
	if got := binary.BigEndian.Uint16(request[34:36]); got != 0x1234 {
		t.Fatalf("RPB handle = 0x%x, want 0x1234", got)
	}
}

func TestBuildPreparedExecuteRequestsIncludeDescriptorAndData(t *testing.T) {
	field := parameterMarkerField{SQLType: db2TypeInteger, Length: 4, Precision: 0, Scale: 0, CCSID: 37, ParameterType: 0xF0}
	format := &parameterMarkerFormat{codePoint: CodePointParameterMarkerFormat, Fields: []parameterMarkerField{field}}
	bindings := []parameterBinding{{field: field, raw: []byte{0x00, 0x00, 0x00, 0x2A}}}

	changeRequest, err := buildChangeDescriptorRequest(0x1234, 0x5678, format, bindings)
	if err != nil {
		t.Fatalf("buildChangeDescriptorRequest() error = %v", err)
	}
	header, err := ParseHeader(changeRequest)
	if err != nil {
		t.Fatalf("ParseHeader(changeRequest) error = %v", err)
	}
	if header.ReqRepID != FunctionChangeDescriptor {
		t.Fatalf("change request id = 0x%x, want 0x%x", header.ReqRepID, FunctionChangeDescriptor)
	}
	if got := binary.BigEndian.Uint16(changeRequest[34:36]); got != 0x1234 {
		t.Fatalf("change request rpb handle = 0x%x, want 0x1234", got)
	}
	if got := binary.BigEndian.Uint16(changeRequest[36:38]); got != 0x5678 {
		t.Fatalf("change request pm handle = 0x%x, want 0x5678", got)
	}

	executeRequest, err := buildExecutePreparedRequest(0x1234, 0x5678, sqlStatementTypeInsertUpdateDelete, format, bindings)
	if err != nil {
		t.Fatalf("buildExecutePreparedRequest() error = %v", err)
	}
	header, err = ParseHeader(executeRequest)
	if err != nil {
		t.Fatalf("ParseHeader(executeRequest) error = %v", err)
	}
	if header.ReqRepID != FunctionExecute {
		t.Fatalf("execute request id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
	}
	if got := binary.BigEndian.Uint16(executeRequest[34:36]); got != 0x1234 {
		t.Fatalf("execute request rpb handle = 0x%x, want 0x1234", got)
	}
	if got := binary.BigEndian.Uint16(executeRequest[36:38]); got != 0x5678 {
		t.Fatalf("execute request pm handle = 0x%x, want 0x5678", got)
	}
	parameterData, ok, err := readTestLLCPValue(executeRequest[40:], CodePointParameterMarkerData)
	if err != nil {
		t.Fatalf("readTestLLCPValue() error = %v", err)
	}
	if !ok {
		t.Fatal("execute request missing parameter marker data")
	}
	if got := binary.BigEndian.Uint16(parameterData[8:10]); got != 1 {
		t.Fatalf("parameter column count = %d, want 1", got)
	}
	if got := binary.BigEndian.Uint16(parameterData[12:14]); got != 4 {
		t.Fatalf("parameter row size = %d, want 4", got)
	}
}

func TestParseParameterMarkerFormatExtended(t *testing.T) {
	data := make([]byte, 16+64)
	binary.BigEndian.PutUint32(data[0:4], 1)
	binary.BigEndian.PutUint32(data[4:8], 1)
	binary.BigEndian.PutUint32(data[12:16], 64)

	base := 16
	binary.BigEndian.PutUint16(data[base+0:], 64)
	binary.BigEndian.PutUint16(data[base+2:], uint16(db2TypeInteger))
	binary.BigEndian.PutUint32(data[base+4:], 4)
	binary.BigEndian.PutUint16(data[base+8:], 0)
	binary.BigEndian.PutUint16(data[base+10:], 0)
	binary.BigEndian.PutUint16(data[base+12:], 37)
	data[base+14] = 1

	payload := appendTestLLCP(nil, CodePointExtendedParameterMarker, data)
	format, err := parseParameterMarkerFormatFromPayload(payload)
	if err != nil {
		t.Fatalf("parseParameterMarkerFormatFromPayload() error = %v", err)
	}
	if format == nil {
		t.Fatal("parseParameterMarkerFormatFromPayload() format = nil")
	}
	if format.parameterCount() != 1 {
		t.Fatalf("parameter count = %d, want 1", format.parameterCount())
	}
	field, ok := format.field(0)
	if !ok {
		t.Fatal("parameter field 0 missing")
	}
	if field.SQLType != db2TypeInteger {
		t.Fatalf("SQLType = %d, want %d", field.SQLType, db2TypeInteger)
	}
	if field.Length != 4 {
		t.Fatalf("Length = %d, want 4", field.Length)
	}
	if field.CCSID != 37 {
		t.Fatalf("CCSID = %d, want 37", field.CCSID)
	}
	if field.ParameterType != 1 {
		t.Fatalf("ParameterType = %d, want 1", field.ParameterType)
	}
}

func TestQueryRowsColumnMetadata(t *testing.T) {
	rows := &queryRows{
		meta: &resultSetMeta{
			columns: []columnMeta{
				{Name: "ID", Type: db2TypeInteger},
				{Name: "NAME", Type: db2TypeVarchar | 1, Length: 42, CCSID: 37},
				{Name: "AMOUNT", Type: db2TypeDecimal | 1, Precision: 9, Scale: 2},
				{Name: "BIN", Type: db2TypeBinary | 1, Length: 8, CCSID: 65535},
				{Name: "DECFLOAT16", Type: db2TypeDecfloat, Precision: 16},
				{Name: "BLOB", Type: db2TypeBlob, Length: -1, LobMaxSize: 1024},
				{Name: "CLOB LOCATOR", Type: db2TypeClobLocator, Length: 4, LobMaxSize: 2048},
			},
		},
	}

	if got := rows.ColumnTypeDatabaseTypeName(0); got != "INTEGER" {
		t.Fatalf("ColumnTypeDatabaseTypeName(0) = %q, want INTEGER", got)
	}
	if got := rows.ColumnTypeScanType(0); got != reflect.TypeOf(int64(0)) {
		t.Fatalf("ColumnTypeScanType(0) = %v, want int64", got)
	}
	if nullable, ok := rows.ColumnTypeNullable(0); !ok || nullable {
		t.Fatalf("ColumnTypeNullable(0) = (%v, %v), want (false, true)", nullable, ok)
	}

	if got := rows.ColumnTypeDatabaseTypeName(1); got != "VARCHAR" {
		t.Fatalf("ColumnTypeDatabaseTypeName(1) = %q, want VARCHAR", got)
	}
	if got := rows.ColumnTypeScanType(1); got != reflect.TypeOf("") {
		t.Fatalf("ColumnTypeScanType(1) = %v, want string", got)
	}
	if length, ok := rows.ColumnTypeLength(1); !ok || length != 42 {
		t.Fatalf("ColumnTypeLength(1) = (%d, %v), want (42, true)", length, ok)
	}
	if nullable, ok := rows.ColumnTypeNullable(1); !ok || !nullable {
		t.Fatalf("ColumnTypeNullable(1) = (%v, %v), want (true, true)", nullable, ok)
	}

	if got := rows.ColumnTypeDatabaseTypeName(2); got != "DECIMAL" {
		t.Fatalf("ColumnTypeDatabaseTypeName(2) = %q, want DECIMAL", got)
	}
	if got := rows.ColumnTypeScanType(2); got != reflect.TypeOf("") {
		t.Fatalf("ColumnTypeScanType(2) = %v, want string", got)
	}
	if precision, scale, ok := rows.ColumnTypePrecisionScale(2); !ok || precision != 9 || scale != 2 {
		t.Fatalf("ColumnTypePrecisionScale(2) = (%d, %d, %v), want (9, 2, true)", precision, scale, ok)
	}

	if got := rows.ColumnTypeDatabaseTypeName(3); got != "BINARY" {
		t.Fatalf("ColumnTypeDatabaseTypeName(3) = %q, want BINARY", got)
	}
	if got := rows.ColumnTypeScanType(3); got != reflect.TypeOf([]byte(nil)) {
		t.Fatalf("ColumnTypeScanType(3) = %v, want []byte", got)
	}
	if length, ok := rows.ColumnTypeLength(3); !ok || length != 8 {
		t.Fatalf("ColumnTypeLength(3) = (%d, %v), want (8, true)", length, ok)
	}

	if precision, scale, ok := rows.ColumnTypePrecisionScale(4); !ok || precision != 16 || scale != 0 {
		t.Fatalf("ColumnTypePrecisionScale(4) = (%d, %d, %v), want (16, 0, true)", precision, scale, ok)
	}
	if got := rows.ColumnTypeScanType(4); got != reflect.TypeOf("") {
		t.Fatalf("ColumnTypeScanType(4) = %v, want string", got)
	}

	if length, ok := rows.ColumnTypeLength(5); !ok || length != 1024 {
		t.Fatalf("ColumnTypeLength(5) = (%d, %v), want (1024, true)", length, ok)
	}
	if length, ok := rows.ColumnTypeLength(6); !ok || length != 2048 {
		t.Fatalf("ColumnTypeLength(6) = (%d, %v), want (2048, true)", length, ok)
	}
	if length, ok := rows.ColumnTypeLength(-1); ok || length != 0 {
		t.Fatalf("ColumnTypeLength(-1) = (%d, %v), want (0, false)", length, ok)
	}
}

func TestQueryRowsColumnMetadataLobFallbacks(t *testing.T) {
	rows := &queryRows{
		meta: &resultSetMeta{
			columns: []columnMeta{
				{Type: db2TypeBlobLocator, Length: 4, LobMaxSize: 0},
				{Type: db2TypeXMLLocator, Length: -1, LobMaxSize: -1},
				{Type: db2TypeDecfloat, Precision: 34, Scale: 0},
			},
		},
	}

	if length, ok := rows.ColumnTypeLength(0); !ok || length != 4 {
		t.Fatalf("ColumnTypeLength(0) = (%d, %v), want (4, true)", length, ok)
	}
	if length, ok := rows.ColumnTypeLength(1); ok || length != 0 {
		t.Fatalf("ColumnTypeLength(1) = (%d, %v), want (0, false)", length, ok)
	}
	if precision, scale, ok := rows.ColumnTypePrecisionScale(2); !ok || precision != 34 || scale != 0 {
		t.Fatalf("ColumnTypePrecisionScale(2) = (%d, %d, %v), want (34, 0, true)", precision, scale, ok)
	}
}

func TestBuildParameterlessPreparedExecuteRequest(t *testing.T) {
	format := &parameterMarkerFormat{codePoint: CodePointParameterMarkerFormat}
	request, err := buildExecutePreparedRequest(0x1234, 0, sqlStatementTypeInsertUpdateDelete, format, nil)
	if err != nil {
		t.Fatalf("buildExecutePreparedRequest() error = %v", err)
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionExecute {
		t.Fatalf("execute request id = 0x%x, want 0x%x", header.ReqRepID, FunctionExecute)
	}
	if got := binary.BigEndian.Uint16(request[34:36]); got != 0x1234 {
		t.Fatalf("execute request rpb handle = 0x%x, want 0x1234", got)
	}
	if got := binary.BigEndian.Uint16(request[36:38]); got != 0 {
		t.Fatalf("execute request descriptor handle = 0x%x, want 0", got)
	}
	data, ok, err := readTestLLCPValue(request[40:], CodePointParameterMarkerData)
	if err != nil {
		t.Fatalf("readTestLLCPValue() error = %v", err)
	}
	if !ok {
		t.Fatal("execute request missing parameter marker data")
	}
	if got := binary.BigEndian.Uint16(data[8:10]); got != 0 {
		t.Fatalf("parameter count = %d, want 0", got)
	}
}

func TestBuildFetchRequestScrollOptions(t *testing.T) {
	request, err := buildFetchRequest("CRSR0001", 1024, fetchDirect, 7)
	if err != nil {
		t.Fatalf("buildFetchRequest() error = %v", err)
	}
	payload, found, err := readTestLLCPValue(request[40:], CodePointFetchScrollOption)
	if err != nil {
		t.Fatalf("readTestLLCPValue() error = %v", err)
	}
	if !found {
		t.Fatal("fetch request missing scroll option")
	}
	if len(payload) != 6 || binary.BigEndian.Uint16(payload[:2]) != uint16(fetchDirect) || binary.BigEndian.Uint32(payload[2:]) != 7 {
		t.Fatalf("scroll option payload = %x, want option 8 and relative 7", payload)
	}
}

func TestBuildOpenDescribeRequestHoldability(t *testing.T) {
	request, err := buildOpenDescribePreparedRequest(1, 0, "STMT0001", "CRSR0001", nil, nil, true, true)
	if err != nil {
		t.Fatalf("buildOpenDescribePreparedRequest() error = %v", err)
	}
	for _, codePoint := range []uint16{CodePointScrollableCursorFlag, CodePointHoldIndicator, CodePointResultSetHoldability} {
		payload, found, err := readTestLLCPValue(request[40:], codePoint)
		if err != nil {
			t.Fatalf("readTestLLCPValue(0x%x) error = %v", codePoint, err)
		}
		if !found {
			t.Fatalf("open request missing code point 0x%x", codePoint)
		}
		if codePoint == CodePointScrollableCursorFlag {
			if len(payload) != 2 || binary.BigEndian.Uint16(payload) != 2 {
				t.Fatalf("scrollable cursor flag = %x, want 0002", payload)
			}
		} else if len(payload) != 1 || payload[0] != holdTrue {
			t.Fatalf("holdability code point 0x%x = %x, want e8", codePoint, payload)
		}
	}
}

func TestScrollableRowsRejectsScrollingWhenDisabled(t *testing.T) {
	rows := &queryRows{}
	for name, operation := range map[string]func() error{
		"before first": rows.BeforeFirst,
		"after last":   rows.AfterLast,
		"current":      rows.Current,
		"first":        rows.First,
		"last":         rows.Last,
		"absolute":     func() error { return rows.Absolute(1) },
		"relative":     func() error { return rows.Relative(1) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("scroll operation error = %v, want ErrUnsupported", err)
			}
		})
	}
	if got := rows.Row(); got != 0 {
		t.Fatalf("Row() = %d, want 0 for an unopened result set", got)
	}
}

package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func batchTestFormat(codePoint uint16, fields ...parameterMarkerField) *parameterMarkerFormat {
	return &parameterMarkerFormat{codePoint: codePoint, Fields: fields}
}

func batchIntField() parameterMarkerField {
	return parameterMarkerField{Name: "C", SQLType: db2TypeInteger, Length: 4}
}

// rawBinding builds a binding with fixed raw bytes, bypassing encoding so the
// block layout can be checked byte for byte.
func rawBinding(isNull bool, raw ...byte) parameterBinding {
	return parameterBinding{raw: raw, isNull: isNull}
}

func TestBuildBlockParameterMarkerDataOriginal(t *testing.T) {
	format := batchTestFormat(CodePointParameterMarkerFormat, batchIntField(), batchIntField())
	rows := [][]parameterBinding{
		{rawBinding(false, 0, 0, 0, 1), rawBinding(false, 0, 0, 0, 2)},
		{rawBinding(true, 0, 0, 0, 0), rawBinding(false, 0, 0, 0, 4)},
		{rawBinding(false, 0, 0, 0, 5), rawBinding(true, 0, 0, 0, 0)},
	}
	payload, codePoint, err := buildBlockParameterMarkerData(format, rows)
	if err != nil {
		t.Fatalf("buildBlockParameterMarkerData() error = %v", err)
	}
	if codePoint != CodePointParameterMarkerData {
		t.Fatalf("code point = 0x%x, want 0x%x", codePoint, CodePointParameterMarkerData)
	}

	// Header: consistency 1, row count 3, columns 2, indicator size 2, row size 8.
	want := []byte{
		0, 0, 0, 1,
		0, 0, 0, 3,
		0, 2,
		0, 2,
		0, 8,
	}
	// Indicators: all rows first, row-major, 2 bytes each, NULL = FFFF.
	want = append(want,
		0, 0, 0, 0, // row 0
		0xFF, 0xFF, 0, 0, // row 1, column 0 is NULL
		0, 0, 0xFF, 0xFF, // row 2, column 1 is NULL
	)
	// Data: row r starts at r*8. NULL values still carry their raw bytes.
	want = append(want,
		0, 0, 0, 1, 0, 0, 0, 2,
		0, 0, 0, 0, 0, 0, 0, 4,
		0, 0, 0, 5, 0, 0, 0, 0,
	)
	if !bytes.Equal(payload, want) {
		t.Fatalf("payload = % X\nwant    % X", payload, want)
	}
}

func TestBuildBlockParameterMarkerDataExtended(t *testing.T) {
	format := batchTestFormat(CodePointExtendedParameterMarker, batchIntField())
	rows := [][]parameterBinding{
		{rawBinding(false, 0, 0, 0, 7)},
		{rawBinding(true, 0, 0, 0, 0)},
	}
	payload, codePoint, err := buildBlockParameterMarkerData(format, rows)
	if err != nil {
		t.Fatalf("buildBlockParameterMarkerData() error = %v", err)
	}
	if codePoint != CodePointParameterMarkerDataExt {
		t.Fatalf("code point = 0x%x, want 0x%x", codePoint, CodePointParameterMarkerDataExt)
	}
	if got := binary.BigEndian.Uint32(payload[4:8]); got != 2 {
		t.Fatalf("row count = %d, want 2", got)
	}
	if got := binary.BigEndian.Uint16(payload[8:10]); got != 1 {
		t.Fatalf("column count = %d, want 1", got)
	}
	if got := binary.BigEndian.Uint32(payload[16:20]); got != 4 {
		t.Fatalf("row size = %d, want 4", got)
	}
	// Header 20, indicators 4 bytes, data 8 bytes.
	if len(payload) != 20+4+8 {
		t.Fatalf("payload length = %d, want 32", len(payload))
	}
	if !bytes.Equal(payload[24:], []byte{0, 0, 0, 7, 0, 0, 0, 0}) {
		t.Fatalf("data = % X", payload[24:])
	}
	if !bytes.Equal(payload[20:24], []byte{0, 0, 0xFF, 0xFF}) {
		t.Fatalf("indicators = % X", payload[20:24])
	}
}

func TestBlockEndSplitsOnShapeAndRowCap(t *testing.T) {
	a := []parameterBinding{rawBinding(false, 1, 2, 3, 4)}
	b := []parameterBinding{rawBinding(false, 1, 2)}

	rows := [][]parameterBinding{a, a, a, b, b}
	if end := blockEnd(rows, 0); end != 3 {
		t.Fatalf("blockEnd(shape A) = %d, want 3", end)
	}
	if end := blockEnd(rows, 3); end != 5 {
		t.Fatalf("blockEnd(shape B) = %d, want 5", end)
	}

	many := make([][]parameterBinding, maxBlockRows+10)
	for i := range many {
		many[i] = a
	}
	if end := blockEnd(many, 0); end != maxBlockRows {
		t.Fatalf("blockEnd over row cap = %d, want %d", end, maxBlockRows)
	}
	if end := blockEnd(many, maxBlockRows); end != len(many) {
		t.Fatalf("blockEnd for tail = %d, want %d", end, len(many))
	}
}

func TestBlockEndByteLimitAlwaysKeepsOneRow(t *testing.T) {
	big := []parameterBinding{rawBinding(false, make([]byte, maxBlockBytes+1)...)}
	rows := [][]parameterBinding{big, big}
	if end := blockEnd(rows, 0); end != 1 {
		t.Fatalf("blockEnd over byte limit = %d, want 1", end)
	}
}

func TestBuildBlockExecuteRequestCarriesBlockIndicator(t *testing.T) {
	format := batchTestFormat(CodePointParameterMarkerFormat, batchIntField())
	rows := [][]parameterBinding{{rawBinding(false, 0, 0, 0, 1)}, {rawBinding(false, 0, 0, 0, 2)}}
	request, err := buildBlockExecuteRequest(9, 3, sqlStatementTypeInsertUpdateDelete, format, rows, nil)
	if err != nil {
		t.Fatalf("buildBlockExecuteRequest() error = %v", err)
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionExecute {
		t.Fatalf("request id = 0x%x, want execute", header.ReqRepID)
	}
	if _, found, err := readTestLLCPValue(request[40:], CodePointParameterMarkerBlockInd); err != nil || !found {
		t.Fatalf("block indicator 0x3814 found = %v (err %v), want true", found, err)
	}
	data, found, err := readTestLLCPValue(request[40:], CodePointParameterMarkerData)
	if err != nil || !found {
		t.Fatalf("data 0x3811 found = %v (err %v)", found, err)
	}
	if got := binary.BigEndian.Uint32(data[4:8]); got != 2 {
		t.Fatalf("data row count = %d, want 2", got)
	}
}

func TestIsBlockableStatement(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"INSERT INTO T VALUES(?,?)", true},
		{"insert into t (a, b) values (?, ?)", true},
		{"INSERT INTO T VALUES (?)", true},
		{"INSERT INTO T VALUES (NULL, ?)", false},
		{"INSERT INTO T VALUES (1, ?)", false},
		{"INSERT INTO T SELECT * FROM S", false},
		{"UPDATE T SET A = ? WHERE B = ?", true},
		{"DELETE FROM T WHERE A = ?", true},
		{"SELECT * FROM T WHERE A = ?", false},
		{"CALL P(?)", false},
	}
	for _, tt := range tests {
		if got := isBlockableStatement(tt.sql); got != tt.want {
			t.Errorf("isBlockableStatement(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}

func TestBatchBlockableRequiresOptionAndNoLOB(t *testing.T) {
	insertFormat := batchTestFormat(CodePointParameterMarkerFormat, batchIntField())
	lobField := parameterMarkerField{Name: "B", SQLType: db2TypeBlob, LOBLocator: 1}
	lobFormat := batchTestFormat(CodePointParameterMarkerFormat, lobField)

	off := &Conn{cfg: Config{UseBlockUpdate: false}}
	on := &Conn{cfg: Config{UseBlockUpdate: true}}
	if off.batchBlockable("INSERT INTO T VALUES(?)", insertFormat) {
		t.Fatal("block path used with useBlockUpdate=false")
	}
	if !on.batchBlockable("INSERT INTO T VALUES(?)", insertFormat) {
		t.Fatal("INSERT with useBlockUpdate=true not blockable")
	}
	if on.batchBlockable("INSERT INTO T VALUES(?)", lobFormat) {
		t.Fatal("LOB parameter was blockable")
	}
	if on.batchBlockable("SELECT 1 FROM T WHERE A = ?", insertFormat) {
		t.Fatal("SELECT was blockable")
	}
	if on.batchBlockable("UPDATE T SET A = ?", insertFormat) {
		t.Fatal("UPDATE blockable without known V7R1 server")
	}
	on.systemInfo = &SystemInfo{ServerVersion: 0x070100}
	if !on.batchBlockable("UPDATE T SET A = ?", insertFormat) {
		t.Fatal("UPDATE not blockable on V7R1")
	}
}

func TestParseDSNUseBlockUpdate(t *testing.T) {
	if cfg := parseAttributeTestDSN(t, "naming=sql"); cfg.UseBlockUpdate {
		t.Fatal("UseBlockUpdate default = true, want false")
	}
	if cfg := parseAttributeTestDSN(t, "naming=sql&useBlockUpdate=true"); !cfg.UseBlockUpdate {
		t.Fatal("useBlockUpdate=true not parsed")
	}
	if _, err := ParseDSN("as400://user:pass@host/qgpl?useBlockUpdate=maybe"); err == nil {
		t.Fatal("invalid useBlockUpdate value accepted")
	}
}

// batchFakeServer answers the descriptor and execute requests of a batch. Each
// execute reply reports the row count of the block it received as the update
// count. It records the row count and parameter code point of every execute.
type batchFakeServer struct {
	executeRows  []uint32
	executeCodes []uint16
	descriptors  int
}

func (s *batchFakeServer) serve(t *testing.T, conn net.Conn, done chan<- struct{}) {
	defer close(done)
	for {
		packet, err := readPacket(conn)
		if err != nil {
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse header: %v", err)
			return
		}
		switch header.ReqRepID {
		case FunctionChangeDescriptor, FunctionDeleteDescriptor:
			if header.ReqRepID == FunctionChangeDescriptor {
				s.descriptors++
			}
			if _, err := conn.Write(buildTestReplyPacket(nil)); err != nil {
				return
			}
		case FunctionExecute:
			body := packet[40:]
			var rows uint32
			var code uint16
			if data, found, _ := readTestLLCPValue(body, CodePointParameterMarkerData); found {
				code = CodePointParameterMarkerData
				rows = binary.BigEndian.Uint32(data[4:8])
			} else if data, found, _ := readTestLLCPValue(body, CodePointParameterMarkerDataExt); found {
				code = CodePointParameterMarkerDataExt
				rows = binary.BigEndian.Uint32(data[4:8])
			}
			s.executeRows = append(s.executeRows, rows)
			s.executeCodes = append(s.executeCodes, code)
			sqlca := make([]byte, 136)
			binary.BigEndian.PutUint32(sqlca[104:108], rows)
			if _, err := conn.Write(buildTestReplyPacket(appendTestLLCP(nil, 0x3807, sqlca))); err != nil {
				return
			}
		default:
			t.Errorf("unexpected request 0x%x", header.ReqRepID)
			return
		}
	}
}

func runBatchAgainstFakeServer(t *testing.T, cfg Config, format *parameterMarkerFormat, rows [][]driver.Value) (int64, *batchFakeServer, error) {
	t.Helper()
	client, server := net.Pipe()
	fake := &batchFakeServer{}
	done := make(chan struct{})
	go fake.serve(t, server, done)

	conn := &Conn{cfg: cfg, netConn: client, autoCommit: true}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	total, err := conn.execBatchPrepared(ctx, "INSERT INTO T VALUES(?)", 5, sqlStatementTypeInsertUpdateDelete, format, rows)
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake server did not finish")
	}
	_ = server.Close()
	return total, fake, err
}

func TestExecBatchBlockPathSendsRowsInOneExecute(t *testing.T) {
	format := batchTestFormat(CodePointParameterMarkerFormat, batchIntField())
	rows := [][]driver.Value{{int64(1)}, {nil}, {int64(3)}}
	total, fake, err := runBatchAgainstFakeServer(t, Config{UseBlockUpdate: true}, format, rows)
	if err != nil {
		t.Fatalf("ExecBatch() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("total rows affected = %d, want 3", total)
	}
	if len(fake.executeRows) != 1 || fake.executeRows[0] != 3 {
		t.Fatalf("executes = %v, want one execute with 3 rows", fake.executeRows)
	}
	if fake.executeCodes[0] != CodePointParameterMarkerData {
		t.Fatalf("parameter code point = 0x%x", fake.executeCodes[0])
	}
	if fake.descriptors != 1 {
		t.Fatalf("change descriptor requests = %d, want 1", fake.descriptors)
	}
}

func TestExecBatchSplitsIntoBlocksAndSumsUpdateCounts(t *testing.T) {
	format := batchTestFormat(CodePointParameterMarkerFormat, batchIntField())
	const n = maxBlockRows + 8000
	rows := make([][]driver.Value, n)
	for i := range rows {
		rows[i] = []driver.Value{int64(i)}
	}
	total, fake, err := runBatchAgainstFakeServer(t, Config{UseBlockUpdate: true}, format, rows)
	if err != nil {
		t.Fatalf("ExecBatch() error = %v", err)
	}
	if total != n {
		t.Fatalf("total rows affected = %d, want %d", total, n)
	}
	if len(fake.executeRows) != 2 || fake.executeRows[0] != maxBlockRows || fake.executeRows[1] != 8000 {
		t.Fatalf("executes = %v, want [%d 8000]", fake.executeRows, maxBlockRows)
	}
}

func TestExecBatchFallsBackToRowByRow(t *testing.T) {
	format := batchTestFormat(CodePointParameterMarkerFormat, batchIntField())
	rows := [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}}
	total, fake, err := runBatchAgainstFakeServer(t, Config{UseBlockUpdate: false}, format, rows)
	if err != nil {
		t.Fatalf("ExecBatch() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("total rows affected = %d, want 3", total)
	}
	if len(fake.executeRows) != 3 {
		t.Fatalf("executes = %v, want 3 single-row executes", fake.executeRows)
	}
	for i, rows := range fake.executeRows {
		if rows != 1 {
			t.Fatalf("execute %d carried %d rows, want 1", i, rows)
		}
	}
}

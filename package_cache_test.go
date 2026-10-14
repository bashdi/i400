package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

// pkgCacheTestField describes one SQLDA field in a synthetic package format.
// length is the short at offset 2; for decimal types it carries precision in
// the high byte and scale in the low byte, as in the real format.
type pkgCacheTestField struct {
	sqlType uint16
	length  uint16
	ccsid   uint16
	param   byte // 0 means input ('I')
	name    string
}

// buildPkgCacheTestSQLDA builds a package data format: a 16-byte header with the
// field count at offset 14, then one 80-byte entry per field.
func buildPkgCacheTestSQLDA(fields []pkgCacheTestField) []byte {
	data := make([]byte, 16+80*len(fields))
	binary.BigEndian.PutUint16(data[14:16], uint16(len(fields)))
	for i, f := range fields {
		base := 16 + 80*i
		binary.BigEndian.PutUint16(data[base:base+2], f.sqlType)
		binary.BigEndian.PutUint16(data[base+2:base+4], f.length)
		binary.BigEndian.PutUint16(data[base+34:base+36], f.ccsid)
		data[base+48] = 0xC9
		if f.param != 0 {
			data[base+48] = f.param
		}
		name, _ := EncodeEBCDIC37(f.name)
		binary.BigEndian.PutUint16(data[base+64:base+66], uint16(len(name)))
		copy(data[base+66:], name)
	}
	return data
}

type pkgCacheTestStatement struct {
	name    string
	text    string
	params  []pkgCacheTestField // nil means no parameter marker format
	results []pkgCacheTestField // nil means no result format
}

// buildPkgCacheTestReply builds the package info LLCP (0x380B) that
// RETURN_PACKAGE returns, following DBReplyPackageInfo's layout. Offsets in the
// entries are counted from the start of the LL, six bytes before the info.
func buildPkgCacheTestReply(t *testing.T, ccsid uint16, statements []pkgCacheTestStatement) []byte {
	t.Helper()
	header := make([]byte, packageInfoHeaderLength)
	binary.BigEndian.PutUint16(header[4:6], ccsid)
	binary.BigEndian.PutUint16(header[24:26], uint16(len(statements)))

	entries := make([]byte, packageInfoEntryLength*len(statements))
	var data []byte
	place := func(payload []byte) (uint32, uint32) {
		offset := uint32(packageInfoHeaderLength + len(entries) + len(data) + 6)
		data = append(data, payload...)
		return offset, uint32(len(payload))
	}
	for i, s := range statements {
		entry := entries[packageInfoEntryLength*i : packageInfoEntryLength*(i+1)]
		name, err := EncodeEBCDIC37(s.name)
		if err != nil {
			t.Fatalf("encode name: %v", err)
		}
		nameField := bytes.Repeat([]byte{0x40}, 18)
		copy(nameField, name)
		copy(entry[3:21], nameField)

		text, err := encodeTextBytes(s.text, int(ccsid))
		if err != nil {
			t.Fatalf("encode text: %v", err)
		}
		offset, length := place(text)
		binary.BigEndian.PutUint32(entry[48:52], offset)
		binary.BigEndian.PutUint32(entry[52:56], length)

		if s.params != nil {
			offset, length = place(buildPkgCacheTestSQLDA(s.params))
			binary.BigEndian.PutUint32(entry[56:60], offset)
			binary.BigEndian.PutUint32(entry[60:64], length)
		}
		if s.results != nil {
			offset, length = place(buildPkgCacheTestSQLDA(s.results))
			binary.BigEndian.PutUint32(entry[40:44], offset)
			binary.BigEndian.PutUint32(entry[44:48], length)
		}
	}
	info := append(append(header, entries...), data...)
	binary.BigEndian.PutUint32(info[0:4], uint32(len(info)))
	return appendTestLLCP(nil, codePointPackageInfo, info)
}

// pkgCacheTestStatements is the package used by the cache tests. The CCSID 1200
// reply carries UTF-16 text.
func pkgCacheTestStatements() []pkgCacheTestStatement {
	return []pkgCacheTestStatement{
		{
			name: "SQL_0001",
			text: "SELECT * FROM T WHERE A = ? AND B = ?",
			params: []pkgCacheTestField{
				{sqlType: 497, length: 4, ccsid: 0},
				{sqlType: 449, length: 10, ccsid: 37, name: "P2"},
			},
			results: []pkgCacheTestField{
				{sqlType: 453, length: 8, ccsid: 37, name: "C1"},
			},
		},
		{
			name: "SQL_0002",
			text: "UPDATE T SET A = 1",
		},
		{
			name: "SQL_0003",
			text: "DELETE FROM T WHERE A = ?",
			// Packed decimal precision 9, scale 2: length 9/2+1 = 5 bytes.
			params: []pkgCacheTestField{{sqlType: 485, length: 9<<8 | 2, ccsid: 0}},
		},
	}
}

func TestParsePackageInfoBuildsCache(t *testing.T) {
	payload := buildPkgCacheTestReply(t, 1200, pkgCacheTestStatements())
	cache, err := parsePackageInfo(payload, 37)
	if err != nil {
		t.Fatalf("parsePackageInfo() error = %v", err)
	}
	if len(cache.statements) != 3 {
		t.Fatalf("statements = %d, want 3", len(cache.statements))
	}

	first := cache.statements[0]
	if first.name != "SQL_0001" || first.text != "SELECT * FROM T WHERE A = ? AND B = ?" || !first.usable {
		t.Fatalf("first statement = %+v", first)
	}
	if first.paramFormat == nil || first.paramFormat.codePoint != CodePointParameterMarkerFormat {
		t.Fatalf("parameter format = %+v", first.paramFormat)
	}
	if got := len(first.paramFormat.Fields); got != 2 {
		t.Fatalf("parameter fields = %d, want 2", got)
	}
	if f := first.paramFormat.Fields[0]; f.SQLType != 497 || f.Length != 4 || f.ParameterType != 0xF0 || f.LOBLocator != -1 {
		t.Fatalf("field 0 = %+v", f)
	}
	// Varying-length types add the two-byte length prefix (10 + 2).
	if f := first.paramFormat.Fields[1]; f.SQLType != 449 || f.Length != 12 || f.Name != "P2" || f.CCSID != 37 {
		t.Fatalf("field 1 = %+v", f)
	}
	if first.paramFormat.RecordSize != 16 {
		t.Fatalf("record size = %d, want 16", first.paramFormat.RecordSize)
	}
	if len(first.resultFields) != 1 || first.resultFields[0].Name != "C1" || first.resultFields[0].Length != 8 {
		t.Fatalf("result fields = %+v", first.resultFields)
	}

	second := cache.statements[1]
	if second.name != "SQL_0002" || second.text != "UPDATE T SET A = 1" || second.paramFormat != nil || second.resultFields != nil {
		t.Fatalf("statement without formats = %+v", second)
	}

	third := cache.statements[2]
	if f := third.paramFormat.Fields[0]; f.SQLType != 485 || f.Length != 5 || f.Scale != 2 || f.Precision != 9 {
		t.Fatalf("packed decimal field = %+v", f)
	}
}

func TestParsePackageInfoRejectsTruncatedReply(t *testing.T) {
	payload := buildPkgCacheTestReply(t, 1200, pkgCacheTestStatements())
	// Keep the header and the first entries only, and claim nine statements.
	info := append([]byte(nil), payload[6:packageInfoHeaderLength+packageInfoEntryLength*3]...)
	binary.BigEndian.PutUint16(info[24:26], 9)
	payload = appendTestLLCP(nil, codePointPackageInfo, info)
	if _, err := parsePackageInfo(payload, 37); err == nil {
		t.Fatal("parsePackageInfo() accepted a statement count larger than the reply")
	}
	if _, err := parsePackageInfo(nil, 37); err == nil {
		t.Fatal("parsePackageInfo() accepted a reply without package info")
	}
}

func newPkgCacheTestState(t *testing.T, statements []pkgCacheTestStatement) *packageState {
	t.Helper()
	cache, err := parsePackageInfo(buildPkgCacheTestReply(t, 1200, statements), 37)
	if err != nil {
		t.Fatalf("parsePackageInfo() error = %v", err)
	}
	return &packageState{
		enabled:           true,
		created:           true,
		cacheOn:           true,
		cache:             cache,
		name:              "MYPKG9499",
		library:           "QGPL",
		errMode:           packageErrorWarning,
		statementPackaged: map[uint16]bool{},
	}
}

func TestCachedStatementLookupRules(t *testing.T) {
	state := newPkgCacheTestState(t, pkgCacheTestStatements())

	tests := []struct {
		name     string
		selectOK bool
		query    string
		wantName string
	}{
		{name: "exact packaged text hits", query: "SELECT * FROM T WHERE A = ? AND B = ?", wantName: "SQL_0001"},
		{name: "different text misses", query: "SELECT * FROM T WHERE A = ? AND B = ? ", wantName: ""},
		{name: "unpackaged text misses even when cached", selectOK: false, query: "UPDATE T SET A = 1", wantName: ""},
		{name: "update is not packaged under select criteria", selectOK: true, query: "UPDATE T SET A = 1", wantName: ""},
		{name: "packaged delete with marker hits", query: "DELETE FROM T WHERE A = ?", wantName: "SQL_0003"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state.selectOK = tt.selectOK
			stmt, ok := state.cachedStatementFor(tt.query)
			if ok != (tt.wantName != "") || stmt.name != tt.wantName {
				t.Fatalf("cachedStatementFor(%q) = (%q, %v), want %q", tt.query, stmt.name, ok, tt.wantName)
			}
		})
	}
	state.selectOK = false

	// A cached parameter marker format with a LOB locator is prepared again.
	lobStatements := []pkgCacheTestStatement{{
		name:   "SQL_0009",
		text:   "INSERT INTO T VALUES(?)",
		params: []pkgCacheTestField{{sqlType: 960, length: 4, ccsid: 0}},
	}}
	lobState := newPkgCacheTestState(t, lobStatements)
	if _, ok := lobState.cachedStatementFor("INSERT INTO T VALUES(?)"); ok {
		t.Fatal("LOB locator parameter must not use the cached statement")
	}

	// A statement with an unusable name never matches.
	unnamed := newPkgCacheTestState(t, []pkgCacheTestStatement{{name: "", text: "DELETE FROM T WHERE A = ?"}})
	if _, ok := unnamed.cachedStatementFor("DELETE FROM T WHERE A = ?"); ok {
		t.Fatal("statement with an empty name must not match")
	}

	var nilState *packageState
	if _, ok := nilState.cachedStatementFor("SELECT 1"); ok {
		t.Fatal("nil package state must not match")
	}
}

func TestPackageCacheOptionFromConfig(t *testing.T) {
	cfg := Config{ExtendedDynamic: true, Package: "MYPKG", PackageCache: true, PackageClear: true, DateFormat: 5, Naming: NamingSQL}
	state, warning, err := newPackageState(cfg)
	if err != nil || warning != nil || state == nil {
		t.Fatalf("newPackageState() = (%v, %v, %v)", state, warning, err)
	}
	if !state.cacheOn {
		t.Fatal("packageCache=true did not enable the cache")
	}
	cfg.PackageCache = false
	if state, _, _ = newPackageState(cfg); state.cacheOn {
		t.Fatal("packageCache=false left the cache on")
	}
}

func TestPackageClearDSNOption(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "packageClear=true")
	if !cfg.PackageClear {
		t.Fatal("packageClear=true not parsed")
	}
	cfg = parseAttributeTestDSN(t, "package%20clear=false")
	if cfg.PackageClear {
		t.Fatal("package clear=false not parsed")
	}
	if _, err := ParseDSN("as400://user:pass@host/qgpl?packageClear=maybe"); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("ParseDSN(packageClear=maybe) error = %v, want ErrInvalidDSN", err)
	}
}

func TestBuildReturnPackageRequest(t *testing.T) {
	request, err := buildReturnPackageRequest("MYPKG9499", "MYLIB")
	if err != nil {
		t.Fatalf("buildReturnPackageRequest() error = %v", err)
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionReturnPackage {
		t.Fatalf("request id = 0x%x, want RETURN_PACKAGE", header.ReqRepID)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != orsSendReplyImmediately|orsPackageInfo {
		t.Fatalf("ORS bitmap = 0x%x, want return data and package info", got)
	}
	attrs := packageTestAttributes(t, request)
	assertParmCount(t, request, attrs)
	findPackageAttr(t, attrs, CodePointPackageName)
	findPackageAttr(t, attrs, CodePointPackageLibrary)
	ret := findPackageAttr(t, attrs, codePointPackageReturnSize)
	if !bytes.Equal(ret.payload, []byte{0, 0, 0, 0}) {
		t.Fatalf("return size payload = %v", ret.payload)
	}
}

func TestEnsurePackageDownloadsExistingPackage(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	reply := buildPkgCacheTestReply(t, 1200, pkgCacheTestStatements())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := readPacket(server); err != nil {
			t.Errorf("read create package: %v", err)
			return
		}
		_, _ = server.Write(buildTestReplyPacketWithRC(nil, 1, -601))

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read return package: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil || header.ReqRepID != FunctionReturnPackage {
			t.Errorf("second request = 0x%x (%v), want RETURN_PACKAGE", header.ReqRepID, err)
			return
		}
		_, _ = server.Write(buildTestReplyPacket(reply))
	}()

	state := newPkgCacheTestState(t, nil)
	state.created = false
	state.cache = nil
	conn := &Conn{netConn: client, pkg: state}
	if err := conn.ensurePackage(context.Background()); err != nil {
		t.Fatalf("ensurePackage() error = %v", err)
	}
	<-done
	if state.cache == nil || len(state.cache.statements) != 3 || !state.created {
		t.Fatalf("cache = %+v, created = %v", state.cache, state.created)
	}
}

func TestEnsurePackageSkipsDownloadWhenCacheOff(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := readPacket(server); err != nil {
			t.Errorf("read create package: %v", err)
			return
		}
		_, _ = server.Write(buildTestReplyPacketWithRC(nil, 1, -601))
	}()

	state := &packageState{enabled: true, name: "MYPKG9499", library: "QGPL", errMode: packageErrorWarning, statementPackaged: map[uint16]bool{}}
	conn := &Conn{netConn: client, pkg: state}
	if err := conn.ensurePackage(context.Background()); err != nil {
		t.Fatalf("ensurePackage() error = %v", err)
	}
	<-done
	if state.cache != nil || !state.created {
		t.Fatalf("cache = %+v, created = %v", state.cache, state.created)
	}
}

// readPkgCacheRequests reads n requests from the server side of the pipe. The
// caller decides what the server writes back. It is safe to call from a goroutine.
func readPkgCacheRequests(server net.Conn, n int) ([][]byte, error) {
	var packets [][]byte
	for i := 0; i < n; i++ {
		packet, err := readPacket(server)
		if err != nil {
			return packets, err
		}
		packets = append(packets, packet)
	}
	return packets, nil
}

func TestPrepareCacheHitSkipsPrepareDescribe(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	state := newPkgCacheTestState(t, pkgCacheTestStatements())
	conn := &Conn{netConn: client, pkg: state}

	const handle = uint16(0x0005)
	const query = "SELECT * FROM T WHERE A = ? AND B = ?"
	gotRequests := make(chan [][]byte, 1)
	go func() {
		// Only the Create RPB should arrive; nothing reads a reply for it.
		packets, err := readPkgCacheRequests(server, 1)
		if err != nil {
			t.Errorf("read Create RPB: %v", err)
		}
		gotRequests <- packets
	}()

	name, format, err := conn.prepareOrReuse(context.Background(), handle, "STMT00000005", query)
	if err != nil {
		t.Fatalf("prepareOrReuse() error = %v", err)
	}
	packets := <-gotRequests
	if len(packets) == 0 {
		t.Fatal("no Create RPB request reached the server")
	}

	if name != "SQL_0001" {
		t.Fatalf("statement name = %q, want cached SQL_0001", name)
	}
	if format == nil || len(format.Fields) != 2 || format.Fields[1].Length != 12 {
		t.Fatalf("parameter format = %+v, want the cached format", format)
	}
	if format == state.cache.statements[0].paramFormat {
		t.Fatal("cached format returned by pointer; callers could modify the cache")
	}

	header, err := ParseHeader(packets[0])
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionCreateRPB {
		t.Fatalf("request id = 0x%x, want Create RPB", header.ReqRepID)
	}
	attrs := packageTestAttributes(t, packets[0])
	nameAttr := findPackageAttr(t, attrs, CodePointPrepareStatementName)
	want, _ := EncodeEBCDIC37("SQL_0001")
	if !bytes.Equal(nameAttr.payload[4:], want) {
		t.Fatalf("RPB statement name = %v, want cached name", nameAttr.payload)
	}

	// No PREPARE_DESCRIBE may follow. A write from the driver would block on the
	// pipe, so a short read deadline on the server side must time out.
	_ = server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if extra, err := readPacket(server); err == nil {
		header, _ := ParseHeader(extra)
		t.Fatalf("unexpected request 0x%x after cache hit", header.ReqRepID)
	}

	if !state.statementPackaged[handle] {
		t.Fatal("cache hit did not record the statement as packaged")
	}
}

func TestPrepareCacheMissSendsPrepareDescribe(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	state := newPkgCacheTestState(t, pkgCacheTestStatements())
	conn := &Conn{netConn: client, pkg: state}

	const query = "SELECT * FROM OTHER WHERE A = ?"
	done := make(chan struct{})
	go func() {
		defer close(done)
		packets, err := readPkgCacheRequests(server, 2)
		if err != nil {
			t.Errorf("read requests: %v", err)
			return
		}
		header, err := ParseHeader(packets[1])
		if err != nil || header.ReqRepID != FunctionPrepareDescribe {
			t.Errorf("second request = 0x%x (%v), want PREPARE_DESCRIBE", header.ReqRepID, err)
		}
		_, _ = server.Write(buildTestReplyPacket(nil))
	}()

	name, _, err := conn.prepareOrReuse(context.Background(), 0x0006, "STMT00000006", query)
	if err != nil {
		t.Fatalf("prepareOrReuse() error = %v", err)
	}
	<-done
	if name != "STMT00000006" {
		t.Fatalf("statement name = %q, want the generated name on a miss", name)
	}
}

func TestClosePreparedStatementSkipsCachedName(t *testing.T) {
	state := newPkgCacheTestState(t, pkgCacheTestStatements())
	// netConn is nil, so any request that reaches the wire fails with ErrBadConn.
	conn := &Conn{pkg: state}

	if err := conn.closePreparedStatement(context.Background(), "SQL_0001", 1); err != nil {
		t.Fatalf("closing a cached package statement error = %v, want nil", err)
	}
	if err := conn.closePreparedStatement(context.Background(), "STMT00000007", 7); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("closing a driver statement error = %v, want it to reach the wire", err)
	}
}

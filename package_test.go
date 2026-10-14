package i400

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

// The expected package names and criteria results below were produced by
// running JTOpen's JDPackageManager.getSuffix/constructor and
// JDSQLStatement.isPackaged (see the scratch harness in the session notes).

func TestPackageNameMatchesJTOpen(t *testing.T) {
	tests := []struct {
		name       string
		pkg        string
		library    string
		commitMode int
		cfg        Config
		wantName   string
		wantLib    string
	}{
		{
			name: "defaults short name", pkg: "MYPKG", commitMode: 0,
			cfg:      Config{DateFormat: 5, Naming: NamingSQL},
			wantName: "MYPKG9499", wantLib: "QGPL",
		},
		{
			name: "name truncated to six characters", pkg: "ORDERSPACKAGE", commitMode: 0,
			cfg:      Config{DateFormat: 5, Naming: NamingSQL},
			wantName: "ORDERS9499", wantLib: "QGPL",
		},
		{
			name: "blanks become underscores and library upper-cased", pkg: "my pkg", library: "mylib", commitMode: 0,
			cfg:      Config{DateFormat: 5, Naming: NamingSQL},
			wantName: "MY_PKG9499", wantLib: "MYLIB",
		},
		{
			name: "short name is not padded", pkg: "ab", commitMode: 0,
			cfg:      Config{DateFormat: 5, Naming: NamingSQL},
			wantName: "AB9499", wantLib: "QGPL",
		},
		{
			name: "commit cs ymd dash comma system", pkg: "ORDERS", library: "lib2", commitMode: 1,
			cfg:      Config{DateFormat: 3, DateSeparator: 1, DecimalSeparator: 1, Naming: NamingSystem, TimeFormat: 1, TimeSeparator: 1},
			wantName: "ORDERS9YK4", wantLib: "LIB2",
		},
		{
			name: "commit chg eur period space", pkg: "ABCDEFG", commitMode: 2,
			cfg:      Config{DateFormat: 6, DateSeparator: 2, DecimalSeparator: 0, Naming: NamingSQL, TimeFormat: 3, TimeSeparator: 3},
			wantName: "ABCDEF9N7U", wantLib: "QGPL",
		},
		{
			name: "commit all mdy comma iso", pkg: "XYZ123", commitMode: 3,
			cfg:      Config{DateFormat: 1, DateSeparator: 3, DecimalSeparator: 0, Naming: NamingSQL, TimeFormat: 2, TimeSeparator: 2},
			wantName: "XYZ1239K6Z", wantLib: "QGPL",
		},
		{
			name: "commit rr with date separator period", pkg: "RRTEST", commitMode: 4,
			cfg:      Config{DateFormat: 5, DateSeparator: 2, Naming: NamingSQL},
			wantName: "RRTEST9O39", wantLib: "QGPL",
		},
		{
			name: "commit rr with date separator comma", pkg: "RRTEST", commitMode: 4,
			cfg:      Config{DateFormat: 5, DateSeparator: 3, Naming: NamingSQL},
			wantName: "RRTEST9W29", wantLib: "QGPL",
		},
		{
			name: "commit rr with date separator space", pkg: "RRTEST", commitMode: 4,
			cfg:      Config{DateFormat: 2, DateSeparator: 4, DecimalSeparator: 1, Naming: NamingSystem, TimeFormat: 4, TimeSeparator: 3},
			wantName: "RRTEST9REQ", wantLib: "QGPL",
		},
		{
			name: "commit rr with date separator slash", pkg: "RRTEST", commitMode: 4,
			cfg:      Config{DateFormat: 0, DateSeparator: 0, Naming: NamingSQL},
			wantName: "RRTEST9939", wantLib: "QGPL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			// With trueAutocommit the commit mode is the configured one, which is
			// what JTOpen's transaction manager reports for the case under test.
			cfg.CommitmentControl = tt.commitMode
			cfg.TrueAutocommit = true
			cfg.ExtendedDynamic = true
			cfg.Package = tt.pkg
			cfg.PackageLibrary = tt.library
			state, warning, err := newPackageState(cfg)
			if err != nil || warning != nil || state == nil {
				t.Fatalf("newPackageState() = (%v, %v, %v), want enabled state", state, warning, err)
			}
			if state.name != tt.wantName {
				t.Fatalf("package name = %q, want %q", state.name, tt.wantName)
			}
			if state.library != tt.wantLib {
				t.Fatalf("package library = %q, want %q", state.library, tt.wantLib)
			}
		})
	}
}

// TestPackageCriteriaMatchesJTOpen covers JDSQLStatement.isPackaged for both
// "package criteria" values.
func TestPackageCriteriaMatchesJTOpen(t *testing.T) {
	tests := []struct {
		sql     string
		def     bool
		selectC bool
	}{
		{"SELECT * FROM T", false, true},
		{"SELECT * FROM T WHERE A = ?", true, true},
		{"SELECT * FROM T FOR UPDATE", true, true},
		{"SELECT * FROM T FOR READ ONLY", false, true},
		{"SELECT * FROM T FOR FETCH ONLY", false, true},
		{"VALUES 1", false, true},
		{"WITH X AS (SELECT 1 FROM SYSIBM.SYSDUMMY1) SELECT * FROM X", false, true},
		{"(SELECT * FROM T)", false, true},
		{"INSERT INTO T VALUES(1)", false, false},
		{"INSERT INTO T VALUES(?)", true, true},
		{"INSERT INTO T SELECT * FROM S", true, true},
		{"INSERT INTO T (SELECT * FROM S)", true, true},
		{"UPDATE T SET A=1", false, false},
		{"UPDATE T SET A=?", true, true},
		{"UPDATE T SET A=1 WHERE CURRENT OF C1", false, false},
		{"UPDATE T SET A=? WHERE CURRENT OF C1", false, false},
		{"DELETE FROM T", false, false},
		{"DELETE FROM T WHERE CURRENT OF C1", false, false},
		{"DECLARE C1 CURSOR FOR SELECT * FROM T", true, true},
		{"CALL P(?)", true, true},
		{"CALL P(1)", false, false},
		{"CREATE TABLE X (A INT)", false, false},
		{"SET SCHEMA FOO", false, false},
		{"SELECT 'a ? b' FROM T", false, true},
		{"SELECT * FROM T -- where a = ?\nWHERE B = 1", false, true},
		{"SELECT * FROM T /* ? */ WHERE B = 1", false, true},
		{"SELECT * FROM T WHERE A = 'FOR UPDATE'", false, true},
	}
	for _, tt := range tests {
		if got := packageCriteriaMatches(tt.sql, false); got != tt.def {
			t.Errorf("default criteria for %q = %v, want %v", tt.sql, got, tt.def)
		}
		if got := packageCriteriaMatches(tt.sql, true); got != tt.selectC {
			t.Errorf("select criteria for %q = %v, want %v", tt.sql, got, tt.selectC)
		}
	}
}

func TestParsePackageDSNOptions(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "")
	if cfg.ExtendedDynamic || cfg.Package != "" || cfg.PackageLibrary != "QGPL" ||
		cfg.PackageCriteria != "default" || cfg.PackageError != "warning" ||
		cfg.PackageCCSID != 13488 || cfg.PackageAddDisabled || cfg.PackageCache {
		t.Fatalf("package defaults = %+v", cfg)
	}

	cfg = parseAttributeTestDSN(t, "extendedDynamic=true&package=mypkg&packageLibrary=mylib&packageCriteria=select&packageAdd=false&packageError=exception&packageCCSID=1200&packageCache=true")
	if !cfg.ExtendedDynamic || cfg.Package != "mypkg" || cfg.PackageLibrary != "MYLIB" ||
		cfg.PackageCriteria != "select" || cfg.PackageError != "exception" ||
		cfg.PackageCCSID != 1200 || !cfg.PackageAddDisabled || !cfg.PackageCache {
		t.Fatalf("package values = %+v", cfg)
	}

	// JTOpen spellings with spaces are accepted.
	cfg = parseAttributeTestDSN(t, "extended%20dynamic=true&package%20library=lib2&package%20error=none&package%20ccsid=system")
	if !cfg.ExtendedDynamic || cfg.PackageLibrary != "LIB2" || cfg.PackageError != "none" || !cfg.PackageCCSIDSystem {
		t.Fatalf("spaced package values = %+v", cfg)
	}

	for _, query := range []string{
		"extendedDynamic=maybe",
		"packageCriteria=sometimes",
		"packageError=loud",
		"packageCCSID=-1",
		"packageAdd=nope",
		"packageCache=nope",
	} {
		if _, err := ParseDSN("as400://user:pass@host/qgpl?" + query); !errors.Is(err, ErrInvalidDSN) {
			t.Errorf("ParseDSN(%q) error = %v, want ErrInvalidDSN", query, err)
		}
	}
}

func TestValidatePackageConfigRejectsBadValues(t *testing.T) {
	if err := (Config{Host: "h", PackageError: "loud"}).Validate(); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("Validate() packageError error = %v", err)
	}
	if err := (Config{Host: "h", PackageCriteria: "maybe"}).Validate(); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("Validate() packageCriteria error = %v", err)
	}
	if err := (Config{Host: "h", PackageCCSID: -5}).Validate(); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("Validate() packageCCSID error = %v", err)
	}
	if err := (Config{Host: "h"}).Validate(); err != nil {
		t.Fatalf("Validate() zero package values error = %v", err)
	}
}

func TestNewPackageStateConnectTimeErrors(t *testing.T) {
	state, warning, err := newPackageState(Config{ExtendedDynamic: false, Package: ""})
	if state != nil || warning != nil || err != nil {
		t.Fatalf("disabled package state = (%v, %v, %v)", state, warning, err)
	}

	state, warning, err = newPackageState(Config{ExtendedDynamic: true, PackageError: "warning"})
	if state != nil || err != nil || warning == nil || warning.State != packageSQLStateDisabled {
		t.Fatalf("missing name with warning = (%v, %v, %v)", state, warning, err)
	}

	state, warning, err = newPackageState(Config{ExtendedDynamic: true, PackageError: "exception"})
	if state != nil || warning != nil || err == nil {
		t.Fatalf("missing name with exception = (%v, %v, %v)", state, warning, err)
	}

	state, warning, err = newPackageState(Config{ExtendedDynamic: true, PackageError: "none"})
	if state != nil || warning != nil || err != nil {
		t.Fatalf("missing name with none = (%v, %v, %v)", state, warning, err)
	}
}

func TestBuildCreatePackageRequest(t *testing.T) {
	request, err := buildCreatePackageRequest("MYPKG9499", "QGPL")
	if err != nil {
		t.Fatalf("buildCreatePackageRequest() error = %v", err)
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionCreatePackage {
		t.Fatalf("request id = 0x%x, want 0x%x", header.ReqRepID, FunctionCreatePackage)
	}
	if got := binary.BigEndian.Uint16(request[38:40]); got != 2 {
		t.Fatalf("parameter count = %d, want 2", got)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != orsSendReplyImmediately {
		t.Fatalf("ORS bitmap = 0x%x, want return data", got)
	}
	attrs := packageTestAttributes(t, request)
	if len(attrs) != 2 {
		t.Fatalf("attributes = %d, want 2", len(attrs))
	}
	wantName, _ := EncodeEBCDIC37("MYPKG9499")
	wantLib, _ := EncodeEBCDIC37("QGPL")
	if attrs[0].codePoint != CodePointPackageName || !bytes.Equal(attrs[0].payload[4:], wantName) {
		t.Fatalf("package name attribute = %+v", attrs[0])
	}
	if attrs[1].codePoint != CodePointPackageLibrary || !bytes.Equal(attrs[1].payload[4:], wantLib) {
		t.Fatalf("library attribute = %+v", attrs[1])
	}
}

func TestEnsurePackageReplyHandling(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		rcClass     uint16
		rcCode      int32
		wantErr     bool
		wantEnabled bool
		wantWarning bool
	}{
		{name: "created", mode: "warning", rcClass: 0, rcCode: 0, wantEnabled: true},
		{name: "already exists is success", mode: "exception", rcClass: 1, rcCode: -601, wantEnabled: true},
		{name: "default collection mismatch warns", mode: "warning", rcClass: 1, rcCode: -999999, wantWarning: true},
		{name: "default collection mismatch none is silent", mode: "none", rcClass: 1, rcCode: -999999},
		{name: "failure with exception", mode: "exception", rcClass: 2, rcCode: -204, wantErr: true},
		{name: "failure with warning", mode: "warning", rcClass: 2, rcCode: -204, wantWarning: true},
		{name: "failure with none is silent", mode: "none", rcClass: 2, rcCode: -204},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			done := make(chan struct{})
			go func() {
				defer close(done)
				packet, err := readPacket(server)
				if err != nil {
					t.Errorf("read create package: %v", err)
					return
				}
				header, err := ParseHeader(packet)
				if err != nil {
					t.Errorf("parse create package: %v", err)
					return
				}
				if header.ReqRepID != FunctionCreatePackage {
					t.Errorf("request id = 0x%x, want CREATE_PACKAGE", header.ReqRepID)
				}
				_, _ = server.Write(buildTestReplyPacketWithRC(nil, tt.rcClass, tt.rcCode))
			}()

			state := &packageState{enabled: true, name: "MYPKG9499", library: "QGPL", errMode: tt.mode, statementPackaged: map[uint16]bool{}}
			conn := &Conn{netConn: client, pkg: state}
			err := conn.ensurePackage(context.Background())
			<-done

			if (err != nil) != tt.wantErr {
				t.Fatalf("ensurePackage() error = %v, wantErr %v", err, tt.wantErr)
			}
			if state.enabled != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", state.enabled, tt.wantEnabled)
			}
			if !tt.wantErr && !state.created {
				t.Fatalf("package not marked created")
			}
			if tt.wantWarning != (conn.lastWarning != nil) {
				t.Fatalf("warning recorded = %v, want %v", conn.lastWarning != nil, tt.wantWarning)
			}
		})
	}
}

func TestEnsurePackageSkipsWhenDisabledOrCreated(t *testing.T) {
	// No network traffic is expected: the connection has no netConn at all.
	conn := &Conn{pkg: &packageState{enabled: false, errMode: "exception"}}
	if err := conn.ensurePackage(context.Background()); err != nil {
		t.Fatalf("ensurePackage() disabled error = %v", err)
	}
	if !conn.pkg.created {
		t.Fatal("disabled package not marked created")
	}

	conn = &Conn{pkg: &packageState{enabled: true, created: true, errMode: "exception"}}
	if err := conn.ensurePackage(context.Background()); err != nil {
		t.Fatalf("ensurePackage() created error = %v", err)
	}

	var nilConn *Conn
	if err := nilConn.ensurePackage(context.Background()); err != nil {
		t.Fatalf("nil conn ensurePackage() error = %v", err)
	}
}

func TestPackageSpecLifecycle(t *testing.T) {
	var nilState *packageState
	if nilState.specForPrepare(1, "SELECT 1") != nil || nilState.specForExecute(1) != nil ||
		nilState.specForLibrary() != nil || nilState.specForImmediate("SELECT 1") != nil {
		t.Fatal("nil package state must produce no specs")
	}

	state := &packageState{enabled: true, name: "MYPKG9499", library: "QGPL", selectOK: false, statementPackaged: map[uint16]bool{}}

	plain := state.specForPrepare(7, "SELECT 1")
	if plain == nil || plain.packaged {
		t.Fatalf("plain select spec = %+v, want non-nil unpackaged", plain)
	}
	if state.specForExecute(7) != nil {
		t.Fatal("execute spec for unpackaged statement must be nil")
	}

	packaged := state.specForPrepare(8, "SELECT * FROM T WHERE A = ?")
	if packaged == nil || !packaged.packaged || packaged.name != "MYPKG9499" {
		t.Fatalf("packaged spec = %+v", packaged)
	}
	exec := state.specForExecute(8)
	if exec == nil || !exec.packaged || exec.name != "MYPKG9499" {
		t.Fatalf("execute spec = %+v", exec)
	}

	if immediate := state.specForImmediate("DELETE FROM T WHERE A = ?"); immediate == nil || !immediate.packaged {
		t.Fatalf("immediate spec = %+v", immediate)
	}

	state.enabled = false
	if state.specForPrepare(9, "SELECT * FROM T WHERE A = ?") != nil || state.specForExecute(8) != nil {
		t.Fatal("disabled package state must produce no specs")
	}
}

func TestPrepareDescribeRequestPackageAttributes(t *testing.T) {
	const query = "SELECT * FROM T WHERE A = ?"
	packagedSpec := &packageSpec{name: "MYPKG9499", library: "QGPL", packaged: true}
	plainSpec := &packageSpec{name: "MYPKG9499", library: "QGPL", packaged: false}

	t.Run("packaged", func(t *testing.T) {
		request, err := buildPrepareAndDescribeRequest(0x1234, "STMT0001", query, packagedSpec)
		if err != nil {
			t.Fatalf("buildPrepareAndDescribeRequest() error = %v", err)
		}
		attrs := packageTestAttributes(t, request)
		assertParmCount(t, request, attrs)
		option := findPackageAttr(t, attrs, codePointPrepareOption)
		if len(option.payload) != 1 || option.payload[0] != 1 {
			t.Fatalf("prepare option = %v, want 1", option.payload)
		}
		name := findPackageAttr(t, attrs, CodePointPackageName)
		want, _ := EncodeEBCDIC37("MYPKG9499")
		if !bytes.Equal(name.payload[4:], want) {
			t.Fatalf("package name payload = %v", name.payload)
		}
	})

	t.Run("not packaged sends empty name", func(t *testing.T) {
		request, err := buildPrepareAndDescribeRequest(0x1234, "STMT0001", "SELECT 1", plainSpec)
		if err != nil {
			t.Fatalf("buildPrepareAndDescribeRequest() error = %v", err)
		}
		attrs := packageTestAttributes(t, request)
		assertParmCount(t, request, attrs)
		option := findPackageAttr(t, attrs, codePointPrepareOption)
		if len(option.payload) != 1 || option.payload[0] != 0 {
			t.Fatalf("prepare option = %v, want 0", option.payload)
		}
		name := findPackageAttr(t, attrs, CodePointPackageName)
		if len(name.payload) != 0 {
			t.Fatalf("empty package name payload = %v", name.payload)
		}
	})

	t.Run("disabled sends no package attributes", func(t *testing.T) {
		request, err := buildPrepareAndDescribeRequest(0x1234, "STMT0001", query, nil)
		if err != nil {
			t.Fatalf("buildPrepareAndDescribeRequest() error = %v", err)
		}
		attrs := packageTestAttributes(t, request)
		assertParmCount(t, request, attrs)
		for _, attr := range attrs {
			if attr.codePoint == CodePointPackageName || attr.codePoint == codePointPrepareOption {
				t.Fatalf("unexpected package attribute 0x%x", attr.codePoint)
			}
		}
	})
}

func TestExecuteRequestsPackageAttributes(t *testing.T) {
	packagedSpec := &packageSpec{name: "MYPKG9499", library: "QGPL", packaged: true}

	request, err := buildExecutePreparedRequest(0x1234, 0x5678, sqlStatementTypeInsertUpdateDelete, nil, nil, packagedSpec)
	if err != nil {
		t.Fatalf("buildExecutePreparedRequest() error = %v", err)
	}
	attrs := packageTestAttributes(t, request)
	assertParmCount(t, request, attrs)
	findPackageAttr(t, attrs, CodePointPackageName)

	request, err = buildExecutePreparedRequest(0x1234, 0x5678, sqlStatementTypeInsertUpdateDelete, nil, nil, &packageSpec{name: "MYPKG9499", library: "QGPL"})
	if err != nil {
		t.Fatalf("buildExecutePreparedRequest() error = %v", err)
	}
	attrs = packageTestAttributes(t, request)
	assertParmCount(t, request, attrs)
	for _, attr := range attrs {
		if attr.codePoint == CodePointPackageName {
			t.Fatal("execute of unpackaged statement must not carry the package name")
		}
	}

	request, err = buildOpenDescribePreparedRequest(1, 0, "STMT0001", "CRSR0001", nil, nil, false, false, packagedSpec)
	if err != nil {
		t.Fatalf("buildOpenDescribePreparedRequest() error = %v", err)
	}
	attrs = packageTestAttributes(t, request)
	assertParmCount(t, request, attrs)
	findPackageAttr(t, attrs, CodePointPackageName)
}

func TestExecuteImmediateCallHasSinglePrepareOption(t *testing.T) {
	for _, spec := range []*packageSpec{nil, {name: "MYPKG9499", library: "QGPL", packaged: true}, {name: "MYPKG9499", library: "QGPL"}} {
		request, err := buildExecuteImmediateRequest("CALL P(1)", sqlStatementTypeCall, spec)
		if err != nil {
			t.Fatalf("buildExecuteImmediateRequest() error = %v", err)
		}
		attrs := packageTestAttributes(t, request)
		assertParmCount(t, request, attrs)
		count := 0
		for _, attr := range attrs {
			if attr.codePoint == codePointPrepareOption {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("spec %+v: prepare option count = %d, want 1", spec, count)
		}
	}
}

func TestCreateRPBRequestCarriesLibrary(t *testing.T) {
	request, err := buildCreateRPBRequest(0x1234, "STMT0001", &packageSpec{name: "MYPKG9499", library: "MYLIB"})
	if err != nil {
		t.Fatalf("buildCreateRPBRequest() error = %v", err)
	}
	attrs := packageTestAttributes(t, request)
	assertParmCount(t, request, attrs)
	lib := findPackageAttr(t, attrs, CodePointPackageLibrary)
	want, _ := EncodeEBCDIC37("MYLIB")
	if !bytes.Equal(lib.payload[4:], want) {
		t.Fatalf("library payload = %v", lib.payload)
	}

	request, err = buildCreateRPBRequest(0x1234, "STMT0001", nil)
	if err != nil {
		t.Fatalf("buildCreateRPBRequest() error = %v", err)
	}
	for _, attr := range packageTestAttributes(t, request) {
		if attr.codePoint == CodePointPackageLibrary {
			t.Fatal("RPB without package support must not carry the library")
		}
	}
}

func TestSetServerAttributesPackageAdd(t *testing.T) {
	cfg := Config{Naming: NamingSQL, DateFormat: 5, CommitmentControl: 1}
	request, err := buildSetServerAttributesRequest(cfg, false, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	if got := packageAddValue(t, request); got != 1 {
		t.Fatalf("package add default = %d, want 1", got)
	}

	cfg.PackageAddDisabled = true
	request, err = buildSetServerAttributesRequest(cfg, false, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	if got := packageAddValue(t, request); got != 0 {
		t.Fatalf("package add disabled = %d, want 0", got)
	}
}

type packageTestAttr struct {
	codePoint uint16
	payload   []byte
}

// packageTestAttributes walks the LLCP attributes that follow the 40-byte
// request header and template.
func packageTestAttributes(t *testing.T, request []byte) []packageTestAttr {
	t.Helper()
	var attrs []packageTestAttr
	for offset := 40; offset < len(request); {
		if offset+6 > len(request) {
			t.Fatalf("truncated attribute at %d", offset)
		}
		length := int(binary.BigEndian.Uint32(request[offset : offset+4]))
		if length < 6 || offset+length > len(request) {
			t.Fatalf("bad attribute length %d at %d", length, offset)
		}
		attrs = append(attrs, packageTestAttr{
			codePoint: binary.BigEndian.Uint16(request[offset+4 : offset+6]),
			payload:   request[offset+6 : offset+length],
		})
		offset += length
	}
	return attrs
}

func assertParmCount(t *testing.T, request []byte, attrs []packageTestAttr) {
	t.Helper()
	if got := int(binary.BigEndian.Uint16(request[38:40])); got != len(attrs) {
		t.Fatalf("template parameter count = %d, attributes = %d", got, len(attrs))
	}
}

func findPackageAttr(t *testing.T, attrs []packageTestAttr, codePoint uint16) packageTestAttr {
	t.Helper()
	for _, attr := range attrs {
		if attr.codePoint == codePoint {
			return attr
		}
	}
	t.Fatalf("attribute 0x%x not found in %d attributes", codePoint, len(attrs))
	return packageTestAttr{}
}

func packageAddValue(t *testing.T, request []byte) uint16 {
	t.Helper()
	for _, attr := range packageTestAttributes(t, request) {
		if attr.codePoint == codePointPackageAddStatement {
			if len(attr.payload) != 2 {
				t.Fatalf("package add payload = %v", attr.payload)
			}
			return binary.BigEndian.Uint16(attr.payload)
		}
	}
	t.Fatal("package add attribute not found")
	return 0
}

func TestPackageAddAttributeNotDuplicated(t *testing.T) {
	// The set-server-attributes body must carry exactly one package-add attribute.
	request, err := buildSetServerAttributesRequest(Config{Naming: NamingSQL, DateFormat: 5}, false, 1)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	count := 0
	for _, attr := range packageTestAttributes(t, request) {
		if attr.codePoint == codePointPackageAddStatement {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("package add attributes = %d, want 1", count)
	}
}

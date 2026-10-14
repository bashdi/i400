package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// ucOpenDB opens a database handle and closes it at test cleanup.
func ucOpenDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("i400", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func ucContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestUCHandshakeEndToEnd runs the full open through sql.Open and PingContext
// against fake signon and database hosts, then checks the parsed system info,
// the job name and the end-job request sent on close.
func TestUCHandshakeEndToEnd(t *testing.T) {
	signon, database := ucStartRig(t, ucDefaultSignon(4), ucDefaultDB())
	db := ucOpenDB(t, ucDSN(database.port(), ""))
	ctx := ucContext(t)

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	var info SystemInfo
	var jobName string
	err = conn.Raw(func(dc any) error {
		c, ok := dc.(*Conn)
		if !ok {
			return fmt.Errorf("driver connection is %T", dc)
		}
		if c.systemInfo == nil {
			return errors.New("systemInfo is nil")
		}
		info = *c.systemInfo
		jobName = c.jobName
		return nil
	})
	if err != nil {
		t.Fatalf("Raw() error = %v", err)
	}
	_ = conn.Close()

	if info.System != "127.0.0.1" {
		t.Errorf("System = %q, want 127.0.0.1", info.System)
	}
	if info.ServerVersion != 0x070200 {
		t.Errorf("ServerVersion = 0x%x, want 0x070200", info.ServerVersion)
	}
	if info.ServerLevel != 5 {
		t.Errorf("ServerLevel = %d, want 5", info.ServerLevel)
	}
	if info.PasswordLevel != 4 {
		t.Errorf("PasswordLevel = %d, want 4", info.PasswordLevel)
	}
	if info.ServerCCSID != 37 {
		t.Errorf("ServerCCSID = %d, want 37", info.ServerCCSID)
	}
	if info.JobName != ucJobName || jobName != ucJobName {
		t.Errorf("JobName = %q, conn job name = %q, want %q", info.JobName, jobName, ucJobName)
	}
	if info.ServerFunctionalLevel != 5 {
		t.Errorf("ServerFunctionalLevel = %d, want 5 (from V7R2M005)", info.ServerFunctionalLevel)
	}
	if got := strings.TrimRight(info.ServerJobIdentifier, " "); got != strings.TrimRight(ucJobID, " ") {
		t.Errorf("ServerJobIdentifier = %q, want %q", got, strings.TrimRight(ucJobID, " "))
	}

	for _, id := range []uint16{RequestExchangeAttributes, RequestSignonInfo} {
		signon.waitFor(id)
	}
	for _, id := range []uint16{RequestExchangeRandomSeeds, RequestStartServer, FunctionSetAttributes, FunctionTestConnection} {
		database.waitFor(id)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("db.Close() error = %v", err)
	}
	database.waitFor(FunctionEndJob)
}

// TestUCPasswordLevelSelectsSubstitute checks the substitute and the auth scheme
// the client sends for password levels 0, 2 and 4, on both the signon request
// and the database start-server request.
func TestUCPasswordLevelSelectsSubstitute(t *testing.T) {
	for _, level := range []int{0, 2, 4} {
		t.Run(fmt.Sprintf("level%d", level), func(t *testing.T) {
			signon, database := ucStartRig(t, ucDefaultSignon(byte(level)), ucDefaultDB())
			db := ucOpenDB(t, ucDSN(database.port(), ""))
			if err := db.PingContext(ucContext(t)); err != nil {
				t.Fatalf("PingContext() error = %v", err)
			}

			exchange := signon.waitFor(RequestExchangeAttributes)
			clientSeed, found, err := readTestLLCPValue(exchange.Raw[20:], CodePointClientSeed)
			if err != nil || !found || len(clientSeed) != 8 {
				t.Fatalf("client seed found=%v len=%d err=%v", found, len(clientSeed), err)
			}

			auth := signon.waitFor(RequestSignonInfo)
			ucCheckPasswordRequest(t, "signon", auth.Raw, 21, level, clientSeed)
			// DSLevel 5 selects the return-error-messages item in the signon request.
			if _, found, _ := readTestLLCPValue(auth.Raw[21:], CodePointReturnErrorMessages); !found {
				t.Errorf("signon request lacks the return-error-messages item")
			}

			seeds := database.waitFor(RequestExchangeRandomSeeds)
			start := database.waitFor(RequestStartServer)
			ucCheckPasswordRequest(t, "start server", start.Raw, 22, level, seeds.Raw[20:28])
			if start.Raw[21] != 1 {
				t.Errorf("start server byte 21 = %d, want 1", start.Raw[21])
			}
		})
	}
}

// TestUCWrongPasswordReturnsAuthenticationError checks that a signon rejection
// for an invalid password surfaces as ErrAuthentication and stops the open
// before the database server is contacted.
func TestUCWrongPasswordReturnsAuthenticationError(t *testing.T) {
	signonScript := ucDefaultSignon(4)
	signonScript.signonRC = 0x3000B
	signon, database := ucStartRig(t, signonScript, ucDefaultDB())
	db := ucOpenDB(t, ucDSN(database.port(), ""))

	err := db.PingContext(ucContext(t))
	if err == nil {
		t.Fatal("PingContext() error = nil, want authentication error")
	}
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("PingContext() error = %v, want ErrAuthentication", err)
	}
	if !strings.Contains(err.Error(), "password may be incorrect") {
		t.Errorf("error message = %q, want it to mention the password", err)
	}
	signon.waitFor(RequestSignonInfo)
	if n := database.count(RequestExchangeRandomSeeds) + database.count(RequestStartServer); n != 0 {
		t.Errorf("database host received %d open requests after signon failed, want 0", n)
	}
}

// TestUCLibraryListSentDuringOpen checks that the libraries option is sent as
// an add-library-list request after the server attributes, with the first
// library as the default SQL library.
func TestUCLibraryListSentDuringOpen(t *testing.T) {
	signon, database := ucStartRig(t, ucDefaultSignon(4), ucDefaultDB())
	db := ucOpenDB(t, ucDSN(database.port(), "libraries=APPLIB,OTHERLIB"))
	if err := db.PingContext(ucContext(t)); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	_ = signon

	recs := database.requests()
	attrIndex, addIndex := -1, -1
	for i, rec := range recs {
		switch rec.ReqRepID {
		case FunctionSetAttributes:
			if attrIndex < 0 {
				attrIndex = i
			}
		case FunctionAddLibraryList:
			if addIndex < 0 {
				addIndex = i
			}
		}
	}
	if addIndex < 0 {
		t.Fatal("no add-library-list request was sent")
	}
	if attrIndex < 0 || addIndex < attrIndex {
		t.Fatalf("add-library-list at %d, set attributes at %d; want it after", addIndex, attrIndex)
	}
	add := recs[addIndex]
	if add.ServerID != NativeDatabaseServerID {
		t.Errorf("add-library-list ServerID = 0x%x, want 0x%x", add.ServerID, NativeDatabaseServerID)
	}
	payload, found, err := readTestLLCPValue(add.Raw[40:], CodePointListOfLibraries)
	if err != nil || !found {
		t.Fatalf("list of libraries found=%v err=%v", found, err)
	}
	entries, err := parseTestLibraryListEntries(payload)
	if err != nil {
		t.Fatalf("parseTestLibraryListEntries() error = %v", err)
	}
	want := []testLibraryListEntry{{Indicator: 'C', Name: "APPLIB"}, {Indicator: 'L', Name: "OTHERLIB"}}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry[%d] = %+v, want %+v", i, entries[i], want[i])
		}
	}

	setAttrs := recs[attrIndex]
	defaultLib, found, err := readTestLLCPValue(setAttrs.Raw[40:], CodePointDefaultSQLLibrary)
	if err != nil || !found || len(defaultLib) < 4 {
		t.Fatalf("default SQL library found=%v len=%d err=%v", found, len(defaultLib), err)
	}
	if got, err := DecodeEBCDIC37(defaultLib[4:]); err != nil || got != "APPLIB" {
		t.Errorf("default SQL library = %q (%v), want APPLIB", got, err)
	}
}

func TestUCNoLibraryListWithoutLibrariesOption(t *testing.T) {
	_, database := ucStartRig(t, ucDefaultSignon(4), ucDefaultDB())
	db := ucOpenDB(t, ucDSN(database.port(), ""))
	if err := db.PingContext(ucContext(t)); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	if n := database.count(FunctionAddLibraryList); n != 0 {
		t.Errorf("add-library-list requests = %d, want 0", n)
	}
}

// TestUCLibraryListFailureFailsOpen checks that a server error on the library
// list makes the open fail with a message that names the step.
func TestUCLibraryListFailureFailsOpen(t *testing.T) {
	dbScript := ucDefaultDB()
	dbScript.libraryRC = -1
	_, database := ucStartRig(t, ucDefaultSignon(4), dbScript)
	db := ucOpenDB(t, ucDSN(database.port(), "libraries=APPLIB"))

	err := db.PingContext(ucContext(t))
	if err == nil || !strings.Contains(err.Error(), "add library list failed") {
		t.Fatalf("PingContext() error = %v, want add library list failure", err)
	}
}

// ucIsTimeout reports whether err is a context deadline or a socket timeout. The
// open path returns the context error only when ctx.Err() is already set, so a
// socket deadline that fires first surfaces as a net timeout.
func ucIsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ucOpenResult is the outcome of Driver.Open, with the time it took.
type ucOpenResult struct {
	conn    driver.Conn
	err     error
	elapsed time.Duration
}

// ucOpenWithin runs Driver.Open (which uses a background context) and fails the
// test if it has not returned within limit.
func ucOpenWithin(t *testing.T, dsn string, limit time.Duration) ucOpenResult {
	t.Helper()
	done := make(chan ucOpenResult, 1)
	start := time.Now()
	go func() {
		conn, err := (&Driver{}).Open(dsn)
		done <- ucOpenResult{conn: conn, err: err, elapsed: time.Since(start)}
	}()
	select {
	case res := <-done:
		return res
	case <-time.After(limit):
		t.Fatalf("Driver.Open did not return within %v; connectTimeout is not bounding the open", limit)
		return ucOpenResult{}
	}
}

// TestUCConnectTimeoutBoundsSilentSignonServer checks that connectTimeout ends
// the open when the signon host accepts the connection but never answers.
func TestUCConnectTimeoutBoundsSilentSignonServer(t *testing.T) {
	ucListen(t, ucSignonAddr, nil)
	database := ucListen(t, "127.0.0.1:0", ucDBReply(ucDefaultDB()))

	res := ucOpenWithin(t, ucDSN(database.port(), "connectTimeout=1s"), 15*time.Second)
	if res.err == nil {
		_ = res.conn.Close()
		t.Fatal("Driver.Open() error = nil, want a timeout")
	}
	if !ucIsTimeout(res.err) {
		t.Errorf("Driver.Open() error = %v, want a timeout", res.err)
	}
	if res.elapsed > 10*time.Second {
		t.Errorf("Driver.Open() took %v, want about the 1s connectTimeout", res.elapsed)
	}
}

// TestUCConnectTimeoutBoundsSilentDatabaseServer checks the same when the
// signon succeeds and the database server never answers the seed exchange.
func TestUCConnectTimeoutBoundsSilentDatabaseServer(t *testing.T) {
	ucListen(t, ucSignonAddr, ucSignonReply(ucDefaultSignon(4)))
	database := ucListen(t, "127.0.0.1:0", nil)

	res := ucOpenWithin(t, ucDSN(database.port(), "connectTimeout=1s"), 15*time.Second)
	if res.err == nil {
		_ = res.conn.Close()
		t.Fatal("Driver.Open() error = nil, want a timeout")
	}
	if !ucIsTimeout(res.err) {
		t.Errorf("Driver.Open() error = %v, want a timeout", res.err)
	}
}

// TestUCContextDeadlineBoundsPing checks that a context deadline ends PingContext
// when the signon host never answers, even with the default connect timeout.
func TestUCContextDeadlineBoundsPing(t *testing.T) {
	ucListen(t, ucSignonAddr, nil)
	database := ucListen(t, "127.0.0.1:0", ucDBReply(ucDefaultDB()))
	db := ucOpenDB(t, ucDSN(database.port(), ""))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := db.PingContext(ctx)
	if !ucIsTimeout(err) {
		t.Fatalf("PingContext() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("PingContext() took %v, want about 500ms", elapsed)
	}
}

// TestUCPortMapperResolvesDatabasePort checks a DSN without a port: the driver
// asks the port mapper for as-database and connects to the port it returns.
func TestUCPortMapperResolvesDatabasePort(t *testing.T) {
	signon := ucListen(t, ucSignonAddr, ucSignonReply(ucDefaultSignon(4)))
	database := ucListen(t, "127.0.0.1:0", ucDBReply(ucDefaultDB()))
	seen := ucStartMapper(t, func(service string) []byte {
		return ucMapperPort(database.port())
	})
	db := ucOpenDB(t, ucDSN(0, ""))

	if err := db.PingContext(ucContext(t)); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	select {
	case service := <-seen:
		if service != DatabaseServiceName {
			t.Errorf("port mapper asked for %q, want %q", service, DatabaseServiceName)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("port mapper was not asked for the database service")
	}
	signon.waitFor(RequestSignonInfo)
	database.waitFor(FunctionTestConnection)
}

// TestUCUnresolvedPortFallsBackToDefaultPort checks that when the port mapper
// is unreachable the driver uses DatabasePort (8471).
func TestUCUnresolvedPortFallsBackToDefaultPort(t *testing.T) {
	if conn, err := net.Dial("tcp", ucMapperAddr); err == nil {
		_ = conn.Close()
		t.Skipf("something listens on %s; the fallback cannot be isolated", ucMapperAddr)
	}
	ucListen(t, ucSignonAddr, ucSignonReply(ucDefaultSignon(4)))
	database := ucListen(t, fmt.Sprintf("127.0.0.1:%d", DatabasePort), ucDBReply(ucDefaultDB()))
	db := ucOpenDB(t, ucDSN(0, ""))

	if err := db.PingContext(ucContext(t)); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	database.waitFor(FunctionTestConnection)
}

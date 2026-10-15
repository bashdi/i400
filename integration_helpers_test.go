package i400

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Environment variables that enable the IBM i integration tests.
//
//	I400_TEST_DSN      DSN for a live IBM i system, for example
//	                   as400://USER:PASSWORD@host/?naming=sql
//	I400_TEST_LIBRARY  Library where tests may create and drop tables,
//	                   procedures and packages. Required by tests that create objects.
//
// Alternatively the DSN is built from I400_ADRESSE, I400_USER and
// I400_PASSWORT, and I400_BIBLIOTHEK is used when I400_TEST_LIBRARY is unset.
// These variables may also be put into a .env file in the package directory
// (see .env_template); variables already set in the environment win.
//
// Every integration test skips when no DSN is configured or when -short is given.
const (
	integrationDSNEnv     = "I400_TEST_DSN"
	integrationLibraryEnv = "I400_TEST_LIBRARY"
	integrationHostEnv    = "I400_ADRESSE"
	integrationUserEnv    = "I400_USER"
	integrationPassEnv    = "I400_PASSWORT"
	integrationLibEnvAlt  = "I400_BIBLIOTHEK"
)

var loadDotEnvOnce sync.Once

// loadDotEnv reads KEY=VALUE lines from .env into the process environment
// without overriding variables that are already set.
func loadDotEnv() {
	loadDotEnvOnce.Do(func() {
		f, err := os.Open(".env")
		if err != nil {
			return
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if _, set := os.LookupEnv(key); !set {
				os.Setenv(key, value)
			}
		}
	})
}

// integrationDSNFromParts builds a DSN from I400_ADRESSE, I400_USER and I400_PASSWORT.
func integrationDSNFromParts() string {
	host := strings.TrimSpace(os.Getenv(integrationHostEnv))
	user := strings.TrimSpace(os.Getenv(integrationUserEnv))
	if host == "" || user == "" {
		return ""
	}
	u := url.URL{Scheme: "as400", Host: host, User: url.UserPassword(user, os.Getenv(integrationPassEnv))}
	return u.String()
}

var integrationNameSeq atomic.Uint32

// integrationDSN returns the DSN from I400_TEST_DSN, or skips the test.
func integrationDSN(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping IBM i integration test in short mode")
	}
	loadDotEnv()
	dsn := strings.TrimSpace(os.Getenv(integrationDSNEnv))
	if dsn == "" {
		dsn = integrationDSNFromParts()
	}
	if dsn == "" {
		t.Skipf("set %s or %s/%s/%s (or a .env file) to run this test", integrationDSNEnv, integrationHostEnv, integrationUserEnv, integrationPassEnv)
	}
	return dsn
}

// integrationLibrary returns the library from I400_TEST_LIBRARY, or skips the test.
func integrationLibrary(t *testing.T) string {
	t.Helper()
	integrationDSN(t)
	lib := strings.ToUpper(strings.TrimSpace(os.Getenv(integrationLibraryEnv)))
	if lib == "" {
		lib = strings.ToUpper(strings.TrimSpace(os.Getenv(integrationLibEnvAlt)))
	}
	if lib == "" {
		t.Skipf("set %s to a library where the test may create objects", integrationLibraryEnv)
	}
	return lib
}

// integrationDSNWith returns the DSN from I400_TEST_DSN with the given query
// options set (replacing any existing value for the same key).
func integrationDSNWith(t *testing.T, options map[string]string) string {
	t.Helper()
	base := integrationDSN(t)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", integrationDSNEnv, err)
	}
	query := u.Query()
	for key, value := range options {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

// integrationName returns a unique system name of at most 10 characters
// (prefix up to 3 letters plus 7 digits) so it is valid for tables,
// procedures and packages.
func integrationName(prefix string) string {
	seq := integrationNameSeq.Add(1)
	micros := uint64(time.Now().UnixNano() / 1000)
	return fmt.Sprintf("%s%07d", prefix, (micros+uint64(seq)*7919)%10_000_000)
}

// integrationQualified returns LIB.NAME.
func integrationQualified(lib, name string) string {
	return lib + "." + name
}

// openIntegrationDB opens a *sql.DB for dsn, pings it and closes it at cleanup.
func openIntegrationDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("i400", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	// Registered before any object cleanup so that cleanups which use db run first.
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	return db
}

// integrationContext returns a context bounded by one minute.
func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// createIntegrationObject runs a DDL statement that creates an object and
// registers dropSQL to run at cleanup. db must have been opened with
// openIntegrationDB before the call so that its Close runs after the drop.
func createIntegrationObject(t *testing.T, db *sql.DB, createSQL, dropSQL string) {
	t.Helper()
	ctx := integrationContext(t)
	if _, err := db.ExecContext(ctx, createSQL); err != nil {
		t.Fatalf("create object: %v\nSQL: %s", err, createSQL)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := db.ExecContext(dropCtx, dropSQL); err != nil {
			t.Errorf("cleanup %q: %v", dropSQL, err)
		}
	})
}

// createIntegrationTable creates qualified with the given column list and drops it at cleanup.
func createIntegrationTable(t *testing.T, db *sql.DB, qualified, columns string) {
	t.Helper()
	createIntegrationObject(t, db,
		fmt.Sprintf("CREATE TABLE %s (%s)", qualified, columns),
		"DROP TABLE "+qualified)
}

// integrationSkipBelowVRM skips the test when the server reports a VRM older
// than minVRM (JTOpen encoding: (version<<16)|(release<<8)|modification), for
// features the release does not have. An unknown VRM (0) does not skip.
func integrationSkipBelowVRM(t *testing.T, db *sql.DB, minVRM int, feature string) {
	t.Helper()
	ctx := integrationContext(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	var vrm int
	if err := conn.Raw(func(dc any) error {
		c, ok := dc.(*Conn)
		if !ok {
			return fmt.Errorf("driver conn is %T, want *Conn", dc)
		}
		if c.systemInfo != nil {
			vrm = c.systemInfo.ServerVersion
		}
		return nil
	}); err != nil {
		t.Fatalf("conn.Raw() error = %v", err)
	}
	if vrm != 0 && vrm < minVRM {
		t.Skipf("%s needs a server VRM of %#x or later; the server reports %#x", feature, minVRM, vrm)
	}
}

// sqlErrorFrom reports whether err is (or wraps) an *SQLError.
func sqlErrorFrom(err error) (*SQLError, bool) {
	var sqlErr *SQLError
	if errors.As(err, &sqlErr) {
		return sqlErr, true
	}
	return nil, false
}

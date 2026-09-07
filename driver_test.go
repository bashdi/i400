package i400

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestDriverOpenConnector(t *testing.T) {
	connector, err := driverInstance.OpenConnector("as400://user:pass@host/QTEMP")
	if err != nil {
		t.Fatalf("OpenConnector() error = %v", err)
	}

	as400Connector, ok := connector.(*Connector)
	if !ok {
		t.Fatalf("OpenConnector() returned %T, want *Connector", connector)
	}
	if as400Connector.cfg.Host != "host" || as400Connector.cfg.User != "user" || as400Connector.cfg.Schema != "QTEMP" {
		t.Fatalf("connector config = host %q, user %q, schema %q", as400Connector.cfg.Host, as400Connector.cfg.User, as400Connector.cfg.Schema)
	}
	if connector.Driver() != driverInstance {
		t.Fatalf("Connector.Driver() = %p, want %p", connector.Driver(), driverInstance)
	}
}

func TestDriverOpenConnectorRejectsInvalidDSN(t *testing.T) {
	_, err := (&Driver{}).OpenConnector("not-an-as400-dsn")
	if err == nil {
		t.Fatal("OpenConnector() error = nil, want error")
	}
	if !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("OpenConnector() error = %v, want ErrInvalidDSN", err)
	}
}

func TestConnectDatabaseRejectsInvalidPortAsConnectionError(t *testing.T) {
	_, _, err := connectDatabase(nil, Config{Port: -1}, &SystemInfo{}, "user", "password")
	if err == nil {
		t.Fatal("connectDatabase() error = nil, want error")
	}
	if !errors.Is(err, ErrConnection) {
		t.Fatalf("connectDatabase() error = %v, want ErrConnection", err)
	}
}

func TestCachedStatementReleaseAfterEviction(t *testing.T) {
	conn := &Conn{}
	statement := &cachedStatement{
		statementName: "STMT0001",
		refs:          1,
		evicted:       true,
	}

	conn.releaseCachedStatement(statement)
	if statement.refs != 0 {
		t.Fatalf("cached statement refs = %d, want 0", statement.refs)
	}
}

func TestCachedPreparedStatementHitReusesEntry(t *testing.T) {
	statement := &cachedStatement{statementName: "STMT0001", refs: 0}
	conn := &Conn{
		cfg: Config{StatementCacheSize: 2},
		statementCache: map[string]*cachedStatement{
			"values (?)": statement,
		},
		statementCacheOrder: []string{"values (?)"},
	}

	first, err := conn.cachedPreparedStatement(context.Background(), "values (?)")
	if err != nil {
		t.Fatalf("first cached lookup error = %v", err)
	}
	second, err := conn.cachedPreparedStatement(context.Background(), "values (?)")
	if err != nil {
		t.Fatalf("second cached lookup error = %v", err)
	}
	if first != statement || second != statement {
		t.Fatal("cached lookup did not return the existing statement")
	}
	if statement.refs != 2 {
		t.Fatalf("cached statement refs = %d, want 2", statement.refs)
	}
	if len(conn.statementCacheOrder) != 1 || conn.statementCacheOrder[0] != "values (?)" {
		t.Fatalf("cache order after hit = %v, want [values (?) ]", conn.statementCacheOrder)
	}
	conn.releaseCachedStatement(statement)
	conn.releaseCachedStatement(statement)
}

var _ driver.Connector = (*Connector)(nil)

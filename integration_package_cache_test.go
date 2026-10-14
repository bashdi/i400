package i400

import (
	"database/sql"
	"fmt"
	"testing"
)

// TestIntegrationPackageCache opens two connections with extendedDynamic=true
// and packageCache=true on the same package, prepares the same statement on
// both, and checks that each returns the right rows. The second connection
// reuses the package the first one created (or found) through the client-side
// package cache.
func TestIntegrationPackageCache(t *testing.T) {
	lib := integrationLibrary(t)
	plainDB := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	dsn := integrationDSNWith(t, map[string]string{
		"extendedDynamic": "true",
		"packageCache":    "true",
		"package":         integrationName("IPC"),
		"packageLibrary":  lib,
		"packageError":    "exception",
	})
	registerPackageCleanup(t, plainDB, lib, integrationPackageName(t, dsn))

	table := integrationQualified(lib, integrationName("ICA"))
	createIntegrationTable(t, plainDB, table, "ID INTEGER NOT NULL, NAME VARCHAR(40) NOT NULL")
	for i := 1; i <= 2; i++ {
		if _, err := plainDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, NAME) VALUES (?, ?)", table), i, fmt.Sprintf("cache-%d", i)); err != nil {
			t.Fatalf("INSERT %d error = %v", i, err)
		}
	}

	// Two independent pools so each side really opens its own connection.
	dbA := openIntegrationDB(t, dsn)
	dbB := openIntegrationDB(t, dsn)
	connA, err := dbA.Conn(ctx)
	if err != nil {
		t.Fatalf("dbA.Conn() error = %v", err)
	}
	defer connA.Close()
	connB, err := dbB.Conn(ctx)
	if err != nil {
		t.Fatalf("dbB.Conn() error = %v", err)
	}
	defer connB.Close()

	query := fmt.Sprintf("SELECT NAME FROM %s WHERE ID = ?", table)
	stmtA, err := connA.PrepareContext(ctx, query)
	if err != nil {
		t.Fatalf("connA prepare error = %v", err)
	}
	defer stmtA.Close()
	stmtB, err := connB.PrepareContext(ctx, query)
	if err != nil {
		t.Fatalf("connB prepare error = %v", err)
	}
	defer stmtB.Close()

	sides := []struct {
		name string
		stmt *sql.Stmt
	}{{"A", stmtA}, {"B", stmtB}}
	for id := 1; id <= 2; id++ {
		want := fmt.Sprintf("cache-%d", id)
		for _, side := range sides {
			var got string
			if err := side.stmt.QueryRowContext(ctx, id).Scan(&got); err != nil {
				t.Fatalf("side %s ID=%d query error = %v", side.name, id, err)
			}
			if got != want {
				t.Fatalf("side %s ID=%d NAME = %q, want %q", side.name, id, got, want)
			}
		}
	}
}

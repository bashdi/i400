package i400

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// integrationPackageName returns the full SQL package name the driver uses for
// the DSN (six-character base plus the derived four-character suffix), so the
// test can remove the package it created.
func integrationPackageName(t *testing.T, dsn string) string {
	t.Helper()
	cfg, err := ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	commitMode := int(serverCommitmentControl(*cfg, true, cfg.CommitmentControl))
	return packageNameFor(cfg.Package, *cfg, commitMode)
}

// registerPackageCleanup drops the SQL package LIBRARY/NAME with DLTSQLPKG,
// run through QCMDEXC on db. db must still be open at cleanup time, so it has
// to be opened before this is called. Failures are logged, not fatal.
func registerPackageCleanup(t *testing.T, db *sql.DB, library, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := fmt.Sprintf("DLTSQLPKG SQLPKG(%s/%s)", library, name)
		if _, err := db.ExecContext(ctx, "CALL QSYS2.QCMDEXC(?)", command); err != nil {
			t.Logf("cleanup: could not drop package %s/%s: %v", library, name, err)
		}
	})
}

// TestIntegrationExtendedDynamicPackage runs parameterized statements with
// extendedDynamic=true (statements stored in an IBM i SQL package) and checks
// the values read back. The package is dropped at cleanup.
func TestIntegrationExtendedDynamicPackage(t *testing.T) {
	lib := integrationLibrary(t)
	plainDB := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	dsn := integrationDSNWith(t, map[string]string{
		"extendedDynamic": "true",
		"package":         integrationName("IPK"),
		"packageLibrary":  lib,
		"packageError":    "exception",
	})
	registerPackageCleanup(t, plainDB, lib, integrationPackageName(t, dsn))

	table := integrationQualified(lib, integrationName("IEX"))
	createIntegrationTable(t, plainDB, table, "ID INTEGER NOT NULL, NAME VARCHAR(40) NOT NULL")

	db := openIntegrationDB(t, dsn)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	for i := 1; i <= 3; i++ {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, NAME) VALUES (?, ?)", table), i, fmt.Sprintf("pkg-%d", i)); err != nil {
			t.Fatalf("packaged INSERT %d error = %v", i, err)
		}
	}

	query := fmt.Sprintf("SELECT NAME FROM %s WHERE ID = ?", table)
	for _, id := range []int{2, 3} {
		var name string
		if err := conn.QueryRowContext(ctx, query, id).Scan(&name); err != nil {
			t.Fatalf("packaged SELECT ID=%d error = %v", id, err)
		}
		want := fmt.Sprintf("pkg-%d", id)
		if name != want {
			t.Fatalf("packaged SELECT ID=%d NAME = %q, want %q", id, name, want)
		}
	}
}

package i400

import (
	"fmt"
	"testing"
)

// TestIntegrationReturnGeneratedKeys inserts rows into a table with an identity
// column using returnGeneratedKeys=true and checks that LastInsertId equals the
// identity value that was actually stored, and that RowsAffected is 1.
func TestIntegrationReturnGeneratedKeys(t *testing.T) {
	lib := integrationLibrary(t)
	plainDB := openIntegrationDB(t, integrationDSN(t))
	table := integrationQualified(lib, integrationName("IGK"))
	createIntegrationTable(t, plainDB, table,
		"ID INTEGER GENERATED ALWAYS AS IDENTITY (START WITH 100, INCREMENT BY 5) NOT NULL PRIMARY KEY, NAME VARCHAR(20) NOT NULL")

	db := openIntegrationDB(t, integrationDSNWith(t, map[string]string{"returnGeneratedKeys": "true"}))
	ctx := integrationContext(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	for _, name := range []string{"first", "second", "third"} {
		result, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (NAME) VALUES (?)", table), name)
		if err != nil {
			t.Fatalf("INSERT %q error = %v", name, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			t.Fatalf("RowsAffected() error = %v", err)
		}
		if affected != 1 {
			t.Fatalf("INSERT %q RowsAffected() = %d, want 1", name, affected)
		}
		key, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("LastInsertId() error = %v", err)
		}

		var stored int64
		if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT ID FROM %s WHERE NAME = ?", table), name).Scan(&stored); err != nil {
			t.Fatalf("SELECT ID for %q error = %v", name, err)
		}
		if key != stored {
			t.Fatalf("LastInsertId() for %q = %d, stored identity = %d", name, key, stored)
		}
	}
}

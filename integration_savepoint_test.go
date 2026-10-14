package i400

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// readIntegrationIDs returns the IDs in table, ascending.
func readIntegrationIDs(t *testing.T, ctx context.Context, db *sql.DB, table string) []int {
	t.Helper()
	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT ID FROM %s ORDER BY ID", table))
	if err != nil {
		failOrSkipNotJournaled(t, "read IDs", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			failOrSkipNotJournaled(t, "Scan()", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	return ids
}

// TestIntegrationSavepoints checks SAVEPOINT, ROLLBACK TO SAVEPOINT and
// RELEASE SAVEPOINT inside a transaction. Only the work done after the
// rolled-back savepoint must disappear. Commitment control needs a library
// whose tables are journaled (see the readme).
func TestIntegrationSavepoints(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("ISP"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL PRIMARY KEY")

	insert := func(t *testing.T, tx *sql.Tx, id int) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID) VALUES (?)", table), id); err != nil {
			failOrSkipNotJournaled(t, fmt.Sprintf("INSERT ID=%d", id), err)
		}
	}

	t.Run("rollback to savepoint then commit", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			failOrSkipNotJournaled(t, "BeginTx()", err)
		}
		insert(t, tx, 1)
		if _, err := tx.ExecContext(ctx, "SAVEPOINT sp1 ON ROLLBACK RETAIN CURSORS"); err != nil {
			tx.Rollback()
			failOrSkipNotJournaled(t, "SAVEPOINT sp1", err)
		}
		insert(t, tx, 2)
		insert(t, tx, 3)
		if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT sp1"); err != nil {
			tx.Rollback()
			failOrSkipNotJournaled(t, "ROLLBACK TO SAVEPOINT sp1", err)
		}
		insert(t, tx, 4)
		if _, err := tx.ExecContext(ctx, "SAVEPOINT sp2 ON ROLLBACK RETAIN CURSORS"); err != nil {
			tx.Rollback()
			failOrSkipNotJournaled(t, "SAVEPOINT sp2", err)
		}
		insert(t, tx, 5)
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT sp2"); err != nil {
			tx.Rollback()
			failOrSkipNotJournaled(t, "RELEASE SAVEPOINT sp2", err)
		}
		if err := tx.Commit(); err != nil {
			failOrSkipNotJournaled(t, "Commit()", err)
		}

		got := readIntegrationIDs(t, ctx, db, table)
		if fmt.Sprint(got) != fmt.Sprint([]int{1, 4, 5}) {
			t.Fatalf("committed IDs = %v, want [1 4 5]", got)
		}
	})

	t.Run("rollback whole transaction", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			failOrSkipNotJournaled(t, "BeginTx()", err)
		}
		insert(t, tx, 100)
		if _, err := tx.ExecContext(ctx, "SAVEPOINT sp3 ON ROLLBACK RETAIN CURSORS"); err != nil {
			tx.Rollback()
			failOrSkipNotJournaled(t, "SAVEPOINT sp3", err)
		}
		insert(t, tx, 101)
		if err := tx.Rollback(); err != nil {
			failOrSkipNotJournaled(t, "Rollback()", err)
		}

		got := readIntegrationIDs(t, ctx, db, table)
		if fmt.Sprint(got) != fmt.Sprint([]int{1, 4, 5}) {
			t.Fatalf("IDs after rollback = %v, want [1 4 5]", got)
		}
	})
}

// failOrSkipNotJournaled skips the test when the server rejects the work with
// SQLCODE -7008: the test library is not journaled, so commitment control
// (savepoints, rollback of the transaction) is not available on its tables.
// Any other error fails the test.
func failOrSkipNotJournaled(t *testing.T, what string, err error) {
	t.Helper()
	if sqlErr, ok := sqlErrorFrom(err); ok && sqlErr.Code == -7008 {
		t.Skipf("skipping: %s needs a journaled library, and the test table is not journaled (SQLCODE -7008): %v", what, err)
	}
	t.Fatalf("%s error = %v", what, err)
}

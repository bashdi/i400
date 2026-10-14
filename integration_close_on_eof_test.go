package i400

import (
	"fmt"
	"testing"
)

// TestIntegrationCloseOnEOF iterates result sets to end of data on a single
// connection, closes them, and then runs another query on the same connection.
// With the server closing the cursor at EOF (V6R1 and later), the connection
// must stay healthy and the follow-up query must see the correct data.
func TestIntegrationCloseOnEOF(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("ICE"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL, NAME VARCHAR(20) NOT NULL")
	for i := 1; i <= 3; i++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, NAME) VALUES (?, ?)", table), i, fmt.Sprintf("row-%d", i)); err != nil {
			t.Fatalf("INSERT %d error = %v", i, err)
		}
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	for pass := 1; pass <= 2; pass++ {
		rows, err := conn.QueryContext(ctx, fmt.Sprintf("SELECT ID, NAME FROM %s ORDER BY ID", table))
		if err != nil {
			t.Fatalf("pass %d: QueryContext() error = %v", pass, err)
		}
		var got []string
		for rows.Next() {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				t.Fatalf("pass %d: Scan() error = %v", pass, err)
			}
			got = append(got, fmt.Sprintf("%d:%s", id, name))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("pass %d: rows.Err() = %v", pass, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("pass %d: rows.Close() error = %v", pass, err)
		}
		want := []string{"1:row-1", "2:row-2", "3:row-3"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("pass %d: rows = %v, want %v", pass, got, want)
		}
	}

	// The connection must still be usable after reaching EOF and closing.
	var count int
	if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil {
		t.Fatalf("follow-up COUNT(*) error = %v", err)
	}
	if count != 3 {
		t.Fatalf("follow-up COUNT(*) = %d, want 3", count)
	}
}

// TestIntegrationCloseOnEOFEmptyResult reaches end of data immediately (no
// rows), closes the result, and checks the connection is still healthy.
func TestIntegrationCloseOnEOFEmptyResult(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("ICE"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL")

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, fmt.Sprintf("SELECT ID FROM %s", table))
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("empty table returned a row")
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}

	var one int
	if err := conn.QueryRowContext(ctx, "VALUES 1").Scan(&one); err != nil {
		t.Fatalf("follow-up VALUES 1 error = %v", err)
	}
	if one != 1 {
		t.Fatalf("follow-up VALUES 1 = %d, want 1", one)
	}
}

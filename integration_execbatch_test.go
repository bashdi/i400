package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"testing"
)

// TestIntegrationExecBatch runs Conn.ExecBatch and Stmt.ExecBatch with and
// without useBlockUpdate, then reads every row back and compares it exactly.
func TestIntegrationExecBatch(t *testing.T) {
	lib := integrationLibrary(t)
	const rowCount = 100

	for _, blockUpdate := range []string{"false", "true"} {
		t.Run("useBlockUpdate="+blockUpdate, func(t *testing.T) {
			plainDB := openIntegrationDB(t, integrationDSN(t))
			table := integrationQualified(lib, integrationName("IBT"))
			createIntegrationTable(t, plainDB, table, "ID INTEGER NOT NULL, NAME VARCHAR(40) NOT NULL")

			db := openIntegrationDB(t, integrationDSNWith(t, map[string]string{"useBlockUpdate": blockUpdate}))
			ctx := integrationContext(t)
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatalf("db.Conn() error = %v", err)
			}
			defer conn.Close()

			insertRows := make([][]driver.Value, 0, rowCount)
			want := make(map[int]string, rowCount)
			for i := 1; i <= rowCount; i++ {
				name := fmt.Sprintf("batch-%03d", i)
				insertRows = append(insertRows, []driver.Value{int64(i), name})
				want[i] = name
			}

			var affected int64
			insertSQL := fmt.Sprintf("INSERT INTO %s (ID, NAME) VALUES (?, ?)", table)
			err = conn.Raw(func(dc any) error {
				c, ok := dc.(*Conn)
				if !ok {
					return fmt.Errorf("driver conn is %T, want *Conn", dc)
				}
				n, err := c.ExecBatch(ctx, insertSQL, insertRows)
				affected = n
				return err
			})
			if err != nil {
				t.Fatalf("Conn.ExecBatch INSERT error = %v", err)
			}
			if affected != rowCount {
				t.Fatalf("Conn.ExecBatch INSERT affected = %d, want %d", affected, rowCount)
			}
			assertBatchRows(t, ctx, conn, table, want)

			// Stmt.ExecBatch: prepare once, then update every row.
			updateRows := make([][]driver.Value, 0, rowCount)
			for i := 1; i <= rowCount; i++ {
				name := fmt.Sprintf("updated-%03d", i)
				updateRows = append(updateRows, []driver.Value{name, int64(i)})
				want[i] = name
			}
			err = conn.Raw(func(dc any) error {
				c, ok := dc.(*Conn)
				if !ok {
					return fmt.Errorf("driver conn is %T, want *Conn", dc)
				}
				stmt, err := c.PrepareContext(ctx, fmt.Sprintf("UPDATE %s SET NAME = ? WHERE ID = ?", table))
				if err != nil {
					return err
				}
				defer stmt.Close()
				s, ok := stmt.(*Stmt)
				if !ok {
					return fmt.Errorf("prepared statement is %T, want *Stmt", stmt)
				}
				n, err := s.ExecBatch(ctx, updateRows)
				affected = n
				return err
			})
			if err != nil {
				t.Fatalf("Stmt.ExecBatch UPDATE error = %v", err)
			}
			if affected != rowCount {
				t.Fatalf("Stmt.ExecBatch UPDATE affected = %d, want %d", affected, rowCount)
			}
			assertBatchRows(t, ctx, conn, table, want)
		})
	}
}

// assertBatchRows reads ID and NAME from table and compares them with want.
func assertBatchRows(t *testing.T, ctx context.Context, conn *sql.Conn, table string, want map[int]string) {
	t.Helper()
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("SELECT ID, NAME FROM %s ORDER BY ID", table))
	if err != nil {
		t.Fatalf("read-back query error = %v", err)
	}
	defer rows.Close()
	got := make(map[int]string, len(want))
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("read-back Scan() error = %v", err)
		}
		got[id] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read-back rows.Err() = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("read back %d rows, want %d", len(got), len(want))
	}
	for id, name := range want {
		if got[id] != name {
			t.Fatalf("ID %d NAME = %q, want %q", id, got[id], name)
		}
	}
}

package i400

import (
	"fmt"
	"testing"
)

// TestIntegrationNextResultSet calls a procedure that returns two result sets
// and reads both through database/sql Rows.NextResultSet.
func TestIntegrationNextResultSet(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("INR"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL PRIMARY KEY, NAME VARCHAR(20) NOT NULL")
	for i, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, NAME) VALUES (?, ?)", table), i+1, name); err != nil {
			t.Fatalf("INSERT %d error = %v", i+1, err)
		}
	}

	procName := integrationQualified(lib, integrationName("IRS"))
	createSQL := fmt.Sprintf(`CREATE PROCEDURE %s ()
LANGUAGE SQL
DYNAMIC RESULT SETS 2
BEGIN
  DECLARE C1 CURSOR WITH RETURN FOR SELECT ID, NAME FROM %s ORDER BY ID;
  DECLARE C2 CURSOR WITH RETURN FOR SELECT NAME FROM %s WHERE ID = 2;
  OPEN C1;
  OPEN C2;
END`, procName, table, table)
	createIntegrationObject(t, db, createSQL, "DROP PROCEDURE "+procName)

	rows, err := db.QueryContext(ctx, fmt.Sprintf("CALL %s()", procName))
	if err != nil {
		t.Fatalf("CALL error = %v", err)
	}
	defer rows.Close()

	var firstIDs []string
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("first result Scan() error = %v", err)
		}
		firstIDs = append(firstIDs, fmt.Sprintf("%d:%s", id, name))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("first result rows.Err() = %v", err)
	}
	wantFirst := []string{"1:alpha", "2:beta", "3:gamma"}
	if fmt.Sprint(firstIDs) != fmt.Sprint(wantFirst) {
		t.Fatalf("first result = %v, want %v", firstIDs, wantFirst)
	}

	if !rows.NextResultSet() {
		t.Fatalf("NextResultSet() = false, want a second result set (err = %v)", rows.Err())
	}
	var secondNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("second result Scan() error = %v", err)
		}
		secondNames = append(secondNames, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("second result rows.Err() = %v", err)
	}
	if fmt.Sprint(secondNames) != fmt.Sprint([]string{"beta"}) {
		t.Fatalf("second result = %v, want [beta]", secondNames)
	}

	if rows.NextResultSet() {
		t.Fatal("NextResultSet() = true after the last result set")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() after last result set = %v", err)
	}
}

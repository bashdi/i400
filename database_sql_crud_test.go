package i400

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	databaseSQLCRUDTestHost     = "pub400.com"
	databaseSQLCRUDTestUser     = "test"
	databaseSQLCRUDTestPassword = "test"
)

func TestDatabaseSQLCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping IBM i integration test in short mode")
	}
	if databaseSQLCRUDTestHasPlaceholders() {
		t.Skip("replace the IBM i placeholder credentials in database_sql_crud_test.go before running this test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	db, err := sql.Open("as400", databaseSQLCRUDTestDSN())
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	tableName := fmt.Sprintf("QTEMP.COPILOTCRUD_%d", time.Now().UnixNano()%1_000_000)
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "DROP TABLE "+tableName)
	}()

	createSQL := fmt.Sprintf(`CREATE TABLE %s (
	ID INTEGER NOT NULL,
    NAME VARCHAR(128) CCSID 1208 NOT NULL,
    AMOUNT INTEGER NOT NULL DEFAULT 0
)`, tableName)
	if _, err := conn.ExecContext(ctx, createSQL); err != nil {
		t.Fatalf("CREATE TABLE error = %v", err)
	}

	insertResult, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, NAME, AMOUNT) VALUES (?, ?, ?)", tableName), 1, "created", 10)
	if err != nil {
		t.Fatalf("INSERT error = %v", err)
	}
	insertedRows, err := insertResult.RowsAffected()
	if err != nil {
		t.Fatalf("INSERT RowsAffected() error = %v", err)
	}
	if insertedRows != 1 {
		t.Fatalf("INSERT RowsAffected() = %d, want 1", insertedRows)
	}

	var gotID int
	var gotName string
	var gotAmount int
	if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT ID, NAME, AMOUNT FROM %s WHERE ID = ?", tableName), 1).Scan(&gotID, &gotName, &gotAmount); err != nil {
		t.Fatalf("SELECT after INSERT error = %v", err)
	}
	if gotID != 1 || gotName != "created" || gotAmount != 10 {
		t.Fatalf("SELECT after INSERT: got (%d, %q, %d), want (1, \"created\", 10)", gotID, gotName, gotAmount)
	}

	updateResult, err := conn.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET NAME = ?, AMOUNT = ? WHERE ID = ?", tableName), "updated", 20, 1)
	if err != nil {
		t.Fatalf("UPDATE error = %v", err)
	}
	updatedRows, err := updateResult.RowsAffected()
	if err != nil {
		t.Fatalf("UPDATE RowsAffected() error = %v", err)
	}
	if updatedRows != 1 {
		t.Fatalf("UPDATE RowsAffected() = %d, want 1", updatedRows)
	}

	if err := conn.QueryRowContext(ctx, fmt.Sprintf("SELECT ID, NAME, AMOUNT FROM %s WHERE ID = ?", tableName), 1).Scan(&gotID, &gotName, &gotAmount); err != nil {
		t.Fatalf("SELECT after UPDATE error = %v", err)
	}
	if gotID != 1 || gotName != "updated" || gotAmount != 20 {
		t.Fatalf("SELECT after UPDATE: got (%d, %q, %d), want (1, \"updated\", 20)", gotID, gotName, gotAmount)
	}

	deleteResult, err := conn.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE ID = ?", tableName), 1)
	if err != nil {
		t.Fatalf("DELETE error = %v", err)
	}
	deletedRows, err := deleteResult.RowsAffected()
	if err != nil {
		t.Fatalf("DELETE RowsAffected() error = %v", err)
	}
	if deletedRows != 1 {
		t.Fatalf("DELETE RowsAffected() = %d, want 1", deletedRows)
	}
}

func databaseSQLCRUDTestDSN() string {
	query := url.Values{}
	query.Set("naming", string(NamingSQL))

	dsn := &url.URL{
		Scheme:   "as400",
		Host:     databaseSQLCRUDTestHost,
		Path:     "/QTEMP",
		RawQuery: query.Encode(),
		User:     url.UserPassword(databaseSQLCRUDTestUser, databaseSQLCRUDTestPassword),
	}
	return dsn.String()
}

func databaseSQLCRUDTestHasPlaceholders() bool {
	return strings.HasPrefix(databaseSQLCRUDTestHost, "YOUR_") ||
		strings.HasPrefix(databaseSQLCRUDTestUser, "YOUR_") ||
		strings.HasPrefix(databaseSQLCRUDTestPassword, "YOUR_")
}

package i400

import (
	"fmt"
	"testing"
	"time"
)

// TestIntegrationTimestampMicroseconds writes time.Now() (truncated to the
// microsecond) into a TIMESTAMP(6) column and reads it back. The comparison is
// on the wall-clock text at microsecond precision, so the server's time zone
// handling does not affect the result.
func TestIntegrationTimestampMicroseconds(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("ITS"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL PRIMARY KEY, TS TIMESTAMP(6) NOT NULL")

	const layout = "2006-01-02 15:04:05.000000"
	values := []time.Time{
		time.Now().Truncate(time.Microsecond),
		time.Date(2026, 2, 28, 23, 59, 59, 999999000, time.Local),
	}
	for i, v := range values {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, TS) VALUES (?, ?)", table), i+1, v); err != nil {
			t.Fatalf("INSERT ID=%d error = %v", i+1, err)
		}
	}

	for i, v := range values {
		var got time.Time
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT TS FROM %s WHERE ID = ?", table), i+1).Scan(&got); err != nil {
			t.Fatalf("SELECT ID=%d error = %v", i+1, err)
		}
		if got.Format(layout) != v.Format(layout) {
			t.Fatalf("ID=%d TS = %s, want %s", i+1, got.Format(layout), v.Format(layout))
		}
	}
}

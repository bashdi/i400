package i400

import (
	"fmt"
	"strings"
	"testing"
)

// TestIntegrationSQLErrorMessage checks the server message carried by SQLError
// for a missing table, with errors=basic and errors=full, on both the query
// path and the ExecContext (EXECUTE IMMEDIATE) path. The table name is never
// created, so the server returns SQL0204.
func TestIntegrationSQLErrorMessage(t *testing.T) {
	integrationDSN(t)
	missing := "QTEMP." + integrationName("INO")

	for _, level := range []string{"basic", "full"} {
		t.Run("errors="+level, func(t *testing.T) {
			db := openIntegrationDB(t, integrationDSNWith(t, map[string]string{"errors": level}))
			ctx := integrationContext(t)

			_, err := db.QueryContext(ctx, fmt.Sprintf("SELECT ID FROM %s", missing))
			if err == nil {
				t.Fatal("SELECT from missing table returned no error")
			}
			checkMissingTableError(t, "query", err, level)

			_, err = db.ExecContext(ctx, fmt.Sprintf("DROP TABLE %s", missing))
			if err == nil {
				t.Fatal("DROP of missing table returned no error")
			}
			checkMissingTableError(t, "exec", err, level)
		})
	}
}

func checkMissingTableError(t *testing.T, path string, err error, level string) {
	t.Helper()
	sqlErr, ok := sqlErrorFrom(err)
	if !ok {
		t.Fatalf("%s: error type = %T (%v), want *SQLError", path, err, err)
	}
	if sqlErr.MessageID != "SQL0204" {
		t.Fatalf("%s: MessageID = %q, want SQL0204 (error: %v)", path, sqlErr.MessageID, err)
	}
	if sqlErr.Code != -204 {
		t.Fatalf("%s: Code = %d, want -204", path, sqlErr.Code)
	}
	if sqlErr.State != "42704" {
		t.Fatalf("%s: State = %q, want 42704", path, sqlErr.State)
	}
	if strings.TrimSpace(sqlErr.MessageText) == "" {
		t.Fatalf("%s: MessageText is empty (error: %v)", path, err)
	}
	if !strings.Contains(sqlErr.Error(), "[SQL0204]") {
		t.Fatalf("%s: Error() = %q, want JTOpen style [SQL0204] prefix", path, sqlErr.Error())
	}
	switch level {
	case "full":
		if strings.TrimSpace(sqlErr.SecondLevelText) == "" {
			t.Fatalf("%s: errors=full SecondLevelText is empty", path)
		}
	case "basic":
		if sqlErr.SecondLevelText != "" {
			t.Fatalf("%s: errors=basic SecondLevelText = %q, want empty", path, sqlErr.SecondLevelText)
		}
	}
}

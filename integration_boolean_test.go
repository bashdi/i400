package i400

import (
	"fmt"
	"testing"
)

// booleanMinVRM is V7R5M0, the first release with the SQL BOOLEAN data type.
const booleanMinVRM = 0x070500

// TestIntegrationBoolean round-trips BOOLEAN values bound as bool, int64 and
// string, and checks each stored value comes back exactly. It skips on releases
// before V7R5 because BOOLEAN does not exist there.
func TestIntegrationBoolean(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	integrationSkipBelowVRM(t, db, booleanMinVRM, "BOOLEAN")
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("IBO"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL PRIMARY KEY, FLAG BOOLEAN NOT NULL")

	cases := []struct {
		id    int
		input any
		want  bool
	}{
		{1, true, true},
		{2, false, false},
		{3, int64(1), true},
		{4, int64(0), false},
		{5, "true", true},
		{6, "false", false},
	}
	insert := fmt.Sprintf("INSERT INTO %s (ID, FLAG) VALUES (?, ?)", table)
	for _, tc := range cases {
		if _, err := db.ExecContext(ctx, insert, tc.id, tc.input); err != nil {
			t.Fatalf("INSERT ID=%d (%v) error = %v", tc.id, tc.input, err)
		}
	}

	for _, tc := range cases {
		var got bool
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT FLAG FROM %s WHERE ID = ?", table), tc.id).Scan(&got); err != nil {
			t.Fatalf("SELECT ID=%d error = %v", tc.id, err)
		}
		if got != tc.want {
			t.Fatalf("ID=%d FLAG = %v, want %v (bound %v)", tc.id, got, tc.want, tc.input)
		}
	}
}

package i400

import (
	"fmt"
	"math/big"
	"testing"
)

// TestIntegrationDecfloat writes DECFLOAT(16) and DECFLOAT(34) values as
// decimal strings and reads them back. The driver returns DECFLOAT as a decimal
// string whose exact format may differ from the input (for example "1E+10"),
// so values are compared as exact rationals.
func TestIntegrationDecfloat(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("IDF"))
	createIntegrationTable(t, db, table, "ID INTEGER NOT NULL PRIMARY KEY, D16 DECFLOAT(16) NOT NULL, D34 DECFLOAT(34) NOT NULL")

	cases := []struct {
		id  int
		d16 string
		d34 string
	}{
		{1, "123.45", "123.45"},
		{2, "-0.001", "-0.001"},
		{3, "1234567890.123456", "1234567890123456789.012345678901234"},
		{4, "0", "-9999999999999999999999999999999999"},
		{5, "1E+10", "1E+30"},
	}

	insert := fmt.Sprintf("INSERT INTO %s (ID, D16, D34) VALUES (?, ?, ?)", table)
	for _, tc := range cases {
		if _, err := db.ExecContext(ctx, insert, tc.id, tc.d16, tc.d34); err != nil {
			t.Fatalf("INSERT ID=%d error = %v", tc.id, err)
		}
	}

	for _, tc := range cases {
		var got16, got34 string
		query := fmt.Sprintf("SELECT D16, D34 FROM %s WHERE ID = ?", table)
		if err := db.QueryRowContext(ctx, query, tc.id).Scan(&got16, &got34); err != nil {
			t.Fatalf("SELECT ID=%d error = %v", tc.id, err)
		}
		assertDecfloatEqual(t, fmt.Sprintf("ID=%d D16", tc.id), got16, tc.d16)
		assertDecfloatEqual(t, fmt.Sprintf("ID=%d D34", tc.id), got34, tc.d34)
	}
}

func assertDecfloatEqual(t *testing.T, label, got, want string) {
	t.Helper()
	wantRat, ok := new(big.Rat).SetString(want)
	if !ok {
		t.Fatalf("%s: test value %q is not a decimal", label, want)
	}
	gotRat, ok := new(big.Rat).SetString(got)
	if !ok {
		t.Fatalf("%s = %q, not a decimal", label, got)
	}
	if gotRat.Cmp(wantRat) != 0 {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

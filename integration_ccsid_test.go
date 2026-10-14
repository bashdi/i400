package i400

import (
	"fmt"
	"strings"
	"testing"
)

// TestIntegrationCCSIDRoundTrip writes text into columns with several CCSIDs
// and reads it back. Values are chosen so that each CCSID can represent them
// exactly. CHAR and GRAPHIC values are blank padded by the server, so trailing
// spaces are trimmed before comparing.
func TestIntegrationCCSIDRoundTrip(t *testing.T) {
	lib := integrationLibrary(t)
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("ICC"))
	createIntegrationTable(t, db, table, strings.Join([]string{
		"ID INTEGER NOT NULL PRIMARY KEY",
		"C37 CHAR(20) CCSID 37 NOT NULL",
		"V273 VARCHAR(60) CCSID 273 NOT NULL",
		"C1141 CHAR(20) CCSID 1141 NOT NULL",
		"M930 CHAR(40) CCSID 930 NOT NULL",
		"M5035 CHAR(40) CCSID 5035 NOT NULL",
		"G1200 GRAPHIC(10) CCSID 1200 NOT NULL",
		"U1208 VARCHAR(80) CCSID 1208 NOT NULL",
		"C1208 CHAR(30) CCSID 1208 NOT NULL",
		"CL1208 CLOB(1K) CCSID 1208 NOT NULL",
	}, ", "))

	type row struct {
		id                                                         int
		c37, v273, c1141, m930, m5035, g1200, u1208, c1208, cl1208 string
	}
	rows := []row{
		{
			id:     1,
			c37:    "HELLO 123",
			v273:   "Grüße äöü ÄÖÜ ß",
			c1141:  "€ äöü ÄÖÜ",
			m930:   "AB日本語CD",
			m5035:  "xy漢字z",
			g1200:  "日本語テキスト",
			u1208:  "Ünïcødé ✓ 漢字 emoji 😀",
			c1208:  "Ünïcødé ✓",
			cl1208: "CLOB Ünïcødé ✓ 漢字",
		},
		{
			id:     2,
			c37:    "ABCdef-/.,",
			v273:   "Straße Ärger",
			c1141:  "Preis 9,99 €",
			m930:   "日本",
			m5035:  "漢字 and ascii",
			g1200:  "テスト",
			u1208:  "plain ascii too",
			c1208:  "naïve",
			cl1208: "long CLOB value " + strings.Repeat("0123456789", 20),
		},
	}

	insert := fmt.Sprintf(
		"INSERT INTO %s (ID, C37, V273, C1141, M930, M5035, G1200, U1208, C1208, CL1208) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", table)
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, insert, r.id, r.c37, r.v273, r.c1141, r.m930, r.m5035, r.g1200, r.u1208, r.c1208, r.cl1208); err != nil {
			t.Fatalf("INSERT ID=%d error = %v", r.id, err)
		}
	}

	query := fmt.Sprintf("SELECT ID, C37, V273, C1141, M930, M5035, G1200, U1208, C1208, CL1208 FROM %s ORDER BY ID", table)
	gotRows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("SELECT error = %v", err)
	}
	defer gotRows.Close()

	index := 0
	for gotRows.Next() {
		var got row
		if err := gotRows.Scan(&got.id, &got.c37, &got.v273, &got.c1141, &got.m930, &got.m5035, &got.g1200, &got.u1208, &got.c1208, &got.cl1208); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if index >= len(rows) {
			t.Fatalf("more rows than inserted: %+v", got)
		}
		want := rows[index]
		index++
		checkCCSIDField(t, "ID", fmt.Sprint(got.id), fmt.Sprint(want.id))
		checkCCSIDField(t, "C37", strings.TrimRight(got.c37, " "), want.c37)
		checkCCSIDField(t, "V273", got.v273, want.v273)
		checkCCSIDField(t, "C1141", strings.TrimRight(got.c1141, " "), want.c1141)
		checkCCSIDField(t, "M930", strings.TrimRight(got.m930, " "), want.m930)
		checkCCSIDField(t, "M5035", strings.TrimRight(got.m5035, " "), want.m5035)
		checkCCSIDField(t, "G1200", strings.TrimRight(got.g1200, " "), want.g1200)
		checkCCSIDField(t, "U1208", got.u1208, want.u1208)
		checkCCSIDField(t, "C1208", strings.TrimRight(got.c1208, " "), want.c1208)
		checkCCSIDField(t, "CL1208", got.cl1208, want.cl1208)
	}
	if err := gotRows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	if index != len(rows) {
		t.Fatalf("read %d rows, want %d", index, len(rows))
	}
}

func checkCCSIDField(t *testing.T, column, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", column, got, want)
	}
}

package i400

import (
	"fmt"
	"testing"
	"time"
)

// TestIntegrationDateTimeFormat round-trips DATE and TIME values through
// non-ISO dateFormat/timeFormat settings. The server formats the text it
// returns in the job's format, and the driver must parse it back to the same
// calendar date and clock time.
func TestIntegrationDateTimeFormat(t *testing.T) {
	lib := integrationLibrary(t)
	plainDB := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)

	table := integrationQualified(lib, integrationName("IDT"))
	createIntegrationTable(t, plainDB, table, "ID INTEGER NOT NULL PRIMARY KEY, D DATE NOT NULL, T TIME NOT NULL")

	formats := []map[string]string{
		{"dateFormat": "usa", "timeFormat": "usa"},
		{"dateFormat": "eur", "timeFormat": "eur", "dateSeparator": "dot", "timeSeparator": "dot"},
		{"dateFormat": "dmy", "timeFormat": "hms", "dateSeparator": "dash"},
		{"dateFormat": "julian", "timeFormat": "hms"},
	}
	dates := []time.Time{
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	clocks := []time.Time{
		time.Date(0, 1, 1, 14, 30, 5, 0, time.UTC),
		time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// The data is written once with the ISO-independent binary parameters; the
	// formats only affect how the server renders what it returns.
	for i := range dates {
		if _, err := plainDB.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (ID, D, T) VALUES (?, ?, ?)", table), i+1, dates[i], clocks[i]); err != nil {
			t.Fatalf("INSERT ID=%d error = %v", i+1, err)
		}
	}

	for _, options := range formats {
		name := fmt.Sprintf("dateFormat=%s/timeFormat=%s", options["dateFormat"], options["timeFormat"])
		t.Run(name, func(t *testing.T) {
			db := openIntegrationDB(t, integrationDSNWith(t, options))
			for i := range dates {
				var gotDate, gotTime time.Time
				query := fmt.Sprintf("SELECT D, T FROM %s WHERE ID = ?", table)
				if err := db.QueryRowContext(ctx, query, i+1).Scan(&gotDate, &gotTime); err != nil {
					t.Fatalf("SELECT ID=%d error = %v", i+1, err)
				}
				if gotDate.Format("2006-01-02") != dates[i].Format("2006-01-02") {
					t.Fatalf("ID=%d D = %s, want %s", i+1, gotDate.Format("2006-01-02"), dates[i].Format("2006-01-02"))
				}
				// The USA time format ("hh:mm AM") carries no seconds.
				wantTime := clocks[i]
				if options["timeFormat"] == "usa" {
					wantTime = wantTime.Truncate(time.Minute)
				}
				if gotTime.Format("15:04:05") != wantTime.Format("15:04:05") {
					t.Fatalf("ID=%d T = %s, want %s", i+1, gotTime.Format("15:04:05"), wantTime.Format("15:04:05"))
				}
			}
		})
	}
}

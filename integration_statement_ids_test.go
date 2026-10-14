package i400

import "testing"

// Statement ids (RPB handles) must be released after queries, prepared
// statements and failed prepares, so long-lived connections never run out.
func TestIntegrationStatementIDsReleased(t *testing.T) {
	db := openIntegrationDB(t, integrationDSN(t))
	ctx := integrationContext(t)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn() error = %v", err)
	}
	defer conn.Close()

	for i := 0; i < 20; i++ {
		var one int
		if err := conn.QueryRowContext(ctx, "SELECT 1 FROM SYSIBM.SYSDUMMY1").Scan(&one); err != nil {
			t.Fatalf("query %d error = %v", i, err)
		}
		stmt, err := conn.PrepareContext(ctx, "SELECT CAST(? AS INTEGER) FROM SYSIBM.SYSDUMMY1")
		if err != nil {
			t.Fatalf("prepare %d error = %v", i, err)
		}
		if err := stmt.QueryRowContext(ctx, i).Scan(&one); err != nil {
			t.Fatalf("prepared query %d error = %v", i, err)
		}
		stmt.Close()
		if _, err := conn.QueryContext(ctx, "SELECT * FROM QSYS2.I400_NO_SUCH_TABLE"); err == nil {
			t.Fatal("query on a missing table succeeded")
		}
	}

	var used int
	if err := conn.Raw(func(driverConn any) error {
		c := driverConn.(*Conn)
		c.nameMu.Lock()
		used = len(c.usedStatementHandles)
		c.nameMu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("Raw() error = %v", err)
	}
	if used != 0 {
		t.Fatalf("statement ids still in use = %d, want 0", used)
	}
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1 FROM SYSIBM.SYSDUMMY1").Scan(&one); err != nil {
		t.Fatalf("follow-up query error = %v", err)
	}
}

package i400

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestIntegrationDeadlineCancelKeepsConnection checks that a context deadline
// that expires during a statement does not drop the connection when native
// cancel is available. The same *Conn must serve the next query, and the
// deadline error must come back shortly after the deadline.
func TestIntegrationDeadlineCancelKeepsConnection(t *testing.T) {
	dsn := integrationDSNWith(t, map[string]string{"queryTimeoutMechanism": "cancel"})
	db := openIntegrationDB(t, dsn)

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	driverConn := func() *Conn {
		t.Helper()
		var c *Conn
		if err := conn.Raw(func(dc any) error {
			var ok bool
			c, ok = dc.(*Conn)
			if !ok {
				return fmt.Errorf("driver connection is %T, want *i400.Conn", dc)
			}
			return nil
		}); err != nil {
			t.Fatalf("conn.Raw() error = %v", err)
		}
		return c
	}

	before := driverConn()
	if !before.canCancelNatively() {
		t.Skip("server did not report native cancel metadata; the deadline path is not exercised")
	}

	const deadline = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	var count int64
	err = conn.QueryRowContext(ctx, slowIntegrationQuery).Scan(&count)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query error = %v (count %d), want context.DeadlineExceeded", err, count)
	}
	if elapsed > deadline+10*time.Second {
		t.Fatalf("query returned after %v; want it back within a few seconds of the %v deadline", elapsed, deadline)
	}

	after := driverConn()
	if after != before {
		t.Fatal("conn.Raw returned a different *i400.Conn after the deadline; the connection was replaced")
	}
	if !after.IsValid() {
		t.Fatal("connection invalidated by the deadline, want it kept usable after native cancel")
	}

	followCtx, followCancel := context.WithTimeout(context.Background(), time.Minute)
	defer followCancel()
	var one int
	if err := conn.QueryRowContext(followCtx, "SELECT 1 FROM SYSIBM.SYSDUMMY1").Scan(&one); err != nil {
		t.Fatalf("query after the deadline error = %v", err)
	}
	if one != 1 {
		t.Fatalf("query after the deadline = %d, want 1", one)
	}
}

package i400

import (
	"context"
	"errors"
	"testing"
	"time"
)

// slowIntegrationQuery takes far longer than the deadlines used below: a
// three-way cross join of a catalog view is about 10^12 rows.
const slowIntegrationQuery = "SELECT COUNT(*) FROM QSYS2.SYSCOLUMNS A CROSS JOIN QSYS2.SYSCOLUMNS B CROSS JOIN QSYS2.SYSCOLUMNS C"

// TestIntegrationQueryTimeoutMechanism checks that a context deadline stops a
// long-running query under both queryTimeoutMechanism values, that the error
// comes back promptly, and that the driver can run another query afterwards.
func TestIntegrationQueryTimeoutMechanism(t *testing.T) {
	integrationDSN(t)
	for _, mechanism := range []string{"cancel", "qqrytimlmt"} {
		t.Run(mechanism, func(t *testing.T) {
			db := openIntegrationDB(t, integrationDSNWith(t, map[string]string{"queryTimeoutMechanism": mechanism}))

			deadline := 3 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()

			start := time.Now()
			var count int64
			err := db.QueryRowContext(ctx, slowIntegrationQuery).Scan(&count)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatalf("query finished with count %d; expected the %v deadline to stop it", count, deadline)
			}
			if elapsed > 60*time.Second {
				t.Fatalf("query took %v to stop after a %v deadline", elapsed, deadline)
			}
			// qqrytimlmt (JTOpen's default) compares only the optimizer's estimate
			// with the limit, before the query runs. The server therefore rejects
			// this query up front with SQL0666 (SQLCODE -666), which is the expected
			// outcome. A deadline stop is accepted too.
			if mechanism == "qqrytimlmt" {
				var sqlErr *SQLError
				if !(errors.As(err, &sqlErr) && sqlErr.Code == -666) && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("qqrytimlmt error = %v, want SQL0666 or deadline exceeded", err)
				}
			}

			followCtx, followCancel := context.WithTimeout(context.Background(), time.Minute)
			defer followCancel()
			var one int
			if err := db.QueryRowContext(followCtx, "SELECT 1 FROM SYSIBM.SYSDUMMY1").Scan(&one); err != nil {
				t.Fatalf("query after timeout error = %v", err)
			}
			if one != 1 {
				t.Fatalf("query after timeout = %d, want 1", one)
			}
		})
	}
}

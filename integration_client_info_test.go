package i400

import (
	"database/sql"
	"strings"
	"testing"
)

// serverClientApplName asks the server which application name it sees for the connection.
func serverClientApplName(t *testing.T, conn *sql.Conn) string {
	t.Helper()
	var name sql.NullString
	if err := conn.QueryRowContext(integrationContext(t), "SELECT CURRENT CLIENT_APPLNAME FROM SYSIBM.SYSDUMMY1").Scan(&name); err != nil {
		t.Fatalf("SELECT CLIENT_APPLNAME error = %v", err)
	}
	return strings.TrimRight(name.String, " ")
}

// TestIntegrationClientInfo checks that the DSN applicationName is seen by the
// server, and that Conn.SetClientInfo changes the value the server reports for
// that connection, both through the driver and on the server side.
func TestIntegrationClientInfo(t *testing.T) {
	db := openIntegrationDB(t, integrationDSNWith(t, map[string]string{"applicationName": "it-dsn-app"}))
	ctx := integrationContext(t)

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	if got := serverClientApplName(t, conn); got != "it-dsn-app" {
		t.Fatalf("server CLIENT_APPLNAME after DSN = %q, want %q", got, "it-dsn-app")
	}

	err = conn.Raw(func(dc any) error {
		c, ok := dc.(*Conn)
		if !ok {
			t.Fatalf("driver conn is %T, want *Conn", dc)
		}
		if got := c.ClientInfo("ApplicationName"); got != "it-dsn-app" {
			t.Errorf("ClientInfo(ApplicationName) before change = %q, want %q", got, "it-dsn-app")
		}
		if err := c.SetClientInfo(ctx, "ApplicationName", "it-runtime-app"); err != nil {
			t.Fatalf("SetClientInfo() error = %v", err)
		}
		if got := c.ClientInfo("ApplicationName"); got != "it-runtime-app" {
			t.Errorf("ClientInfo(ApplicationName) after change = %q, want %q", got, "it-runtime-app")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("conn.Raw() error = %v", err)
	}

	if got := serverClientApplName(t, conn); got != "it-runtime-app" {
		t.Fatalf("server CLIENT_APPLNAME after SetClientInfo = %q, want %q", got, "it-runtime-app")
	}
}

package i400

import (
	"strings"
	"testing"
)

// TestIntegrationPasswordLevelSignon connects with the DSN credentials. The
// password level the server negotiates (level 4 on current systems) is chosen by
// the server, so the test checks that signon succeeds and that the server
// reports the DSN user as the current user.
func TestIntegrationPasswordLevelSignon(t *testing.T) {
	dsn := integrationDSN(t)
	cfg, err := ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.User == "" || cfg.Password == "" {
		t.Skipf("%s must include USER:PASSWORD to test signon", integrationDSNEnv)
	}

	db := openIntegrationDB(t, dsn)
	ctx := integrationContext(t)

	var currentUser string
	if err := db.QueryRowContext(ctx, "SELECT CURRENT_USER FROM SYSIBM.SYSDUMMY1").Scan(&currentUser); err != nil {
		t.Fatalf("SELECT CURRENT_USER error = %v", err)
	}
	currentUser = strings.TrimRight(currentUser, " ")
	if !strings.EqualFold(currentUser, cfg.User) {
		t.Fatalf("CURRENT_USER = %q, want DSN user %q", currentUser, cfg.User)
	}
}

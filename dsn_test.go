package i400

import (
	"strings"
	"testing"
)

func TestParseDSN(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/schema?naming=system&libraries=LIB1,LIB2&tls=true")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}

	if cfg.Host != "host" {
		t.Fatalf("Host = %q, want %q", cfg.Host, "host")
	}
	if cfg.User != "user" {
		t.Fatalf("User = %q, want %q", cfg.User, "user")
	}
	if cfg.Password != "pass" {
		t.Fatalf("Password = %q, want %q", cfg.Password, "pass")
	}
	if cfg.Schema != "schema" {
		t.Fatalf("Schema = %q, want %q", cfg.Schema, "schema")
	}
	if !cfg.UseTLS {
		t.Fatalf("UseTLS = false, want true")
	}
	if cfg.Naming != NamingSystem {
		t.Fatalf("Naming = %q, want %q", cfg.Naming, NamingSystem)
	}
	if got := strings.Join(cfg.Libraries, ","); got != "LIB1,LIB2" {
		t.Fatalf("Libraries = %q, want %q", got, "LIB1,LIB2")
	}
}

func TestParseDSNExplicitPortAndIPv6(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@[fd13:ac12:18:17::16]:1234/mylib?secure=false")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}

	if cfg.Host != "fd13:ac12:18:17::16" {
		t.Fatalf("Host = %q, want %q", cfg.Host, "fd13:ac12:18:17::16")
	}
	if cfg.Port != 1234 {
		t.Fatalf("Port = %d, want %d", cfg.Port, 1234)
	}
	if cfg.UseTLS {
		t.Fatalf("UseTLS = true, want false")
	}
	if cfg.DateFormat != defaultDateFormat {
		t.Fatalf("DateFormat = %d, want %d", cfg.DateFormat, defaultDateFormat)
	}
	if cfg.CommitmentControl != defaultCommitmentControl {
		t.Fatalf("CommitmentControl = %d, want %d", cfg.CommitmentControl, defaultCommitmentControl)
	}
}

func TestParseDSNSessionOptions(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/schema?dateFormat=iso&dateSeparator=dash&timeFormat=eur&timeSeparator=colon&decimalSeparator=comma&commitmentControl=rr")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}

	if cfg.DateFormat != 5 {
		t.Fatalf("DateFormat = %d, want 5", cfg.DateFormat)
	}
	if cfg.DateSeparator != 1 {
		t.Fatalf("DateSeparator = %d, want 1", cfg.DateSeparator)
	}
	if cfg.TimeFormat != 3 {
		t.Fatalf("TimeFormat = %d, want 3", cfg.TimeFormat)
	}
	if cfg.TimeSeparator != 0 {
		t.Fatalf("TimeSeparator = %d, want 0", cfg.TimeSeparator)
	}
	if cfg.DecimalSeparator != 1 {
		t.Fatalf("DecimalSeparator = %d, want 1", cfg.DecimalSeparator)
	}
	if cfg.CommitmentControl != 4 {
		t.Fatalf("CommitmentControl = %d, want 4", cfg.CommitmentControl)
	}
}

func TestParseDSNStatementCacheSize(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/QTEMP?statementCacheSize=3")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.StatementCacheSize != 3 {
		t.Fatalf("StatementCacheSize = %d, want 3", cfg.StatementCacheSize)
	}
}

func TestParseDSNRejectsNegativeStatementCacheSize(t *testing.T) {
	_, err := ParseDSN("as400://user:pass@host/QTEMP?statementCacheSize=-1")
	if err == nil {
		t.Fatal("ParseDSN() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "statementCacheSize") {
		t.Fatalf("ParseDSN() error = %v, want statementCacheSize detail", err)
	}
}

func TestParseDSNCursorOptions(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/QTEMP?scrollableCursors=true&holdCursors=true")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.ScrollableCursors || !cfg.HoldCursors {
		t.Fatalf("cursor options = scrollable=%v hold=%v, want both true", cfg.ScrollableCursors, cfg.HoldCursors)
	}
}

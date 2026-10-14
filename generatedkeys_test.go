package i400

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// packedGeneratedKeyForTest encodes value as the 16-byte 30,0 packed decimal that the
// server places in SQLCA bytes 72..87.
func packedGeneratedKeyForTest(value int64) []byte {
	digits := fmt.Sprintf("%030d", value)
	nibbles := "0" + digits + "C" // pad to an even nibble count, positive sign
	out := make([]byte, len(nibbles)/2)
	for i := range out {
		hi := nibbleValueForTest(nibbles[2*i])
		lo := nibbleValueForTest(nibbles[2*i+1])
		out[i] = hi<<4 | lo
	}
	return out
}

func nibbleValueForTest(c byte) byte {
	if c == 'C' {
		return 0xC
	}
	return c - '0'
}

func sqlcaPayloadWithKeyForTest(t *testing.T, key []byte) []byte {
	t.Helper()
	sqlca := make([]byte, 136)
	copy(sqlca[72:88], key)
	return llcpForTest(CodePointSQLCA, sqlca)
}

func TestSQLCAGeneratedKeyReachesLastInsertID(t *testing.T) {
	info, err := parseSQLCAFromPayload(sqlcaPayloadWithKeyForTest(t, packedGeneratedKeyForTest(1234567)))
	if err != nil {
		t.Fatalf("parseSQLCAFromPayload() error = %v", err)
	}
	if !info.HasGeneratedKey || info.GeneratedKey != 1234567 {
		t.Fatalf("generated key = (%v, %d), want (true, 1234567)", info.HasGeneratedKey, info.GeneratedKey)
	}
	result := &execResult{rowsAffected: 1, lastInsertID: info.GeneratedKey, hasLastInsert: info.HasGeneratedKey}
	id, err := result.LastInsertId()
	if err != nil || id != 1234567 {
		t.Fatalf("LastInsertId() = (%d, %v), want (1234567, nil)", id, err)
	}
}

func TestLastInsertIDWithoutKeyWrapsErrUnsupported(t *testing.T) {
	_, err := (&execResult{}).LastInsertId()
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("LastInsertId() error = %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "returnGeneratedKeys") {
		t.Fatalf("LastInsertId() error = %q, want a hint about returnGeneratedKeys", err)
	}
}

func TestIsInsertStatement(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO t VALUES (1)":                       true,
		"  insert into t values (1)":                     true,
		"\n\t/* note */ -- line\n  Insert into t values": true,
		"INSERT(":                        true,
		"INSERTED":                       false,
		"SELECT 1 FROM sysibm.sysdummy1": false,
		"UPDATE t SET a = 1":             false,
		"-- only a comment":              false,
		"/* unterminated":                false,
	}
	for query, want := range cases {
		if got := isInsertStatement(query); got != want {
			t.Errorf("isInsertStatement(%q) = %v, want %v", query, got, want)
		}
	}
}

func TestWrapInsertForGeneratedKeys(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"plain", "INSERT INTO t (a) VALUES (1)", "SELECT *SQLGENCOLUMNS FROM NEW TABLE(\nINSERT INTO t (a) VALUES (1)\n)"},
		{"leading whitespace", "   \n\tinsert into t values (?)", "SELECT *SQLGENCOLUMNS FROM NEW TABLE(\ninsert into t values (?)\n)"},
		{"leading comments", "/* hi */ -- there\n  INSERT INTO t VALUES (2)", "SELECT *SQLGENCOLUMNS FROM NEW TABLE(\n/* hi */ -- there\n  INSERT INTO t VALUES (2)\n)"},
		{"trailing semicolon", "INSERT INTO t VALUES (3) ;  \n", "SELECT *SQLGENCOLUMNS FROM NEW TABLE(\nINSERT INTO t VALUES (3)\n)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wrapInsertForGeneratedKeys(tc.query)
			if err != nil {
				t.Fatalf("wrapInsertForGeneratedKeys() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("wrapInsertForGeneratedKeys() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestWrapRefusesNonInsert(t *testing.T) {
	for _, query := range []string{"UPDATE t SET a = 1", "SELECT * FROM t", "DELETE FROM t", "CALL p()", "-- x"} {
		if _, err := wrapInsertForGeneratedKeys(query); !errors.Is(err, ErrUnsupported) {
			t.Errorf("wrapInsertForGeneratedKeys(%q) error = %v, want ErrUnsupported", query, err)
		}
	}
}

type fakeGeneratedKeyRows struct {
	columns []string
	rows    [][]driver.Value
	pos     int
}

func (r *fakeGeneratedKeyRows) Columns() []string { return r.columns }
func (r *fakeGeneratedKeyRows) Close() error      { return nil }
func (r *fakeGeneratedKeyRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

func TestReadGeneratedKeyRowsReportsLastKeyAndRowCount(t *testing.T) {
	rows := &fakeGeneratedKeyRows{
		columns: []string{"ID", "NAME"},
		rows: [][]driver.Value{
			{int64(10), "a"},
			{" 11 ", "b"},
			{[]byte("12"), "c"},
		},
	}
	result, err := readGeneratedKeyRows(rows)
	if err != nil {
		t.Fatalf("readGeneratedKeyRows() error = %v", err)
	}
	if n, _ := result.RowsAffected(); n != 3 {
		t.Fatalf("RowsAffected() = %d, want 3", n)
	}
	if id, err := result.LastInsertId(); err != nil || id != 12 {
		t.Fatalf("LastInsertId() = (%d, %v), want (12, nil)", id, err)
	}
}

func TestReadGeneratedKeyRowsWithNoRows(t *testing.T) {
	result, err := readGeneratedKeyRows(&fakeGeneratedKeyRows{columns: []string{"ID"}})
	if err != nil {
		t.Fatalf("readGeneratedKeyRows() error = %v", err)
	}
	if n, _ := result.RowsAffected(); n != 0 {
		t.Fatalf("RowsAffected() = %d, want 0", n)
	}
	if _, err := result.LastInsertId(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("LastInsertId() error = %v, want ErrUnsupported", err)
	}
}

func TestReadGeneratedKeyRowsRejectsNullKey(t *testing.T) {
	rows := &fakeGeneratedKeyRows{columns: []string{"ID"}, rows: [][]driver.Value{{nil}}}
	if _, err := readGeneratedKeyRows(rows); err == nil {
		t.Fatal("readGeneratedKeyRows() error = nil, want error for NULL key")
	}
}

func TestReturnGeneratedKeysDSNOption(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pw@host/?returnGeneratedKeys=true")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.ReturnGeneratedKeys {
		t.Fatal("ReturnGeneratedKeys = false, want true")
	}

	cfg, err = ParseDSN("as400://user:pw@host/")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.ReturnGeneratedKeys {
		t.Fatal("ReturnGeneratedKeys default = true, want false")
	}

	if _, err := ParseDSN("as400://user:pw@host/?returnGeneratedKeys=maybe"); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("ParseDSN(invalid) error = %v, want ErrInvalidDSN", err)
	}
}

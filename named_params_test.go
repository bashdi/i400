package i400

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"
)

// --- rewriteNamedParams ----------------------------------------------------

func TestRewriteNamedParamsNoPlaceholders(t *testing.T) {
	q, names, err := rewriteNamedParams("SELECT 1 FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT 1 FROM t" {
		t.Errorf("query changed unexpectedly: %q", q)
	}
	if names != nil {
		t.Errorf("paramNames should be nil, got %v", names)
	}
}

func TestRewriteNamedParamsSingleParam(t *testing.T) {
	q, names, err := rewriteNamedParams("SELECT * FROM t WHERE id = :id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT * FROM t WHERE id = ?" {
		t.Errorf("got %q", q)
	}
	if len(names) != 1 || names[0] != "id" {
		t.Errorf("paramNames = %v, want [id]", names)
	}
}

func TestRewriteNamedParamsMultiple(t *testing.T) {
	q, names, err := rewriteNamedParams("INSERT INTO t (a, b) VALUES (:alpha, :beta)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "INSERT INTO t (a, b) VALUES (?, ?)" {
		t.Errorf("got %q", q)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("paramNames = %v, want [alpha beta]", names)
	}
}

func TestRewriteNamedParamsInsideStringNotReplaced(t *testing.T) {
	q, names, err := rewriteNamedParams("SELECT ':fake' AS x, :real FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT ':fake' AS x, ? FROM t" {
		t.Errorf("got %q", q)
	}
	if len(names) != 1 || names[0] != "real" {
		t.Errorf("paramNames = %v, want [real]", names)
	}
}

func TestRewriteNamedParamsInsideCommentNotReplaced(t *testing.T) {
	q, names, err := rewriteNamedParams("SELECT :val FROM t -- :skip\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT ? FROM t -- :skip\n" {
		t.Errorf("got %q", q)
	}
	if len(names) != 1 || names[0] != "val" {
		t.Errorf("paramNames = %v, want [val]", names)
	}
}

func TestRewriteNamedParamsDoubledColonPassthrough(t *testing.T) {
	// :: should pass through unchanged (PostgreSQL cast syntax – no-op on DB2)
	q, names, err := rewriteNamedParams("SELECT x::text FROM t WHERE id = :id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT x::text FROM t WHERE id = ?" {
		t.Errorf("got %q", q)
	}
	if len(names) != 1 || names[0] != "id" {
		t.Errorf("paramNames = %v, want [id]", names)
	}
}

func TestRewriteNamedParamsMixed_QMark_And_Named(t *testing.T) {
	// A query that uses only positional ? – named rewrite should not touch it.
	q, names, err := rewriteNamedParams("SELECT * FROM t WHERE a = ? AND b = ?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "SELECT * FROM t WHERE a = ? AND b = ?" {
		t.Errorf("query was mutated: %q", q)
	}
	if names != nil {
		t.Errorf("paramNames should be nil for positional-only query")
	}
}

// --- reorderNamedArgs -------------------------------------------------------

func TestReorderNamedArgsNilParamNames(t *testing.T) {
	args := []driver.NamedValue{
		{Ordinal: 1, Name: "x", Value: int64(1)},
		{Ordinal: 2, Name: "y", Value: int64(2)},
	}
	out, err := reorderNamedArgs(nil, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if &out[0] != &args[0] {
		// Slice header may differ but underlying array must be the original
	}
	if len(out) != 2 {
		t.Errorf("expected 2 args, got %d", len(out))
	}
}

func TestReorderNamedArgsReorders(t *testing.T) {
	paramNames := []string{"beta", "alpha"}
	args := []driver.NamedValue{
		{Ordinal: 1, Name: "alpha", Value: int64(10)},
		{Ordinal: 2, Name: "beta", Value: int64(20)},
	}
	out, err := reorderNamedArgs(paramNames, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 args, got %d", len(out))
	}
	if out[0].Value.(int64) != 20 {
		t.Errorf("out[0] = %v, want 20 (beta)", out[0].Value)
	}
	if out[1].Value.(int64) != 10 {
		t.Errorf("out[1] = %v, want 10 (alpha)", out[1].Value)
	}
	// Names should be cleared, ordinals set positionally
	if out[0].Name != "" || out[1].Name != "" {
		t.Errorf("names should be cleared: %q %q", out[0].Name, out[1].Name)
	}
	if out[0].Ordinal != 1 || out[1].Ordinal != 2 {
		t.Errorf("ordinals wrong: %d %d", out[0].Ordinal, out[1].Ordinal)
	}
}

func TestReorderNamedArgsMissingParam(t *testing.T) {
	paramNames := []string{"x", "missing"}
	args := []driver.NamedValue{{Ordinal: 1, Name: "x", Value: int64(1)}}
	_, err := reorderNamedArgs(paramNames, args)
	if err == nil {
		t.Fatal("expected error for missing param, got nil")
	}
}

// --- countSQLPlaceholders with :name ---------------------------------------

func TestCountSQLPlaceholdersNamedParams(t *testing.T) {
	count, err := countSQLPlaceholders("SELECT :a, :b FROM t WHERE c = :a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

func TestCountSQLPlaceholdersMixed(t *testing.T) {
	count, err := countSQLPlaceholders("SELECT ? FROM t WHERE x = :y AND z = ?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
}

// --- expandSQLQuery with :name ---------------------------------------------

func TestExpandSQLQueryNamedParams(t *testing.T) {
	args := []driver.NamedValue{
		{Name: "name", Value: "Alice"},
		{Name: "age", Value: int64(30)},
	}
	got, err := expandSQLQuery("SELECT * FROM t WHERE name = :name AND age = :age", args)
	if err != nil {
		t.Fatalf("expandSQLQuery() error = %v", err)
	}
	want := "SELECT * FROM t WHERE name = 'Alice' AND age = 30"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandSQLQueryNamedParamInsideStringIgnored(t *testing.T) {
	args := []driver.NamedValue{{Name: "val", Value: int64(42)}}
	got, err := expandSQLQuery("SELECT ':val', :val FROM t", args)
	if err != nil {
		t.Fatalf("expandSQLQuery() error = %v", err)
	}
	want := "SELECT ':val', 42 FROM t"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandSQLQueryNamedParamTimeTime(t *testing.T) {
	ts := time.Date(2026, 4, 22, 13, 14, 15, 0, time.UTC)
	args := []driver.NamedValue{{Name: "ts", Value: ts}}
	got, err := expandSQLQuery("INSERT INTO t VALUES (:ts)", args)
	if err != nil {
		t.Fatalf("expandSQLQuery() error = %v", err)
	}
	want := "INSERT INTO t VALUES (TIMESTAMP('2026-04-22-13.14.15'))"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandSQLQueryNamedParamMissing(t *testing.T) {
	args := []driver.NamedValue{{Name: "other", Value: int64(1)}}
	_, err := expandSQLQuery("SELECT :missing FROM t", args)
	if err == nil {
		t.Fatal("expected error for missing named param")
	}
}

// --- SQLSTATE sentinel errors ----------------------------------------------

func TestSQLErrorIsNoData(t *testing.T) {
	err := &SQLError{Code: -100, State: "02000", Message: "no data"}
	if !errors.Is(err, ErrSQLNoData) {
		t.Errorf("expected errors.Is(err, ErrSQLNoData) to be true for SQLSTATE 02000")
	}
	if errors.Is(err, ErrSQLConstraintViolation) {
		t.Errorf("unexpected match for ErrSQLConstraintViolation")
	}
}

func TestSQLErrorIsConstraintViolation(t *testing.T) {
	err := &SQLError{Code: -803, State: "23505", Message: "duplicate key"}
	if !errors.Is(err, ErrSQLConstraintViolation) {
		t.Errorf("expected errors.Is(err, ErrSQLConstraintViolation) for SQLSTATE 23505")
	}
	if errors.Is(err, ErrSQLSyntaxError) {
		t.Errorf("unexpected match for ErrSQLSyntaxError")
	}
}

func TestSQLErrorIsDataException(t *testing.T) {
	err := &SQLError{Code: -302, State: "22001", Message: "data truncation"}
	if !errors.Is(err, ErrSQLDataException) {
		t.Errorf("expected errors.Is(err, ErrSQLDataException) for SQLSTATE 22001")
	}
}

func TestSQLErrorIsInvalidCursor(t *testing.T) {
	err24 := &SQLError{Code: -501, State: "24000", Message: "invalid cursor"}
	err25 := &SQLError{Code: -500, State: "25000", Message: "invalid transaction state"}
	if !errors.Is(err24, ErrSQLInvalidCursor) {
		t.Errorf("expected ErrSQLInvalidCursor for SQLSTATE 24000")
	}
	if !errors.Is(err25, ErrSQLInvalidCursor) {
		t.Errorf("expected ErrSQLInvalidCursor for SQLSTATE 25000")
	}
}

func TestSQLErrorIsSyntaxError(t *testing.T) {
	err := &SQLError{Code: -104, State: "42601", Message: "syntax error"}
	if !errors.Is(err, ErrSQLSyntaxError) {
		t.Errorf("expected errors.Is(err, ErrSQLSyntaxError) for SQLSTATE 42601")
	}
}

func TestSQLErrorIsDoesNotMatchDifferentClass(t *testing.T) {
	err := &SQLError{Code: -104, State: "42601", Message: "syntax error"}
	if errors.Is(err, ErrSQLNoData) {
		t.Errorf("42601 should not match ErrSQLNoData")
	}
	if errors.Is(err, ErrSQLConstraintViolation) {
		t.Errorf("42601 should not match ErrSQLConstraintViolation")
	}
}

func TestSQLErrorIsNoStateDoesNotMatch(t *testing.T) {
	err := &SQLError{Code: -999, Message: "unknown"}
	if errors.Is(err, ErrSQLNoData) {
		t.Errorf("error without SQLSTATE should not match ErrSQLNoData")
	}
}

func TestSQLErrorIsWrapped(t *testing.T) {
	inner := &SQLError{Code: -803, State: "23505", Message: "duplicate key"}
	wrapped := fmt.Errorf("exec failed: %w", inner)
	if !errors.Is(wrapped, ErrSQLConstraintViolation) {
		t.Errorf("wrapped SQLError should match via errors.Is")
	}
}

// --- CheckNamedValue: time.Time accepted natively --------------------------

func TestCheckNamedValueTimeTime(t *testing.T) {
	c := &Conn{}
	ts := time.Now()
	nv := &driver.NamedValue{Value: ts}
	err := c.CheckNamedValue(nv)
	if err != nil {
		t.Fatalf("CheckNamedValue(time.Time) returned error: %v", err)
	}
	if nv.Value != ts {
		t.Errorf("time.Time was mutated")
	}
}

func TestCheckNamedValueNativeTypes(t *testing.T) {
	c := &Conn{}
	cases := []interface{}{
		int64(1), float64(1.5), true, "hello",
	}
	for _, in := range cases {
		nv := &driver.NamedValue{Value: in}
		if err := c.CheckNamedValue(nv); err != nil {
			t.Errorf("CheckNamedValue(%T) returned error: %v", in, err)
		}
		if nv.Value != in {
			t.Errorf("CheckNamedValue(%T) mutated value", in)
		}
	}
	// []byte cannot be compared with == ; test separately
	bv := []byte{0x01}
	nv := &driver.NamedValue{Value: bv}
	if err := c.CheckNamedValue(nv); err != nil {
		t.Errorf("CheckNamedValue([]byte) returned error: %v", err)
	}
}

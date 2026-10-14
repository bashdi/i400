package i400

import (
	"context"
	"database/sql/driver"
	"io"
	"strings"
	"testing"
)

// uqDrain reads every row of a one-column result, closes it and returns the values.
func uqDrain(t *testing.T, rows driver.Rows) []driver.Value {
	t.Helper()
	dest := make([]driver.Value, len(rows.Columns()))
	var values []driver.Value
	for {
		err := rows.Next(dest)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		values = append(values, dest[0])
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return values
}

// uqIntValue converts a decoded INTEGER value for comparison.
func uqIntValue(t *testing.T, v driver.Value) int64 {
	t.Helper()
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	}
	t.Fatalf("value %v (%T) is not an integer", v, v)
	return 0
}

// uqQueryOne runs a parameterized single-row query with the value 42 and checks it.
func uqQueryOne(t *testing.T, c *Conn, query string) {
	t.Helper()
	rows, err := c.QueryContext(context.Background(), query, []driver.NamedValue{{Ordinal: 1, Value: int64(42)}})
	if err != nil {
		t.Fatalf("QueryContext(%q) error = %v", query, err)
	}
	values := uqDrain(t, rows)
	if len(values) != 1 || uqIntValue(t, values[0]) != 42 {
		t.Fatalf("rows = %v, want one row with 42", values)
	}
}

// uqPositionalSequence is the request flow of one parameterized query with no
// statement cache: the transient statement is prepared, its descriptor is
// changed, the cursor is opened and fetched, the descriptor is deleted, and
// closing the rows closes the cursor and deletes the statement's RPB.
var uqPositionalSequence = []uint16{
	FunctionCreateRPB,
	FunctionPrepareDescribe,
	FunctionChangeDescriptor,
	FunctionOpenDescribe,
	FunctionDeleteDescriptor,
	FunctionFetch,
	FunctionClose,
	FunctionDeleteRPB,
}

func TestUQQueryContextPositionalArgs(t *testing.T) {
	f := newUQFake(t)
	uqPushSingleRowQuery(f)
	c := f.conn(Config{})

	uqQueryOne(t, c, "SELECT A FROM T WHERE A = ?")
	uqCheckIDs(t, f, uqPositionalSequence...)
}

func TestUQQueryContextNamedArgs(t *testing.T) {
	f := newUQFake(t)
	uqPushSingleRowQuery(f)
	c := f.conn(Config{})

	rows, err := c.QueryContext(context.Background(), "SELECT A FROM T WHERE A = :id", []driver.NamedValue{{Name: "id", Ordinal: 1, Value: int64(42)}})
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	values := uqDrain(t, rows)
	if len(values) != 1 || uqIntValue(t, values[0]) != 42 {
		t.Fatalf("rows = %v, want one row with 42", values)
	}
	uqCheckIDs(t, f, uqPositionalSequence...)
}

// TestUQQueryContextMissingNamedArgFailsBeforeWire checks that an unbound
// named parameter is reported without sending anything to the server.
func TestUQQueryContextMissingNamedArgFailsBeforeWire(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})

	_, err := c.QueryContext(context.Background(), "SELECT A FROM T WHERE A = :id", []driver.NamedValue{{Name: "other", Ordinal: 1, Value: int64(1)}})
	if err == nil || !strings.Contains(err.Error(), `named parameter "id" not provided`) {
		t.Fatalf("QueryContext() error = %v, want missing named parameter", err)
	}
	if ids := f.ids(); len(ids) != 0 {
		t.Fatalf("requests sent = %x, want none", ids)
	}
}

// TestUQQueryPreparedCachedReusesStatement checks that a second execution of
// the same text with a statement cache does not send another PREPARE.
func TestUQQueryPreparedCachedReusesStatement(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam))
	f.push(FunctionOpenDescribe, uqOpenReply(), uqOpenReply())
	f.push(FunctionFetch,
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil))
	c := f.conn(Config{StatementCacheSize: 2})

	const query = "SELECT A FROM T WHERE A = ?"
	uqQueryOne(t, c, query)
	uqQueryOne(t, c, query)

	if n := f.count(FunctionPrepareDescribe); n != 1 {
		t.Fatalf("PREPARE_DESCRIBE sent %d times, want 1", n)
	}
	if n := f.count(FunctionCreateRPB); n != 1 {
		t.Fatalf("Create RPB sent %d times, want 1", n)
	}
	if n := f.count(FunctionDeleteRPB); n != 0 {
		t.Fatalf("Delete RPB sent %d times while the statement is cached, want 0", n)
	}
	if n := f.count(FunctionOpenDescribe); n != 2 {
		t.Fatalf("OPEN_DESCRIBE sent %d times, want 2", n)
	}
	var handles []uint16
	for _, r := range f.requests() {
		if r.id == FunctionOpenDescribe {
			handles = append(handles, uqHandle(r.packet))
		}
	}
	if handles[0] != handles[1] {
		t.Fatalf("open handles = %v, want the cached statement's handle both times", handles)
	}
}

// TestUQExecPreparedCachedReusesStatement covers ExecContext with arguments on a cached statement.
func TestUQExecPreparedCachedReusesStatement(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam))
	f.push(FunctionExecute, uqReplyWith(uqSQLCA(0, "", 0, 1)), uqReplyWith(uqSQLCA(0, "", 0, 1)))
	c := f.conn(Config{StatementCacheSize: 1})

	for i := 0; i < 2; i++ {
		res, err := c.ExecContext(context.Background(), "INSERT INTO T VALUES (?)", []driver.NamedValue{{Ordinal: 1, Value: int64(7)}})
		if err != nil {
			t.Fatalf("ExecContext() #%d error = %v", i+1, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			t.Fatalf("RowsAffected() = %d, want 1", n)
		}
	}
	if n := f.count(FunctionPrepareDescribe); n != 1 {
		t.Fatalf("PREPARE_DESCRIBE sent %d times, want 1", n)
	}
	if n := f.count(FunctionExecute); n != 2 {
		t.Fatalf("EXECUTE sent %d times, want 2", n)
	}
	if n := f.count(FunctionDeleteRPB); n != 0 {
		t.Fatalf("Delete RPB sent %d times, want 0 while cached", n)
	}
}

// TestUQQueryTransientStatementIsDeletedOnClose checks that the uncached path
// sends PREPARE for every query and deletes each statement when its rows close.
func TestUQQueryTransientStatementIsDeletedOnClose(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam), uqPrepareReply(uqIntParam))
	f.push(FunctionOpenDescribe, uqOpenReply(), uqOpenReply())
	f.push(FunctionFetch,
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil))
	c := f.conn(Config{})

	const query = "SELECT A FROM T WHERE A = ?"
	uqQueryOne(t, c, query)
	uqQueryOne(t, c, query)

	if n := f.count(FunctionPrepareDescribe); n != 2 {
		t.Fatalf("PREPARE_DESCRIBE sent %d times, want 2", n)
	}
	if n := f.count(FunctionDeleteRPB); n != 2 {
		t.Fatalf("Delete RPB sent %d times, want 2", n)
	}
}

// TestUQStatementCacheEvictionClosesVictim fills a one-entry cache. The second
// statement evicts the first, which is idle, so its RPB is deleted on the server
// and the statement is prepared again when the query comes back.
func TestUQStatementCacheEvictionClosesVictim(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam), uqPrepareReply(uqIntParam), uqPrepareReply(uqIntParam))
	f.push(FunctionOpenDescribe, uqOpenReply(), uqOpenReply(), uqOpenReply())
	f.push(FunctionFetch,
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil))
	c := f.conn(Config{StatementCacheSize: 1})

	uqQueryOne(t, c, "SELECT A FROM T WHERE A = ?")
	handleA := uqHandle(uqRequestsWithID(f, FunctionPrepareDescribe)[0].packet)
	if handleA == 0 {
		t.Fatal("first statement has no handle")
	}

	uqQueryOne(t, c, "SELECT B FROM T WHERE B = ?")

	// The eviction closes the victim in the background.
	f.waitCount(t, FunctionDeleteRPB, 1)
	deletes := uqRequestsWithID(f, FunctionDeleteRPB)
	if got := uqHandle(deletes[0].packet); got != handleA {
		t.Fatalf("evicted statement deleted handle %d, want %d", got, handleA)
	}

	uqQueryOne(t, c, "SELECT A FROM T WHERE A = ?")
	if n := f.count(FunctionPrepareDescribe); n != 3 {
		t.Fatalf("PREPARE_DESCRIBE sent %d times, want 3 (evicted statement is prepared again)", n)
	}
}

// TestUQStatementCacheEvictsLeastRecentlyUsed checks that a cache hit moves the
// statement to the back of the eviction order, so the entry evicted is the one
// that was not used most recently.
func TestUQStatementCacheEvictsLeastRecentlyUsed(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe,
		uqPrepareReply(uqIntParam), // A
		uqPrepareReply(uqIntParam), // B
		uqPrepareReply(uqIntParam)) // C
	f.push(FunctionOpenDescribe, uqOpenReply(), uqOpenReply(), uqOpenReply(), uqOpenReply(), uqOpenReply())
	f.push(FunctionFetch,
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil),
		uqFetchReply(2, 701, 0, []int32{42}, nil))
	c := f.conn(Config{StatementCacheSize: 2})

	const qa, qb, qc = "SELECT A FROM T WHERE A = ?", "SELECT B FROM T WHERE B = ?", "SELECT C FROM T WHERE C = ?"
	uqQueryOne(t, c, qa)
	uqQueryOne(t, c, qb)
	handleB := uqHandle(uqRequestsWithID(f, FunctionPrepareDescribe)[1].packet)

	uqQueryOne(t, c, qa) // hit: A becomes the most recently used entry
	uqQueryOne(t, c, qc) // full cache: B is the victim

	f.waitCount(t, FunctionDeleteRPB, 1)
	deletes := uqRequestsWithID(f, FunctionDeleteRPB)
	if got := uqHandle(deletes[0].packet); got != handleB {
		t.Fatalf("evicted handle = %d, want B's handle %d", got, handleB)
	}

	before := f.count(FunctionPrepareDescribe)
	uqQueryOne(t, c, qa)
	if n := f.count(FunctionPrepareDescribe); n != before {
		t.Fatalf("A was prepared again (%d -> %d) although it was still cached", before, n)
	}
}

// TestUQStatementCacheEvictionDefersCloseUntilReleased checks that a victim in
// use is not closed at eviction; it is deleted when its rows close.
func TestUQStatementCacheEvictionDefersCloseUntilReleased(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam), uqPrepareReply(uqIntParam))
	f.push(FunctionOpenDescribe, uqOpenReply(), uqOpenReply())
	f.push(FunctionFetch, uqFetchReply(2, 701, 0, []int32{42}, nil))
	c := f.conn(Config{StatementCacheSize: 1})

	rowsA, err := c.QueryContext(context.Background(), "SELECT A FROM T WHERE A = ?", []driver.NamedValue{{Ordinal: 1, Value: int64(42)}})
	if err != nil {
		t.Fatalf("QueryContext(A) error = %v", err)
	}
	handleA := uqHandle(uqRequestsWithID(f, FunctionPrepareDescribe)[0].packet)

	uqQueryOne(t, c, "SELECT B FROM T WHERE B = ?")
	if n := f.count(FunctionDeleteRPB); n != 0 {
		t.Fatalf("victim in use was deleted at eviction (%d deletes)", n)
	}

	if err := rowsA.Close(); err != nil {
		t.Fatalf("rows A Close() error = %v", err)
	}
	deletes := uqRequestsWithID(f, FunctionDeleteRPB)
	if len(deletes) != 1 {
		t.Fatalf("Delete RPB sent %d times after release, want 1", len(deletes))
	}
	if got := uqHandle(deletes[0].packet); got != handleA {
		t.Fatalf("released victim deleted handle %d, want %d", got, handleA)
	}
}

func uqRequestsWithID(f *uqFake, id uint16) []uqRequest {
	var out []uqRequest
	for _, r := range f.requests() {
		if r.id == id {
			out = append(out, r)
		}
	}
	return out
}

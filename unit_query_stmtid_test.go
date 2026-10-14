package i400

import (
	"context"
	"errors"
	"testing"
)

// TestUQStatementIDsStayLowAcrossQueries runs many uncached queries in turn.
// Each statement's RPB is deleted when its rows close, so every statement gets
// id 1 back and the id set is empty at the end.
func TestUQStatementIDsStayLowAcrossQueries(t *testing.T) {
	const queries = 25
	f := newUQFake(t)
	for i := 0; i < queries; i++ {
		f.push(FunctionOpenDescribe, uqOpenReply())
		f.push(FunctionFetch, uqFetchReply(2, 701, 0, []int32{int32(i)}, nil))
	}
	c := f.conn(Config{})

	for i := 0; i < queries; i++ {
		rows, err := c.QueryContext(context.Background(), "SELECT A FROM T", nil)
		if err != nil {
			t.Fatalf("query %d: QueryContext() error = %v", i, err)
		}
		values := uqDrain(t, rows)
		if len(values) != 1 || uqIntValue(t, values[0]) != int64(i) {
			t.Fatalf("query %d: rows = %v", i, values)
		}
	}

	for _, r := range uqRequestsWithID(f, FunctionCreateRPB) {
		if h := uqHandle(r.packet); h != 1 {
			t.Fatalf("Create RPB used handle %d, want 1", h)
		}
	}
	for _, r := range uqRequestsWithID(f, FunctionOpenDescribe) {
		if h := uqHandle(r.packet); h != 1 {
			t.Fatalf("open used handle %d, want 1", h)
		}
	}
	if n := f.count(FunctionDeleteRPB); n != queries {
		t.Fatalf("Delete RPB sent %d times, want %d", n, queries)
	}
	if len(c.usedStatementHandles) != 0 {
		t.Fatalf("statement ids still in use after all rows closed: %v", c.usedStatementHandles)
	}
}

// TestUQDiscardStatementAfterFailedPrepare checks that a prepare the server
// rejects deletes the statement's RPB and frees its id, on both the Prepare
// path and the QueryContext path.
func TestUQDiscardStatementAfterFailedPrepare(t *testing.T) {
	prepareEntries := map[string]func(c *Conn) error{
		"Prepare": func(c *Conn) error {
			_, err := c.Prepare("SELECT BAD FROM T")
			return err
		},
		"QueryContext": func(c *Conn) error {
			_, err := c.QueryContext(context.Background(), "SELECT BAD FROM T", nil)
			return err
		},
	}
	for name, run := range prepareEntries {
		t.Run(name, func(t *testing.T) {
			f := newUQFake(t)
			f.push(FunctionPrepareDescribe, uqSQLError(-204, "42704"))
			c := f.conn(Config{})

			err := run(c)
			var sqlErr *SQLError
			if !errors.As(err, &sqlErr) || sqlErr.Code != -204 {
				t.Fatalf("error = %v, want *SQLError -204", err)
			}
			uqCheckIDs(t, f, FunctionCreateRPB, FunctionPrepareDescribe, functionSendResultsSet, FunctionDeleteRPB)
			if h := uqHandle(f.requests()[len(f.requests())-1].packet); h != 1 {
				t.Fatalf("discarded RPB handle = %d, want 1", h)
			}
			if len(c.usedStatementHandles) != 0 {
				t.Fatalf("statement id not released after discard: %v", c.usedStatementHandles)
			}

			// The freed id is the one the next statement gets.
			uqPushSingleRowQuery(f)
			uqQueryOne(t, c, "SELECT A FROM T WHERE A = ?")
			prepares := uqRequestsWithID(f, FunctionPrepareDescribe)
			if h := uqHandle(prepares[len(prepares)-1].packet); h != 1 {
				t.Fatalf("statement after discard got handle %d, want 1", h)
			}
		})
	}
}

// TestUQStatementIDsExhaustedFailsQuery checks that a query fails with
// errMaxStatementsExceeded, before any request, when no statement id is free.
func TestUQStatementIDsExhaustedFailsQuery(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})
	c.usedStatementHandles = make(map[uint16]struct{}, maxStatements)
	for id := uint16(1); id < maxStatements; id++ {
		c.usedStatementHandles[id] = struct{}{}
	}

	_, err := c.QueryContext(context.Background(), "SELECT A FROM T", nil)
	if !errors.Is(err, errMaxStatementsExceeded) {
		t.Fatalf("QueryContext() error = %v, want errMaxStatementsExceeded", err)
	}
	if ids := f.ids(); len(ids) != 0 {
		t.Fatalf("requests sent = %x, want none", ids)
	}
}

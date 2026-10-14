package i400

import (
	"context"
	"database/sql/driver"
	"io"
	"testing"
)

// uqRowsOver builds a one-column INTEGER result over a fetch cursor on the
// fake connection. The statement is not owned, so closing the rows does not
// delete its RPB.
func uqRowsOver(c *Conn) *queryRows {
	return &queryRows{
		conn:            c,
		ctx:             context.Background(),
		meta:            &resultSetMeta{columns: []columnMeta{{Name: "COL1", Type: db2TypeInteger, Length: 4}}, RowSize: 4},
		statementHandle: 1,
		statementName:   "STMT00000001",
		cursorName:      "CRSR00000001",
		fetchBufferSize: 256 * 1024,
		resultSetCount:  1,
		resultSetIndex:  1,
	}
}

// uqNext reads one row and returns its value, or io.EOF.
func uqNext(t *testing.T, rows *queryRows) (driver.Value, error) {
	t.Helper()
	dest := make([]driver.Value, 1)
	err := rows.Next(dest)
	return dest[0], err
}

// TestUQRowsNextAcrossFetchBlocks reads two data blocks and then end of data
// (class 1 / code 100). Each block is one fetch, and end of data closes the cursor.
func TestUQRowsNextAcrossFetchBlocks(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionFetch,
		uqFetchReply(0, 0, 0, []int32{1, 2}, nil),
		uqFetchReply(0, 0, 0, []int32{3}, nil),
		uqEndOfData())
	rows := uqRowsOver(f.conn(Config{}))

	var got []int64
	for {
		v, err := uqNext(t, rows)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		got = append(got, uqIntValue(t, v))
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("values = %v, want [1 2 3]", got)
	}
	if _, err := uqNext(t, rows); err != io.EOF {
		t.Fatalf("Next() after end = %v, want io.EOF", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	uqCheckIDs(t, f, FunctionFetch, FunctionFetch, FunctionFetch, FunctionClose)
}

// TestUQRowsEndOfDataCursorClosedByServer checks class 2 / code 700: the server
// already closed the forward-only cursor, so no further fetch and no close are sent.
func TestUQRowsEndOfDataCursorClosedByServer(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionFetch, uqFetchReply(2, 700, 0, []int32{5}, nil))
	rows := uqRowsOver(f.conn(Config{}))

	if v, err := uqNext(t, rows); err != nil || uqIntValue(t, v) != 5 {
		t.Fatalf("Next() = %v, %v, want 5", v, err)
	}
	if _, err := uqNext(t, rows); err != io.EOF {
		t.Fatalf("Next() at end = %v, want io.EOF", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	uqCheckIDs(t, f, FunctionFetch)
}

// TestUQRowsEndOfDataLastBlock checks class 2 / code 701: the last block is
// returned with its rows. Nothing more is fetched, and the cursor is closed
// with the no-reply close request.
func TestUQRowsEndOfDataLastBlock(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionFetch, uqFetchReply(2, 701, 0, []int32{5}, nil))
	rows := uqRowsOver(f.conn(Config{}))

	if v, err := uqNext(t, rows); err != nil || uqIntValue(t, v) != 5 {
		t.Fatalf("Next() = %v, %v, want 5", v, err)
	}
	if _, err := uqNext(t, rows); err != io.EOF {
		t.Fatalf("Next() at end = %v, want io.EOF", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	f.waitCount(t, FunctionClose, 1)
	uqCheckIDs(t, f, FunctionFetch, FunctionClose)
	if last := f.requests()[1]; uqExpectsReply(last.packet) {
		t.Fatal("close after the last block asked for a reply; JTOpen sends it without one")
	}
}

// TestUQRowsNextNullIndicators checks that a row whose indicator is 0xFFFF
// decodes as NULL and the rows around it keep their values.
func TestUQRowsNextNullIndicators(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionFetch,
		uqFetchReply(0, 0, 2, []int32{7, 0, 9}, []bool{false, true, false}),
		uqEndOfData())
	rows := uqRowsOver(f.conn(Config{}))

	v1, err := uqNext(t, rows)
	if err != nil || uqIntValue(t, v1) != 7 {
		t.Fatalf("row 1 = %v, %v, want 7", v1, err)
	}
	v2, err := uqNext(t, rows)
	if err != nil || v2 != nil {
		t.Fatalf("row 2 = %v, %v, want NULL", v2, err)
	}
	v3, err := uqNext(t, rows)
	if err != nil || uqIntValue(t, v3) != 9 {
		t.Fatalf("row 3 = %v, %v, want 9", v3, err)
	}
	if _, err := uqNext(t, rows); err != io.EOF {
		t.Fatalf("Next() at end = %v, want io.EOF", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestUQRowsCloseMidIteration closes a result after one row of a three-row
// block. The open cursor is closed on the server with a reply, nothing more is
// fetched, and further Next calls return io.EOF.
func TestUQRowsCloseMidIteration(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionFetch, uqFetchReply(0, 0, 0, []int32{1, 2, 3}, nil))
	rows := uqRowsOver(f.conn(Config{}))

	if v, err := uqNext(t, rows); err != nil || uqIntValue(t, v) != 1 {
		t.Fatalf("Next() = %v, %v, want 1", v, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := uqNext(t, rows); err != io.EOF {
		t.Fatalf("Next() after Close = %v, want io.EOF", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	uqCheckIDs(t, f, FunctionFetch, FunctionClose)
	if last := f.requests()[1]; !uqExpectsReply(last.packet) {
		t.Fatal("close of an open cursor did not ask for a reply")
	}
}

package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// TestUQLegacyPrepareExecAndClose drives the no-context API: Conn.Prepare,
// Stmt.NumInput, Stmt.Exec and Stmt.Close.
func TestUQLegacyPrepareExecAndClose(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam))
	f.push(FunctionExecute, uqReplyWith(uqSQLCA(0, "", 0, 3)))
	c := f.conn(Config{})

	stmt, err := c.Prepare("INSERT INTO T VALUES (?)")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if n := stmt.NumInput(); n != 1 {
		t.Fatalf("NumInput() = %d, want 1", n)
	}
	res, err := stmt.Exec([]driver.Value{int64(7)})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n, _ := res.RowsAffected(); n != 3 {
		t.Fatalf("RowsAffected() = %d, want 3", n)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	uqCheckIDs(t, f,
		FunctionCreateRPB,
		FunctionPrepareDescribe,
		FunctionChangeDescriptor,
		FunctionExecute,
		FunctionDeleteDescriptor,
		FunctionDeleteRPB)
}

// TestUQLegacyStmtQueryWithAndWithoutArgs drives Stmt.Query on a prepared SELECT.
func TestUQLegacyStmtQueryWithAndWithoutArgs(t *testing.T) {
	t.Run("with args", func(t *testing.T) {
		f := newUQFake(t)
		uqPushSingleRowQuery(f)
		c := f.conn(Config{})

		stmt, err := c.Prepare("SELECT A FROM T WHERE A = ?")
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		rows, err := stmt.Query([]driver.Value{int64(42)})
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		values := uqDrain(t, rows)
		if len(values) != 1 || uqIntValue(t, values[0]) != 42 {
			t.Fatalf("rows = %v, want one row with 42", values)
		}
		if err := stmt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		uqCheckIDs(t, f,
			FunctionCreateRPB,
			FunctionPrepareDescribe,
			FunctionChangeDescriptor,
			FunctionOpenDescribe,
			FunctionDeleteDescriptor,
			FunctionFetch,
			FunctionClose,
			FunctionDeleteRPB)
	})

	t.Run("without args", func(t *testing.T) {
		f := newUQFake(t)
		f.push(FunctionPrepareDescribe, uqPrepareReply())
		f.push(FunctionOpenDescribe, uqOpenReply())
		f.push(FunctionFetch, uqFetchReply(2, 701, 0, []int32{5}, nil))
		c := f.conn(Config{})

		stmt, err := c.Prepare("SELECT A FROM T")
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		rows, err := stmt.Query(nil)
		if err != nil {
			t.Fatalf("Query() error = %v", err)
		}
		if values := uqDrain(t, rows); len(values) != 1 || uqIntValue(t, values[0]) != 5 {
			t.Fatalf("rows = %v, want one row with 5", values)
		}
		if err := stmt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		uqCheckIDs(t, f,
			FunctionCreateRPB,
			FunctionPrepareDescribe,
			FunctionOpenDescribe,
			FunctionFetch,
			FunctionClose,
			FunctionDeleteRPB)
	})
}

// TestUQLegacyBeginCommit drives Conn.Begin and Tx.Commit with the default isolation.
func TestUQLegacyBeginCommit(t *testing.T) {
	f := newUQFake(t)
	c := f.conn(Config{})

	tx, err := c.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if c.autoCommit {
		t.Fatal("Begin() left autocommit on")
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if !c.autoCommit {
		t.Fatal("Commit() did not restore autocommit")
	}
	uqCheckIDs(t, f, FunctionSetAttributes, FunctionCommit, FunctionSetAttributes)
}

// TestUQCommitSQLErrorReturnsSQLError checks that a commit the server rejects
// comes back as *SQLError, and that the transaction is finished.
func TestUQCommitSQLErrorReturnsSQLError(t *testing.T) {
	f := newUQFake(t)
	f.push(FunctionCommit, uqSQLError(-911, "40501"))
	c := f.conn(Config{})

	tx, err := c.BeginTx(context.Background(), driver.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	err = tx.Commit()
	var sqlErr *SQLError
	if !errors.As(err, &sqlErr) {
		t.Fatalf("Commit() error = %T (%v), want *SQLError", err, err)
	}
	if sqlErr.Code != -911 || sqlErr.State != "40501" {
		t.Fatalf("SQLError = code %d state %q, want -911 / 40501", sqlErr.Code, sqlErr.State)
	}
	if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("Rollback() after failed Commit = %v, want sql.ErrTxDone", err)
	}
	if !c.IsValid() {
		t.Fatal("a rejected commit must not invalidate the connection")
	}
	uqCheckIDs(t, f, FunctionSetAttributes, FunctionCommit, functionSendResultsSet)
}

// TestUQBeginTxIsolationLevels checks each supported isolation level: the SET
// TRANSACTION statement, the commitment control level sent with autocommit off,
// and the commit that follows.
func TestUQBeginTxIsolationLevels(t *testing.T) {
	tests := []struct {
		name  string
		level sql.IsolationLevel
		mode  string
		ctl   uint16
	}{
		{name: "read uncommitted", level: sql.LevelReadUncommitted, mode: "CHG", ctl: 2},
		{name: "read committed", level: sql.LevelReadCommitted, mode: "CS", ctl: 1},
		{name: "repeatable read", level: sql.LevelRepeatableRead, mode: "ALL", ctl: 3},
		{name: "serializable", level: sql.LevelSerializable, mode: "RR", ctl: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newUQFake(t)
			c := f.conn(Config{})

			tx, err := c.BeginTx(context.Background(), driver.TxOptions{Isolation: driver.IsolationLevel(tt.level)})
			if err != nil {
				t.Fatalf("BeginTx() error = %v", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("Commit() error = %v", err)
			}
			uqCheckIDs(t, f, FunctionExecuteImmediate, FunctionSetAttributes, FunctionCommit, FunctionSetAttributes)

			reqs := f.requests()
			stmt, found, err := readTestLLCPValue(reqs[0].packet[40:], 0x3831)
			if err != nil || !found {
				t.Fatalf("SQL text attribute: found=%v err=%v", found, err)
			}
			text, err := DecodeUTF16BE(stmt[6:])
			if err != nil {
				t.Fatalf("decode SQL text: %v", err)
			}
			if want := "SET TRANSACTION ISOLATION LEVEL " + tt.mode; text != want {
				t.Fatalf("SQL text = %q, want %q", text, want)
			}
			if got := requestBodyShortValue(t, reqs[1].packet, CodePointCommitmentControlLevel); got != tt.ctl {
				t.Fatalf("commitment control with autocommit off = %d, want %d", got, tt.ctl)
			}
		})
	}
}

// TestUQBeginTxUnsupportedIsolationSendsNothing checks that an isolation level
// the driver cannot express is refused before the server is contacted.
func TestUQBeginTxUnsupportedIsolationSendsNothing(t *testing.T) {
	levels := []sql.IsolationLevel{sql.LevelWriteCommitted, sql.LevelSnapshot, sql.LevelLinearizable}
	for _, level := range levels {
		f := newUQFake(t)
		c := f.conn(Config{})
		_, err := c.BeginTx(context.Background(), driver.TxOptions{Isolation: driver.IsolationLevel(level)})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("level %d: BeginTx() error = %v, want ErrUnsupported", level, err)
		}
		if ids := f.ids(); len(ids) != 0 {
			t.Fatalf("level %d: requests sent = %x, want none", level, ids)
		}
		if !c.autoCommit {
			t.Fatalf("level %d: autocommit changed", level)
		}
	}
}

// TestUQTransactionCommitmentControlMapping checks the isolation to commitment
// control mapping, including the fallback for levels without a mapping.
func TestUQTransactionCommitmentControlMapping(t *testing.T) {
	tests := []struct {
		level sql.IsolationLevel
		want  int
	}{
		{sql.LevelReadUncommitted, 2},
		{sql.LevelReadCommitted, 1},
		{sql.LevelRepeatableRead, 3},
		{sql.LevelSerializable, 4},
		{sql.LevelDefault, defaultCommitmentControl},
		{sql.LevelSnapshot, defaultCommitmentControl},
		{sql.LevelWriteCommitted, defaultCommitmentControl},
		{sql.LevelLinearizable, defaultCommitmentControl},
	}
	for _, tt := range tests {
		if got := transactionCommitmentControl(driver.IsolationLevel(tt.level)); got != tt.want {
			t.Errorf("transactionCommitmentControl(%d) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

// TestUQStmtExecOnInvalidConnKeepsInFlightCount checks that Stmt.ExecContext
// releases its in-use count once when the connection is gone. A second call
// still in flight must keep the statement open for Close.
func TestUQStmtExecOnInvalidConnKeepsInFlightCount(t *testing.T) {
	c := &Conn{}
	stmt := newStmt(c, "INSERT INTO T VALUES (1)", "", 0, 0, nil, nil)
	stmt.active = 1 // one call already in flight

	_, err := stmt.ExecContext(context.Background(), nil)
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("ExecContext() error = %v, want driver.ErrBadConn", err)
	}
	if stmt.active != 1 {
		t.Fatalf("active = %d after a failed exec, want 1 (the call still in flight)", stmt.active)
	}
}

// uqConnector hands database/sql the single fake connection.
type uqConnector struct{ conn *Conn }

func (c uqConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c uqConnector) Driver() driver.Driver                        { return driverInstance }

// TestUQDatabaseSQLQueryWithArgs runs a parameterized query through database/sql.
func TestUQDatabaseSQLQueryWithArgs(t *testing.T) {
	f := newUQFake(t)
	uqPushSingleRowQuery(f)
	db := sql.OpenDB(uqConnector{conn: f.conn(Config{})})
	defer db.Close()

	rows, err := db.QueryContext(context.Background(), "SELECT A FROM T WHERE A = ?", int64(42))
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	var got []int64
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		got = append(got, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("rows.Close() error = %v", err)
	}
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("values = %v, want [42]", got)
	}
	uqCheckIDs(t, f, uqPositionalSequence...)
}

// TestUQDatabaseSQLBeginTxIsolationCommit runs a transaction with an isolation
// level through database/sql and commits it.
func TestUQDatabaseSQLBeginTxIsolationCommit(t *testing.T) {
	f := newUQFake(t)
	db := sql.OpenDB(uqConnector{conn: f.conn(Config{})})
	defer db.Close()

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	uqCheckIDs(t, f, FunctionExecuteImmediate, FunctionSetAttributes, FunctionCommit, FunctionSetAttributes)
}

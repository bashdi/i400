package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

// uxBatchRows builds n single-column rows holding 0..n-1.
func uxBatchRows(n int) [][]driver.Value {
	rows := make([][]driver.Value, n)
	for i := range rows {
		rows[i] = []driver.Value{int64(i)}
	}
	return rows
}

func uxBlockConfig(c *Conn) {
	c.cfg.UseBlockUpdate = true
}

func TestUxConnExecBatchBlockPathNamedPlaceholders(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0), uxIntField(0xF0)}, execute: uxEchoRowsExecute}
	rows := [][]driver.Value{{int64(1), int64(10)}, {int64(2), nil}, {int64(3), int64(30)}}
	var total int64
	var err error
	s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
		total, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(:id, :name)", rows)
	})
	if err != nil {
		t.Fatalf("ExecBatch() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, []uint32{3}) {
		t.Fatalf("execute row counts = %v, want [3] (one block)", got)
	}
	prepare := uxRequestsWith(s, FunctionPrepareDescribe)
	if len(prepare) != 1 {
		t.Fatalf("prepare requests = %d, want 1", len(prepare))
	}
	if !uxContainsText(prepare[0].packet, "VALUES(?, ?)") || uxContainsText(prepare[0].packet, ":id") {
		t.Fatal("prepared text was not rewritten from named to positional markers")
	}
	if s.count(FunctionChangeDescriptor) != 1 || s.count(FunctionDeleteDescriptor) != 1 {
		t.Fatalf("descriptor requests change=%d delete=%d, want 1 and 1",
			s.count(FunctionChangeDescriptor), s.count(FunctionDeleteDescriptor))
	}
	if s.count(FunctionDeleteRPB) != 1 {
		t.Fatalf("statement close requests = %d, want 1", s.count(FunctionDeleteRPB))
	}
}

func TestUxConnExecBatchRowByRowPath(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}, execute: uxEchoRowsExecute}
	var total int64
	var err error
	s := uxRunSession(t, nil, db.handle, func(c *Conn, _ *uxSession) {
		total, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", uxBatchRows(3))
	})
	if err != nil {
		t.Fatalf("ExecBatch() error = %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, []uint32{1, 1, 1}) {
		t.Fatalf("execute row counts = %v, want [1 1 1]", got)
	}
	if s.count(FunctionChangeDescriptor) != 3 {
		t.Fatalf("change descriptor requests = %d, want one per row", s.count(FunctionChangeDescriptor))
	}
}

func TestUxConnExecBatchSecondBlockErrorReturnsPartialCount(t *testing.T) {
	const n = maxBlockRows + 5
	calls := 0
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}}
	db.execute = func(s *uxSession, req uxRequest) []byte {
		calls++
		if calls == 1 {
			return uxReply(uxSQLCA(0, maxBlockRows, 0))
		}
		return uxReply(uxSQLCA(-803, 0, 0))
	}
	var total int64
	var err error
	s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
		total, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", uxBatchRows(n))
	})
	if err == nil {
		t.Fatal("ExecBatch() error = nil, want SQL error from second block")
	}
	var sqlErr *SQLError
	if !errors.As(err, &sqlErr) || sqlErr.Code != -803 {
		t.Fatalf("ExecBatch() error = %v, want *SQLError with code -803", err)
	}
	if total != maxBlockRows {
		t.Fatalf("partial total = %d, want %d (rows of the first block)", total, maxBlockRows)
	}
	if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, []uint32{maxBlockRows, 5}) {
		t.Fatalf("execute row counts = %v, want [%d 5]", got, maxBlockRows)
	}
}

func TestUxBatchRejectsSQLOut(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}}
	var dest int64
	rows := [][]driver.Value{{int64(1)}, {sql.Out{Dest: &dest}}}

	t.Run("Conn", func(t *testing.T) {
		var err error
		s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
			_, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", rows)
		})
		if err == nil || !strings.Contains(err.Error(), "sql.Out is not supported") {
			t.Fatalf("ExecBatch() error = %v, want sql.Out rejection", err)
		}
		if n := s.count(FunctionExecute); n != 0 {
			t.Fatalf("execute requests = %d, want none", n)
		}
	})

	t.Run("Stmt", func(t *testing.T) {
		var err error
		s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
			stmt, perr := c.PrepareContext(context.Background(), "INSERT INTO T VALUES(?)")
			if perr != nil {
				t.Fatalf("PrepareContext() error = %v", perr)
			}
			_, err = stmt.(*Stmt).ExecBatch(context.Background(), rows)
		})
		if err == nil || !strings.Contains(err.Error(), "sql.Out is not supported") {
			t.Fatalf("Stmt.ExecBatch() error = %v, want sql.Out rejection", err)
		}
		if n := s.count(FunctionExecute); n != 0 {
			t.Fatalf("execute requests = %d, want none", n)
		}
	})
}

func TestUxBatchEmptyRowsReturnsZeroWithoutPrepare(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}}

	t.Run("Conn", func(t *testing.T) {
		var total int64
		var err error
		s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
			total, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", nil)
			if err != nil {
				return
			}
			var n int64
			n, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", [][]driver.Value{})
			total += n
		})
		if err != nil || total != 0 {
			t.Fatalf("ExecBatch(empty) = %d, %v; want 0, nil", total, err)
		}
		if len(s.snapshot()) != 0 {
			t.Fatalf("requests sent for empty batch: %d, want none", len(s.snapshot()))
		}
	})

	t.Run("Stmt", func(t *testing.T) {
		var total int64
		var err error
		var before, after int
		uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, session *uxSession) {
			stmt, perr := c.PrepareContext(context.Background(), "INSERT INTO T VALUES(?)")
			if perr != nil {
				t.Fatalf("PrepareContext() error = %v", perr)
			}
			before = len(session.snapshot())
			total, err = stmt.(*Stmt).ExecBatch(context.Background(), nil)
			after = len(session.snapshot())
		})
		if err != nil || total != 0 {
			t.Fatalf("Stmt.ExecBatch(empty) = %d, %v; want 0, nil", total, err)
		}
		if before != after {
			t.Fatalf("Stmt.ExecBatch(empty) sent %d requests, want none", after-before)
		}
	})
}

func TestUxStmtExecBatchBlockAndRowPaths(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}, execute: uxEchoRowsExecute}

	t.Run("block", func(t *testing.T) {
		var total int64
		var err error
		s := uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
			stmt, perr := c.PrepareContext(context.Background(), "INSERT INTO T VALUES(?)")
			if perr != nil {
				t.Fatalf("PrepareContext() error = %v", perr)
			}
			total, err = stmt.(*Stmt).ExecBatch(context.Background(), uxBatchRows(4))
			if cerr := stmt.Close(); cerr != nil {
				t.Errorf("Stmt.Close() error = %v", cerr)
			}
		})
		if err != nil || total != 4 {
			t.Fatalf("Stmt.ExecBatch() = %d, %v; want 4, nil", total, err)
		}
		if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, []uint32{4}) {
			t.Fatalf("execute row counts = %v, want [4]", got)
		}
	})

	t.Run("row by row", func(t *testing.T) {
		var total int64
		var err error
		s := uxRunSession(t, nil, db.handle, func(c *Conn, _ *uxSession) {
			stmt, perr := c.PrepareContext(context.Background(), "INSERT INTO T VALUES(?)")
			if perr != nil {
				t.Fatalf("PrepareContext() error = %v", perr)
			}
			total, err = stmt.(*Stmt).ExecBatch(context.Background(), uxBatchRows(2))
		})
		if err != nil || total != 2 {
			t.Fatalf("Stmt.ExecBatch() = %d, %v; want 2, nil", total, err)
		}
		if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, []uint32{1, 1}) {
			t.Fatalf("execute row counts = %v, want [1 1]", got)
		}
	})
}

func TestUxStmtExecBatchGuards(t *testing.T) {
	var nilStmt *Stmt
	if _, err := nilStmt.ExecBatch(context.Background(), uxBatchRows(1)); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("nil Stmt.ExecBatch() error = %v, want ErrConnectionClosed", err)
	}

	client, server := net.Pipe()
	defer server.Close()
	defer client.Close()
	conn := &Conn{netConn: client, autoCommit: true}
	unprepared := &Stmt{conn: conn}
	if _, err := unprepared.ExecBatch(context.Background(), uxBatchRows(1)); !errors.Is(err, errPreparedStatementBindingUnsupported) {
		t.Fatalf("unprepared Stmt.ExecBatch() error = %v, want binding unsupported", err)
	}

	noConn := &Stmt{statementName: "S1", parameterMarkerFormat: &parameterMarkerFormat{}}
	if _, err := noConn.ExecBatch(context.Background(), uxBatchRows(1)); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("Stmt.ExecBatch() without conn error = %v, want ErrBadConn", err)
	}
}

func TestUxConnExecBatchGuards(t *testing.T) {
	var closed Conn
	if _, err := closed.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", uxBatchRows(1)); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("ExecBatch() on invalid conn error = %v, want ErrBadConn", err)
	}

	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}}
	var err error
	s := uxRunSession(t, nil, db.handle, func(c *Conn, _ *uxSession) {
		_, err = c.ExecBatch(context.Background(), "SELECT * FROM T", uxBatchRows(1))
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("ExecBatch(SELECT) error = %v, want ErrUnsupported", err)
	}
	if n := s.count(FunctionExecute); n != 0 {
		t.Fatalf("execute requests for SELECT = %d, want none", n)
	}
}

func TestUxConnExecBatchUpdateBlocksOnlyOnV7R1(t *testing.T) {
	fields := []parameterMarkerField{uxIntField(0xF0), uxIntField(0xF0)}
	rows := [][]driver.Value{{int64(1), int64(1)}, {int64(2), int64(2)}}
	cases := []struct {
		name string
		vrm  int
		want []uint32
	}{
		{name: "V7R1 blocks", vrm: blockUpdateMinVRM, want: []uint32{2}},
		{name: "V6R1 row by row", vrm: 0x060100, want: []uint32{1, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &uxDB2{fields: fields, execute: uxEchoRowsExecute}
			var total int64
			var err error
			s := uxRunSession(t, func(c *Conn) {
				c.cfg.UseBlockUpdate = true
				c.systemInfo = &SystemInfo{ServerVersion: tc.vrm}
			}, db.handle, func(c *Conn, _ *uxSession) {
				total, err = c.ExecBatch(context.Background(), "UPDATE T SET A = ? WHERE B = ?", rows)
			})
			if err != nil || total != 2 {
				t.Fatalf("ExecBatch() = %d, %v; want 2, nil", total, err)
			}
			if got := uxExecuteRowCounts(s); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("execute row counts = %v, want %v", got, tc.want)
			}
		})
	}
}

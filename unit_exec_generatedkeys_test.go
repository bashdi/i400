package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

const uxWrappedMarker = "SELECT *SQLGENCOLUMNS FROM NEW TABLE("

func uxReturnKeysConfig(c *Conn) {
	c.cfg.ReturnGeneratedKeys = true
}

// uxKeyRow is one 4-byte INTEGER generated key.
func uxKeyRow(key uint32) []byte {
	raw := make([]byte, 4)
	binary.BigEndian.PutUint32(raw, key)
	return raw
}

// uxWrappedPrepareCount counts prepare requests whose text is the wrapped INSERT.
func uxWrappedPrepareCount(s *uxSession) int {
	n := 0
	for _, req := range uxRequestsWith(s, FunctionPrepareDescribe) {
		if uxContainsText(req.packet, uxWrappedMarker) {
			n++
		}
	}
	return n
}

func TestUxConnExecContextWrapsInsertForGeneratedKeys(t *testing.T) {
	const insert = "INSERT INTO T VALUES(1)"
	db := &uxDB2{rows: [][]byte{uxKeyRow(7), uxKeyRow(9)}, nulls: []bool{false, false}, endOfData: true}
	var res driver.Result
	var err error
	s := uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, _ *uxSession) {
		res, err = c.ExecContext(context.Background(), insert, nil)
	})
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	prepare := uxRequestsWith(s, FunctionPrepareDescribe)
	if len(prepare) != 1 {
		t.Fatalf("prepare requests = %d, want 1", len(prepare))
	}
	if !uxContainsText(prepare[0].packet, uxWrappedMarker) || !uxContainsText(prepare[0].packet, insert) {
		t.Fatal("prepared text is not the wrapped INSERT")
	}
	if n := s.count(FunctionExecuteImmediate); n != 0 {
		t.Fatalf("execute immediate requests = %d, want 0 (the wrapped form is a query)", n)
	}
	if n := s.count(FunctionOpenDescribe); n != 1 {
		t.Fatalf("open requests = %d, want 1", n)
	}
	if id, err := res.LastInsertId(); err != nil || id != 9 {
		t.Fatalf("LastInsertId() = %d, %v; want 9 (last returned key)", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 2 {
		t.Fatalf("RowsAffected() = %d, %v; want 2 (rows returned)", n, err)
	}
}

func TestUxConnExecContextWrappedInsertWithArgs(t *testing.T) {
	db := &uxDB2{fields: []parameterMarkerField{uxIntField(0xF0)}, rows: [][]byte{uxKeyRow(11)}, nulls: []bool{false}, endOfData: true}
	var res driver.Result
	var err error
	s := uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, _ *uxSession) {
		res, err = c.ExecContext(context.Background(), "INSERT INTO T VALUES(?)", []driver.NamedValue{{Ordinal: 1, Value: int64(5)}})
	})
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if uxWrappedPrepareCount(s) != 1 {
		t.Fatal("parameterized INSERT was not wrapped in SELECT *SQLGENCOLUMNS FROM NEW TABLE")
	}
	open := uxRequestsWith(s, FunctionOpenDescribe)
	if len(open) != 1 {
		t.Fatalf("open requests = %d, want 1", len(open))
	}
	params, found, _ := readTestLLCPValue(open[0].packet[40:], CodePointParameterMarkerData)
	if !found || len(params) < 20 || binary.BigEndian.Uint32(params[16:20]) != 5 {
		t.Fatalf("open request parameter block = % x, want value 5", params)
	}
	if id, err := res.LastInsertId(); err != nil || id != 11 {
		t.Fatalf("LastInsertId() = %d, %v; want 11", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("RowsAffected() = %d, %v; want 1", n, err)
	}
}

func TestUxStmtExecContextWrapsInsertForGeneratedKeys(t *testing.T) {
	db := &uxDB2{rows: [][]byte{uxKeyRow(7)}, nulls: []bool{false}, endOfData: true}
	var res driver.Result
	var err error
	var wrappedBeforeExec int
	s := uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, session *uxSession) {
		stmtIface, perr := c.PrepareContext(context.Background(), "INSERT INTO T VALUES(1)")
		if perr != nil {
			t.Fatalf("PrepareContext() error = %v", perr)
		}
		stmt := stmtIface.(*Stmt)
		wrappedBeforeExec = uxWrappedPrepareCount(session)
		res, err = stmt.ExecContext(context.Background(), nil)
		if cerr := stmt.Close(); cerr != nil {
			t.Errorf("Stmt.Close() error = %v", cerr)
		}
	})
	if err != nil {
		t.Fatalf("Stmt.ExecContext() error = %v", err)
	}
	if wrappedBeforeExec != 0 {
		t.Fatalf("wrapped prepares before exec = %d, want 0", wrappedBeforeExec)
	}
	if uxWrappedPrepareCount(s) != 1 {
		t.Fatalf("wrapped prepares = %d, want 1 (the INSERT run by Stmt.ExecContext)", uxWrappedPrepareCount(s))
	}
	if id, err := res.LastInsertId(); err != nil || id != 7 {
		t.Fatalf("LastInsertId() = %d, %v; want 7", id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("RowsAffected() = %d, %v; want 1", n, err)
	}
}

func TestUxNonInsertAndDisabledOptionAreNotWrapped(t *testing.T) {
	t.Run("UPDATE with option on", func(t *testing.T) {
		db := &uxDB2{updateCount: 3}
		var res driver.Result
		var err error
		s := uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, _ *uxSession) {
			res, err = c.ExecContext(context.Background(), "UPDATE T SET A = 1", nil)
		})
		if err != nil {
			t.Fatalf("ExecContext() error = %v", err)
		}
		if n, err := res.RowsAffected(); err != nil || n != 3 {
			t.Fatalf("RowsAffected() = %d, %v; want 3", n, err)
		}
		if _, err := res.LastInsertId(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("LastInsertId() error = %v, want ErrUnsupported (no key)", err)
		}
		if uxWrappedPrepareCount(s) != 0 || s.count(FunctionPrepareDescribe) != 0 {
			t.Fatal("UPDATE was prepared or wrapped; want plain execute immediate")
		}
		if s.count(FunctionExecuteImmediate) != 1 {
			t.Fatalf("execute immediate requests = %d, want 1", s.count(FunctionExecuteImmediate))
		}
	})

	t.Run("INSERT with option off", func(t *testing.T) {
		db := &uxDB2{updateCount: 1}
		var res driver.Result
		var err error
		s := uxRunSession(t, nil, db.handle, func(c *Conn, _ *uxSession) {
			res, err = c.ExecContext(context.Background(), "INSERT INTO T VALUES(1)", nil)
		})
		if err != nil {
			t.Fatalf("ExecContext() error = %v", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			t.Fatalf("RowsAffected() = %d, want 1", n)
		}
		if uxWrappedPrepareCount(s) != 0 || s.count(FunctionExecuteImmediate) != 1 {
			t.Fatal("INSERT was wrapped although returnGeneratedKeys is off")
		}
	})
}

func TestUxGeneratedKeysZeroRowsAndNullKey(t *testing.T) {
	t.Run("no rows", func(t *testing.T) {
		db := &uxDB2{endOfData: true}
		var res driver.Result
		var err error
		uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, _ *uxSession) {
			res, err = c.ExecContext(context.Background(), "INSERT INTO T VALUES(1)", nil)
		})
		if err != nil {
			t.Fatalf("ExecContext() error = %v", err)
		}
		if n, _ := res.RowsAffected(); n != 0 {
			t.Fatalf("RowsAffected() = %d, want 0", n)
		}
		if _, err := res.LastInsertId(); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("LastInsertId() error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("NULL key", func(t *testing.T) {
		db := &uxDB2{rows: [][]byte{uxKeyRow(0)}, nulls: []bool{true}, endOfData: true}
		var err error
		uxRunSession(t, uxReturnKeysConfig, db.handle, func(c *Conn, _ *uxSession) {
			_, err = c.ExecContext(context.Background(), "INSERT INTO T VALUES(1)", nil)
		})
		if err == nil || !strings.Contains(err.Error(), "generated key is NULL") {
			t.Fatalf("ExecContext() error = %v, want NULL generated key error", err)
		}
	})
}

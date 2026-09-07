package i400

import (
	"errors"
	"testing"
)

func TestExecResultGeneratedKeyAndRowsAffected(t *testing.T) {
	result := &execResult{rowsAffected: 3, lastInsertID: 42, hasLastInsert: true}

	if rows, err := result.RowsAffected(); err != nil || rows != 3 {
		t.Fatalf("RowsAffected() = (%d, %v), want (3, nil)", rows, err)
	}
	if id, err := result.LastInsertId(); err != nil || id != 42 {
		t.Fatalf("LastInsertId() = (%d, %v), want (42, nil)", id, err)
	}
}

func TestExecResultWithoutGeneratedKey(t *testing.T) {
	result := &execResult{rowsAffected: 1}

	if id, err := result.LastInsertId(); id != 0 || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("LastInsertId() = (%d, %v), want (0, ErrUnsupported)", id, err)
	}
}

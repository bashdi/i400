package i400

import (
	"errors"
	"testing"
)

func TestNextStatementNameReusesLowestFreeID(t *testing.T) {
	c := &Conn{}
	_, h1, _ := c.nextStatementName()
	_, h2, _ := c.nextStatementName()
	_, h3, _ := c.nextStatementName()
	if h1 != 1 || h2 != 2 || h3 != 3 {
		t.Fatalf("handles = %d,%d,%d, want 1,2,3", h1, h2, h3)
	}
	c.releaseStatementHandle(h2)
	name, h, err := c.nextStatementName()
	if err != nil || h != 2 || name != "STMT00000002" {
		t.Fatalf("after release got %q/%d/%v, want STMT00000002/2", name, h, err)
	}
}

func TestNextStatementNameNeverWrapsToZero(t *testing.T) {
	c := &Conn{}
	for i := 1; i < maxStatements; i++ {
		_, h, err := c.nextStatementName()
		if err != nil || h == 0 {
			t.Fatalf("allocation %d: handle %d, err %v", i, h, err)
		}
		c.releaseStatementHandle(h)
	}
	for i := 1; i < maxStatements; i++ {
		if _, _, err := c.nextStatementName(); err != nil {
			t.Fatalf("allocation %d: %v", i, err)
		}
	}
	if _, _, err := c.nextStatementName(); !errors.Is(err, errMaxStatementsExceeded) {
		t.Fatalf("error = %v, want errMaxStatementsExceeded", err)
	}
}

func TestNextDescriptorHandleSkipsZero(t *testing.T) {
	c := &Conn{descriptorSeq: 0xFFFF}
	if h := c.nextDescriptorHandle(); h != 1 {
		t.Fatalf("handle after 0xFFFF = %d, want 1", h)
	}
}

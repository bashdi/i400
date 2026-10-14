package i400

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUxRecordDeleteDescriptorFailureRecordsWarnings(t *testing.T) {
	t.Run("success records nothing", func(t *testing.T) {
		c := &Conn{}
		c.recordDeleteDescriptorFailure(nil, replyEnvelope{})
		if w := c.Warnings(); w != nil {
			t.Fatalf("Warnings() = %+v, want nil", w)
		}
	})

	t.Run("warning-only reply records nothing", func(t *testing.T) {
		c := &Conn{}
		c.recordDeleteDescriptorFailure(nil, replyEnvelope{RCClass: 1, RCCode: 5})
		if w := c.Warnings(); w != nil {
			t.Fatalf("Warnings() = %+v, want nil for a non-error reply", w)
		}
	})

	t.Run("transport error", func(t *testing.T) {
		c := &Conn{}
		c.recordDeleteDescriptorFailure(errors.New("socket closed"), replyEnvelope{})
		w := c.Warnings()
		if w == nil {
			t.Fatal("Warnings() = nil, want a delete descriptor warning")
		}
		if !strings.Contains(w.Message, "delete descriptor request failed") || !strings.Contains(w.Message, "socket closed") {
			t.Fatalf("warning message = %q", w.Message)
		}
	})

	t.Run("error reply keeps the return code", func(t *testing.T) {
		c := &Conn{}
		c.recordDeleteDescriptorFailure(nil, replyEnvelope{RCClass: 3, RCCode: -1})
		w := c.Warnings()
		if w == nil {
			t.Fatal("Warnings() = nil, want a delete descriptor warning")
		}
		if w.Code != -1 {
			t.Fatalf("warning code = %d, want -1", w.Code)
		}
		if !strings.Contains(w.Message, "class=3") {
			t.Fatalf("warning message = %q, want the class", w.Message)
		}
	})

	t.Run("warnings chain in order", func(t *testing.T) {
		c := &Conn{}
		c.recordDeleteDescriptorFailure(errors.New("first"), replyEnvelope{})
		c.recordDeleteDescriptorFailure(nil, replyEnvelope{RCClass: 3, RCCode: -2})
		first := c.Warnings()
		if first == nil || first.Next == nil {
			t.Fatalf("Warnings() chain = %+v, want two warnings", first)
		}
		if !strings.Contains(first.Message, "first") || first.Next.Code != -2 {
			t.Fatalf("chain = %q then code %d", first.Message, first.Next.Code)
		}
		c.ClearWarnings()
		if c.Warnings() != nil {
			t.Fatal("ClearWarnings() did not clear the delete descriptor warnings")
		}
	})
}

// TestUxBlockExecuteDeleteDescriptorFailureIsWarning checks the end-to-end path:
// a failed delete descriptor after a block execute does not change the update
// count and is reported through Conn.Warnings.
func TestUxBlockExecuteDeleteDescriptorFailureIsWarning(t *testing.T) {
	db := &uxDB2{
		fields:           []parameterMarkerField{uxIntField(0xF0)},
		execute:          uxEchoRowsExecute,
		deleteDescriptor: buildTestReplyPacketWithRC(nil, 3, -1),
	}
	var total int64
	var err error
	var warning *SQLWarning
	uxRunSession(t, uxBlockConfig, db.handle, func(c *Conn, _ *uxSession) {
		total, err = c.ExecBatch(context.Background(), "INSERT INTO T VALUES(?)", uxBatchRows(2))
		warning = c.Warnings()
	})
	if err != nil {
		t.Fatalf("ExecBatch() error = %v, want the cleanup failure kept out of the result", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if warning == nil || !strings.Contains(warning.Message, "delete descriptor request failed") {
		t.Fatalf("Warnings() = %+v, want a delete descriptor warning", warning)
	}
}

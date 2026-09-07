package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBuildCommitRequest(t *testing.T) {
	request := buildCommitRequest()
	if len(request) != 40 {
		t.Fatalf("len(buildCommitRequest()) = %d, want 40", len(request))
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionCommit {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionCommit)
	}
	if header.TemplateLength != 20 {
		t.Fatalf("TemplateLength = %d, want 20", header.TemplateLength)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != orsSendReplyImmediately {
		t.Fatalf("ORS bitmap = 0x%x, want 0x%x", got, orsSendReplyImmediately)
	}
}

func TestBuildRollbackRequest(t *testing.T) {
	request := buildRollbackRequest()
	if len(request) != 40 {
		t.Fatalf("len(buildRollbackRequest()) = %d, want 40", len(request))
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionRollback {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionRollback)
	}
	if header.TemplateLength != 20 {
		t.Fatalf("TemplateLength = %d, want 20", header.TemplateLength)
	}
}

func TestBuildEndJobRequest(t *testing.T) {
	request := buildEndJobRequest()
	if len(request) != 40 {
		t.Fatalf("len(buildEndJobRequest()) = %d, want 40", len(request))
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionEndJob {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionEndJob)
	}
	if header.TemplateLength != 0 {
		t.Fatalf("TemplateLength = %d, want 0", header.TemplateLength)
	}
	for i := 20; i < len(request); i++ {
		if request[i] != 0 {
			t.Fatalf("request[%d] = 0x%x, want 0", i, request[i])
		}
	}
}

func TestBuildAutoCommitRequest(t *testing.T) {
	request := buildAutoCommitRequest(true, 4)
	if len(request) != 55 {
		t.Fatalf("len(buildAutoCommitRequest(true, 4)) = %d, want 55", len(request))
	}

	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionSetAttributes {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionSetAttributes)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != 0x81000000 {
		t.Fatalf("ORS bitmap = 0x%x, want 0x81000000", got)
	}
	if got := binary.BigEndian.Uint16(request[44:46]); got != CodePointTrueAutoCommitIndicator {
		t.Fatalf("code point = 0x%x, want 0x%x", got, CodePointTrueAutoCommitIndicator)
	}
	if got := request[46]; got != 0xE8 {
		t.Fatalf("autocommit indicator = 0x%x, want 0xE8", got)
	}
	if got := binary.BigEndian.Uint16(request[53:55]); got != 0 {
		t.Fatalf("commitment control = %d, want 0", got)
	}

	request = buildAutoCommitRequest(false, 4)
	if got := request[46]; got != 0xD5 {
		t.Fatalf("autocommit indicator = 0x%x, want 0xD5", got)
	}
	if got := binary.BigEndian.Uint16(request[53:55]); got != 4 {
		t.Fatalf("commitment control = %d, want 4", got)
	}
}

func TestBeginTxWithIsolationSetsCommitMode(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read isolation request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			t.Errorf("parse isolation request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionExecuteImmediate {
			t.Errorf("isolation request ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionExecuteImmediate)
			return
		}
		stmtPayload, found, err := readTestLLCPValue(packet[40:], 0x3831)
		if err != nil {
			t.Errorf("read isolation SQL text: %v", err)
			return
		}
		if !found {
			t.Errorf("isolation SQL text attribute not found")
			return
		}
		text, err := DecodeUTF16BE(stmtPayload[6:])
		if err != nil {
			t.Errorf("decode isolation SQL text: %v", err)
			return
		}
		if text != "SET TRANSACTION ISOLATION LEVEL CS" {
			t.Errorf("isolation SQL text = %q, want %q", text, "SET TRANSACTION ISOLATION LEVEL CS")
			return
		}
		if _, err := server.Write(buildTestReplyPacket(buildTestSQLCAPayload(t, 0, "", 0))); err != nil {
			t.Errorf("write isolation reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read autocommit-off request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse autocommit-off request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionSetAttributes {
			t.Errorf("autocommit-off ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionSetAttributes)
			return
		}
		if got := requestBodyAutoCommitFlag(t, packet); got != 0xD5 {
			t.Errorf("autocommit-off indicator = 0x%x, want 0xD5", got)
			return
		}
		if got := requestBodyShortValue(t, packet, CodePointCommitmentControlLevel); got != 1 {
			t.Errorf("autocommit-off commitment control = %d, want 1", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write autocommit-off reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read rollback request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse rollback request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionRollback {
			t.Errorf("rollback ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionRollback)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write rollback reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read autocommit-on request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse autocommit-on request header: %v", err)
			return
		}
		if header.ReqRepID != FunctionSetAttributes {
			t.Errorf("autocommit-on ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionSetAttributes)
			return
		}
		if got := requestBodyAutoCommitFlag(t, packet); got != 0xE8 {
			t.Errorf("autocommit-on indicator = 0x%x, want 0xE8", got)
			return
		}
		if got := requestBodyShortValue(t, packet, CodePointCommitmentControlLevel); got != 0 {
			t.Errorf("autocommit-on commitment control = %d, want 0", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write autocommit-on reply: %v", err)
			return
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	tx, err := conn.BeginTx(context.Background(), driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelReadCommitted)})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if tx == nil {
		t.Fatal("BeginTx() returned nil Tx")
	}
	if conn.autoCommit {
		t.Fatal("BeginTx() left autocommit enabled")
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("Tx.Rollback() error = %v", err)
	}
	if !conn.autoCommit {
		t.Fatal("Tx.Rollback() did not restore autocommit")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for transaction test server")
	}
}

func requestBodyAutoCommitFlag(t *testing.T, packet []byte) byte {
	t.Helper()
	payload, found, err := readTestLLCPValue(packet[40:], CodePointTrueAutoCommitIndicator)
	if err != nil {
		t.Fatalf("readTestLLCPValue(0x%x) error = %v", CodePointTrueAutoCommitIndicator, err)
	}
	if !found {
		t.Fatalf("attribute 0x%x not found", CodePointTrueAutoCommitIndicator)
	}
	if len(payload) != 1 {
		t.Fatalf("attribute 0x%x payload length = %d, want 1", CodePointTrueAutoCommitIndicator, len(payload))
	}
	return payload[0]
}

func requestBodyShortValue(t *testing.T, packet []byte, codePoint uint16) uint16 {
	t.Helper()
	payload, found, err := readTestLLCPValue(packet[40:], codePoint)
	if err != nil {
		t.Fatalf("readTestLLCPValue(0x%x) error = %v", codePoint, err)
	}
	if !found {
		t.Fatalf("attribute 0x%x not found", codePoint)
	}
	if len(payload) != 2 {
		t.Fatalf("attribute 0x%x payload length = %d, want 2", codePoint, len(payload))
	}
	return binary.BigEndian.Uint16(payload)
}

func TestTxDoneReturnsSQLTxDone(t *testing.T) {
	tx := &Tx{done: true}

	if err := tx.Commit(); err != sql.ErrTxDone {
		t.Fatalf("Commit() error = %v, want sql.ErrTxDone", err)
	}
	if err := tx.Rollback(); err != sql.ErrTxDone {
		t.Fatalf("Rollback() error = %v, want sql.ErrTxDone", err)
	}
}

func TestTransactionIsolationMode(t *testing.T) {
	tests := []struct {
		name      string
		level     driver.IsolationLevel
		wantMode  string
		wantError bool
	}{
		{name: "default", level: driver.IsolationLevel(sql.LevelDefault)},
		{name: "read uncommitted", level: driver.IsolationLevel(sql.LevelReadUncommitted), wantMode: "CHG"},
		{name: "read committed", level: driver.IsolationLevel(sql.LevelReadCommitted), wantMode: "CS"},
		{name: "repeatable read", level: driver.IsolationLevel(sql.LevelRepeatableRead), wantMode: "ALL"},
		{name: "serializable", level: driver.IsolationLevel(sql.LevelSerializable), wantMode: "RR"},
		{name: "write committed", level: driver.IsolationLevel(sql.LevelWriteCommitted), wantError: true},
		{name: "snapshot", level: driver.IsolationLevel(sql.LevelSnapshot), wantError: true},
		{name: "linearizable", level: driver.IsolationLevel(sql.LevelLinearizable), wantError: true},
		{name: "unknown", level: driver.IsolationLevel(999), wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, err := transactionIsolationMode(test.level)
			if test.wantError {
				if err == nil {
					t.Fatal("transactionIsolationMode() error = nil, want error")
				}
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("transactionIsolationMode() error = %v, want ErrUnsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("transactionIsolationMode() error = %v, want nil", err)
			}
			if mode != test.wantMode {
				t.Fatalf("transactionIsolationMode() mode = %q, want %q", mode, test.wantMode)
			}
		})
	}
}

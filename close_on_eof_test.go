package i400

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestServerAttributeCloseOnEOFByVRM(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql")
	tests := []struct {
		name    string
		vrm     int
		wantSet bool
	}{
		{name: "V6R1M0", vrm: 0x060100, wantSet: true},
		{name: "V7R5M0", vrm: 0x070500, wantSet: true},
		{name: "V5R4M0", vrm: 0x050400, wantSet: false},
		{name: "unknown", vrm: 0, wantSet: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := buildSetServerAttributesRequestVRM(cfg, false, cfg.CommitmentControl, tt.vrm)
			if err != nil {
				t.Fatalf("buildSetServerAttributesRequestVRM() error = %v", err)
			}
			value, found, err := readTestLLCPValue(request[40:], CodePointCloseOnEOF)
			if err != nil {
				t.Fatalf("parse request: %v", err)
			}
			if found != tt.wantSet {
				t.Fatalf("0x383F found = %v, want %v", found, tt.wantSet)
			}
			if tt.wantSet && !bytes.Equal(value, []byte{0xE8}) {
				t.Fatalf("0x383F = % x, want e8", value)
			}
		})
	}
}

func buildTestReplyPacketWithRC(payload []byte, rcClass uint16, rcCode int32) []byte {
	packet := buildTestReplyPacket(payload)
	binary.BigEndian.PutUint16(packet[34:36], rcClass)
	binary.BigEndian.PutUint32(packet[36:40], uint32(rcCode))
	return packet
}

// TestQueryRowsCloseOnEOF checks that the explicit close request is skipped only
// when the server reports (class 2, code 700) that it closed a forward-only
// cursor at end of data.
func TestQueryRowsCloseOnEOF(t *testing.T) {
	tests := []struct {
		name       string
		rcClass    uint16
		rcCode     int32
		scrollable bool
		wantClose  bool
	}{
		{name: "forward-only closed by server", rcClass: 2, rcCode: 700, wantClose: false},
		{name: "forward-only end of data", rcClass: 1, rcCode: 100, wantClose: true},
		{name: "scrollable closed by server is not trusted", rcClass: 2, rcCode: 700, scrollable: true, wantClose: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			done := make(chan struct{})
			go func() {
				defer close(done)
				packet, err := readPacket(server)
				if err != nil {
					t.Errorf("read fetch request: %v", err)
					return
				}
				header, err := ParseHeader(packet[4:])
				if err != nil {
					t.Errorf("parse fetch header: %v", err)
					return
				}
				if header.ReqRepID != FunctionFetch {
					t.Errorf("request id = 0x%x, want fetch 0x%x", header.ReqRepID, FunctionFetch)
					return
				}
				if _, err := server.Write(buildTestReplyPacketWithRC(nil, tt.rcClass, tt.rcCode)); err != nil {
					t.Errorf("write fetch reply: %v", err)
					return
				}

				if !tt.wantClose {
					// Nothing may follow: a close request would show up here.
					_ = server.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
					if packet, err := readPacket(server); err == nil {
						header, _ := ParseHeader(packet[4:])
						t.Errorf("unexpected request after EOF: 0x%x", header.ReqRepID)
					}
					return
				}
				packet, err = readPacket(server)
				if err != nil {
					t.Errorf("read close request: %v", err)
					return
				}
				header, err = ParseHeader(packet[4:])
				if err != nil || header.ReqRepID != FunctionClose {
					t.Errorf("request after EOF = 0x%x (err %v), want close", header.ReqRepID, err)
					return
				}
				if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
					t.Errorf("write close reply: %v", err)
				}
			}()

			rows := &queryRows{
				conn:            &Conn{netConn: client, autoCommit: true},
				ctx:             context.Background(),
				meta:            &resultSetMeta{columns: []columnMeta{{Name: "COL1", Type: db2TypeInteger, Length: 4}}, RowSize: 4},
				statementName:   "STMT0001",
				cursorName:      "CRSR0001",
				fetchBufferSize: 256 * 1024,
				scrollable:      tt.scrollable,
			}
			dest := make([]driver.Value, 1)
			if err := rows.Next(dest); err != io.EOF {
				t.Fatalf("Next() error = %v, want io.EOF", err)
			}
			if err := rows.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("server goroutine did not finish")
			}
		})
	}
}

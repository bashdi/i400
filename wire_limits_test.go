package i400

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
)

// feedPacket writes a length prefix and optional body to one end of a pipe and
// returns the other end for readPacket.
func feedPacket(t *testing.T, prefix []byte, body []byte) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	go func() {
		_, _ = client.Write(prefix)
		if len(body) > 0 {
			_, _ = client.Write(body)
		}
	}()
	return server
}

func lengthPrefix(length uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, length)
	return buf
}

func requireWireError(t *testing.T, err error, operation string) {
	t.Helper()
	var wireErr *WireError
	if !errors.As(err, &wireErr) {
		t.Fatalf("expected *WireError, got %T: %v", err, err)
	}
	if wireErr.Operation != operation {
		t.Fatalf("operation = %q, want %q", wireErr.Operation, operation)
	}
}

func TestReadPacketRejectsOversizedLength(t *testing.T) {
	for _, length := range []uint32{maxPacketLength + 1, 0xFFFFFFFF} {
		conn := feedPacket(t, lengthPrefix(length), nil)
		_, err := readPacket(conn)
		requireWireError(t, err, "validate packet length")
	}
}

func TestReadPacketRejectsUndersizedLength(t *testing.T) {
	for _, length := range []uint32{0, 1, 4, HeaderSize - 1} {
		conn := feedPacket(t, lengthPrefix(length), nil)
		_, err := readPacket(conn)
		requireWireError(t, err, "validate packet length")
	}
}

func TestReadPacketAcceptsHeaderSizedPacket(t *testing.T) {
	body := make([]byte, HeaderSize-4)
	conn := feedPacket(t, lengthPrefix(HeaderSize), body)
	packet, err := readPacket(conn)
	if err != nil {
		t.Fatalf("readPacket: %v", err)
	}
	if len(packet) != HeaderSize {
		t.Fatalf("len(packet) = %d, want %d", len(packet), HeaderSize)
	}
}

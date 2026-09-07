package i400

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

func setConnDeadline(conn net.Conn, ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
}

func clearConnDeadline(conn net.Conn) {
	_ = conn.SetDeadline(time.Time{})
}

func readPacket(conn net.Conn) ([]byte, error) {
	var lengthBuf [4]byte
	if _, err := io.ReadFull(conn, lengthBuf[:]); err != nil {
		return nil, &WireError{Operation: "read packet length", Err: err}
	}
	length := binary.BigEndian.Uint32(lengthBuf[:])
	if length < 4 {
		return nil, &WireError{Operation: "validate packet length", Err: fmt.Errorf("invalid packet length: %d", length)}
	}
	packet := make([]byte, length)
	copy(packet, lengthBuf[:])
	if _, err := io.ReadFull(conn, packet[4:]); err != nil {
		return nil, &WireError{Operation: "read packet body", Err: err}
	}
	return packet, nil
}

func appendU32(dst []byte, value uint32) []byte {
	return binary.BigEndian.AppendUint32(dst, value)
}

func appendU16(dst []byte, value uint16) []byte {
	return binary.BigEndian.AppendUint16(dst, value)
}

func appendU64(dst []byte, value uint64) []byte {
	return binary.BigEndian.AppendUint64(dst, value)
}

func appendI32(dst []byte, value int32) []byte {
	return appendU32(dst, uint32(value))
}

func appendBytes(dst []byte, value []byte) []byte {
	return append(dst, value...)
}

func buildHeader(length uint32, headerID, serverID uint16, templateLength uint16, reqRepID uint16) []byte {
	buf := make([]byte, 20)
	binary.BigEndian.PutUint32(buf[0:4], length)
	binary.BigEndian.PutUint16(buf[4:6], headerID)
	binary.BigEndian.PutUint16(buf[6:8], serverID)
	binary.BigEndian.PutUint32(buf[8:12], 0)
	binary.BigEndian.PutUint32(buf[12:16], 0)
	binary.BigEndian.PutUint16(buf[16:18], templateLength)
	binary.BigEndian.PutUint16(buf[18:20], reqRepID)
	return buf
}

func readReturnCode(packet []byte) (int32, error) {
	if len(packet) < 24 {
		return 0, fmt.Errorf("reply too short: %d", len(packet))
	}
	return int32(binary.BigEndian.Uint32(packet[20:24])), nil
}

func readSimpleReply(packet []byte) (int32, []byte, error) {
	rc, err := readReturnCode(packet)
	if err != nil {
		return 0, nil, err
	}
	if len(packet) < 24 {
		return 0, nil, fmt.Errorf("reply too short: %d", len(packet))
	}
	return rc, packet[24:], nil
}

func readSimpleReplyForID(packet []byte, expectedReplyID uint16) (int32, []byte, error) {
	header, err := ParseHeader(packet)
	if err != nil {
		return 0, nil, err
	}
	if header.ReqRepID != expectedReplyID {
		return 0, nil, fmt.Errorf("unexpected reply id: 0x%x", header.ReqRepID)
	}
	return readSimpleReply(packet)
}

type replyEnvelope struct {
	Header                Header
	ORSBitmap             uint32
	Compressed            uint32
	ReturnORSHandle       uint16
	ReturnDataFunctionID  uint16
	RequestDataFunctionID uint16
	RCClass               uint16
	RCCode                int32
}

func (e replyEnvelope) hasWarning() bool {
	return e.RCClass != 0 && e.RCCode >= 0
}

func (e replyEnvelope) hasError() bool {
	return e.RCClass != 0 && e.RCCode < 0
}

func readReplyEnvelope(packet []byte) (replyEnvelope, []byte, error) {
	if len(packet) < 40 {
		return replyEnvelope{}, nil, fmt.Errorf("reply too short: %d", len(packet))
	}
	header, err := ParseHeader(packet)
	if err != nil {
		return replyEnvelope{}, nil, err
	}
	if header.ReqRepID != 0x2800 {
		return replyEnvelope{}, nil, fmt.Errorf("unexpected reply id: 0x%x", header.ReqRepID)
	}
	envelope := replyEnvelope{
		Header:                header,
		ORSBitmap:             binary.BigEndian.Uint32(packet[20:24]),
		Compressed:            binary.BigEndian.Uint32(packet[24:28]),
		ReturnORSHandle:       binary.BigEndian.Uint16(packet[28:30]),
		ReturnDataFunctionID:  binary.BigEndian.Uint16(packet[30:32]),
		RequestDataFunctionID: binary.BigEndian.Uint16(packet[32:34]),
		RCClass:               binary.BigEndian.Uint16(packet[34:36]),
		RCCode:                int32(binary.BigEndian.Uint32(packet[36:40])),
	}
	return envelope, packet[40:], nil
}

package i400

import (
	"bytes"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
)

// uxRequest is one host-server request received by a fake server.
type uxRequest struct {
	id     uint16
	packet []byte
}

// uxSession records every request a fake server received.
type uxSession struct {
	mu       sync.Mutex
	requests []uxRequest
}

func (s *uxSession) record(req uxRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
}

func (s *uxSession) snapshot() []uxRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uxRequest(nil), s.requests...)
}

// count returns how many requests with the given request id were received.
func (s *uxSession) count(id uint16) int {
	n := 0
	for _, req := range s.snapshot() {
		if req.id == id {
			n++
		}
	}
	return n
}

// uxRunSession connects a Conn to an in-memory fake server and runs fn against
// it. handle answers each request; a nil reply means the request gets none.
// setup, when not nil, configures the Conn before fn runs.
func uxRunSession(t *testing.T, setup func(c *Conn), handle func(s *uxSession, req uxRequest) []byte, fn func(c *Conn, s *uxSession)) *uxSession {
	t.Helper()
	client, server := net.Pipe()
	session := &uxSession{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			packet, err := readPacket(server)
			if err != nil {
				return
			}
			header, err := ParseHeader(packet)
			if err != nil {
				t.Errorf("parse request header: %v", err)
				return
			}
			req := uxRequest{id: header.ReqRepID, packet: packet}
			session.record(req)
			reply := handle(session, req)
			if reply == nil {
				continue
			}
			if _, err := server.Write(reply); err != nil {
				return
			}
		}
	}()

	conn := &Conn{netConn: client, autoCommit: true}
	if setup != nil {
		setup(conn)
	}
	defer func() {
		_ = client.Close()
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("fake server did not stop")
		}
	}()
	watchdog := time.AfterFunc(20*time.Second, func() {
		_ = client.Close()
		_ = server.Close()
	})
	defer watchdog.Stop()
	fn(conn, session)
	return session
}

// uxSQLCA builds a 0x3807 SQLCA payload carrying the SQLCODE, update count and
// result set count.
func uxSQLCA(sqlCode int32, updateCount uint32, resultSets uint32) []byte {
	sqlca := make([]byte, 136)
	binary.BigEndian.PutUint32(sqlca[12:16], uint32(sqlCode))
	binary.BigEndian.PutUint32(sqlca[100:104], resultSets)
	binary.BigEndian.PutUint32(sqlca[104:108], updateCount)
	return appendTestLLCP(nil, 0x3807, sqlca)
}

// uxReply wraps payload in a reply packet with a zero return code.
func uxReply(payload []byte) []byte {
	return buildTestReplyPacket(payload)
}

// uxPrepareReply answers a prepare-describe with the given parameter markers.
func uxPrepareReply(fields ...parameterMarkerField) []byte {
	payload := append(uxSQLCA(0, 0, 0), buildTestPrepareParameterMarkerFormatPayloads(fields...)...)
	return uxReply(payload)
}

// uxIntField is an INTEGER parameter marker with the given parameter mode
// (0xF0 input, 0xF1 output, 0xF2 input/output).
func uxIntField(mode byte) parameterMarkerField {
	return parameterMarkerField{SQLType: db2TypeInteger, Length: 4, CCSID: 37, ParameterType: int(mode)}
}

// uxEBCDIC encodes ASCII text as CCSID 37.
func uxEBCDIC(text string) []byte {
	raw, _ := charmap.CodePage037.NewEncoder().Bytes([]byte(text))
	return raw
}

// uxPad extends raw to length bytes with the EBCDIC blank.
func uxPad(raw []byte, length int) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = 0x40
	}
	copy(out, raw)
	return out
}

// uxContainsText reports whether packet carries text, either as ASCII, CCSID 37
// or UTF-16BE, so tests do not depend on the statement text encoding.
func uxContainsText(packet []byte, text string) bool {
	if bytes.Contains(packet, []byte(text)) || bytes.Contains(packet, uxEBCDIC(text)) {
		return true
	}
	var utf []byte
	for _, unit := range utf16.Encode([]rune(text)) {
		utf = append(utf, byte(unit>>8), byte(unit))
	}
	return bytes.Contains(packet, utf)
}

// uxRequestsWith returns the requests with the given id.
func uxRequestsWith(s *uxSession, id uint16) []uxRequest {
	var out []uxRequest
	for _, req := range s.snapshot() {
		if req.id == id {
			out = append(out, req)
		}
	}
	return out
}

// uxExecuteRowCounts returns, for every execute request, the row count of its
// parameter block (1 for a single-row execute).
func uxExecuteRowCounts(s *uxSession) []uint32 {
	var rows []uint32
	for _, req := range uxRequestsWith(s, FunctionExecute) {
		data, found, _ := readTestLLCPValue(req.packet[40:], CodePointParameterMarkerData)
		if !found {
			data, _, _ = readTestLLCPValue(req.packet[40:], CodePointParameterMarkerDataExt)
		}
		count := uint32(1)
		if len(data) >= 8 {
			count = binary.BigEndian.Uint32(data[4:8])
		}
		rows = append(rows, count)
	}
	return rows
}

// uxDB2 is a generic fake for the statement flows the driver runs: prepare,
// descriptors, open/fetch, execute, execute immediate and the closes. Hooks
// override individual replies.
type uxDB2 struct {
	fields      []parameterMarkerField // parameter markers returned by prepare-describe
	rows        [][]byte               // 4-byte rows returned by fetch
	nulls       []bool                 // indicator per row
	endOfData   bool                   // fetch reports the last block (class 2, code 701)
	updateCount uint32                 // update count of execute and execute immediate

	execute          func(s *uxSession, req uxRequest) []byte
	fetch            func(s *uxSession, req uxRequest) []byte
	deleteDescriptor []byte // reply to delete descriptor, nil for success
}

func (d *uxDB2) handle(s *uxSession, req uxRequest) []byte {
	switch req.id {
	case FunctionCreateRPB:
		return nil // fire-and-forget preamble
	case FunctionPrepareDescribe:
		return uxPrepareReply(d.fields...)
	case FunctionChangeDescriptor:
		return uxReply(uxSQLCA(0, 0, 0))
	case FunctionOpenDescribe:
		return uxReply(append(uxSQLCA(0, 0, 1), buildTestDescribePayload()...))
	case FunctionFetch:
		if d.fetch != nil {
			return d.fetch(s, req)
		}
		var class uint16
		var code int32
		if d.endOfData {
			class, code = 2, 701
		}
		return buildTestReplyPacketWithRC(buildTestResultDataPayload(1, 4, d.rows, d.nulls), class, code)
	case FunctionExecute, FunctionExecuteImmediate:
		if d.execute != nil {
			return d.execute(s, req)
		}
		return uxReply(uxSQLCA(0, d.updateCount, 0))
	case FunctionDeleteDescriptor:
		if d.deleteDescriptor != nil {
			return d.deleteDescriptor
		}
		return uxReply(nil)
	case FunctionClose:
		// After a last block (code 701) the driver closes the cursor without
		// waiting for a reply, as JTOpen does.
		if d.endOfData {
			return nil
		}
		return uxReply(nil)
	}
	return uxReply(nil)
}

// uxEchoRowsExecute replies to an execute with the row count of its parameter
// block, the way the host reports the update count of a block.
func uxEchoRowsExecute(s *uxSession, req uxRequest) []byte {
	data, found, _ := readTestLLCPValue(req.packet[40:], CodePointParameterMarkerData)
	if !found {
		data, _, _ = readTestLLCPValue(req.packet[40:], CodePointParameterMarkerDataExt)
	}
	rows := uint32(1)
	if len(data) >= 8 {
		rows = binary.BigEndian.Uint32(data[4:8])
	}
	return uxReply(uxSQLCA(0, rows, 0))
}

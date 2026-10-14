package i400

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"
)

// uqORSSendReply is the ORS bit a request sets when the server must answer it.
// Requests without it (Create RPB, end job, close with no reply) get no reply.
const uqORSSendReply = 0x80000000

// uqReply builds the server answer to one request. Returning nil sends nothing.
type uqReply func(request []byte) []byte

type uqRequest struct {
	id     uint16
	packet []byte
}

// uqFake is a scripted IBM i server on the far side of a net.Pipe. Requests are
// recorded in arrival order. A reply is taken from the per-request-id script
// queue when one is pending; otherwise a request that asks for a reply gets a
// plain success reply, so unscripted steps do not block.
type uqFake struct {
	client net.Conn
	server net.Conn
	done   chan struct{}

	mu     sync.Mutex
	script map[uint16][]uqReply
	reqs   []uqRequest
}

// newUQFake starts the server goroutine. A watchdog closes the client after a
// while so a driver that waits for a reply that never comes fails the test
// instead of hanging the run.
func newUQFake(t *testing.T) *uqFake {
	t.Helper()
	client, server := net.Pipe()
	f := &uqFake{client: client, server: server, done: make(chan struct{}), script: map[uint16][]uqReply{}}
	watchdog := time.AfterFunc(15*time.Second, func() { _ = client.Close() })
	t.Cleanup(func() {
		watchdog.Stop()
		_ = client.Close()
		_ = server.Close()
		<-f.done
	})
	go f.serve()
	return f
}

// conn returns a driver connection running over the fake server.
func (f *uqFake) conn(cfg Config) *Conn {
	return &Conn{cfg: cfg, netConn: f.client, autoCommit: true}
}

func (f *uqFake) serve() {
	defer close(f.done)
	for {
		packet, err := readPacket(f.server)
		if err != nil {
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, uqRequest{id: header.ReqRepID, packet: packet})
		var reply []byte
		if queue := f.script[header.ReqRepID]; len(queue) > 0 {
			f.script[header.ReqRepID] = queue[1:]
			reply = queue[0](packet)
		} else if uqExpectsReply(packet) {
			reply = buildTestReplyPacket(nil)
		}
		f.mu.Unlock()
		if reply != nil {
			if _, err := f.server.Write(reply); err != nil {
				return
			}
		}
	}
}

// push queues replies for the next requests with the given id, in order.
func (f *uqFake) push(id uint16, replies ...uqReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script[id] = append(f.script[id], replies...)
}

// requests returns a copy of every request received so far.
func (f *uqFake) requests() []uqRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uqRequest(nil), f.reqs...)
}

// ids returns the request ids in arrival order.
func (f *uqFake) ids() []uint16 {
	reqs := f.requests()
	ids := make([]uint16, len(reqs))
	for i, r := range reqs {
		ids[i] = r.id
	}
	return ids
}

// count returns how many requests with id arrived.
func (f *uqFake) count(id uint16) int {
	n := 0
	for _, r := range f.requests() {
		if r.id == id {
			n++
		}
	}
	return n
}

// waitCount polls until n requests with id have arrived. It is used for
// fire-and-forget requests, which the client does not wait on.
func (f *uqFake) waitCount(t *testing.T, id uint16, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.count(id) < n {
		if time.Now().After(deadline) {
			t.Fatalf("request 0x%04x seen %d times, want %d (ids %v)", id, f.count(id), n, f.ids())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// uqExpectsReply reports whether the request asks for a reply.
func uqExpectsReply(packet []byte) bool {
	return len(packet) >= 24 && binary.BigEndian.Uint32(packet[20:24])&uqORSSendReply != 0
}

// uqHandle returns the RPB (statement) handle carried in a request template.
func uqHandle(packet []byte) uint16 {
	return binary.BigEndian.Uint16(packet[34:36])
}

// uqCheckIDs fails the test unless the request ids match want exactly.
func uqCheckIDs(t *testing.T, f *uqFake, want ...uint16) {
	t.Helper()
	got := f.ids()
	if len(got) != len(want) {
		t.Fatalf("request ids = %x, want %x", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request ids = %x, want %x", got, want)
		}
	}
}

// uqReplyWith answers with a success return code and the given payload.
func uqReplyWith(payload []byte) uqReply {
	return func([]byte) []byte { return buildTestReplyPacket(payload) }
}

// uqReplyRC answers with the given return class and code and payload.
func uqReplyRC(class uint16, code int32, payload []byte) uqReply {
	return func([]byte) []byte { return buildTestReplyPacketWithRC(payload, class, code) }
}

// uqNoReply sends nothing back, for requests the test expects to go unanswered.
func uqNoReply() uqReply {
	return func([]byte) []byte { return nil }
}

// uqSQLCA builds an SQLCA code point with the given SQLCODE, SQLSTATE, result
// set count and update count.
func uqSQLCA(code int32, state string, resultSets int, updateCount int32) []byte {
	sqlca := make([]byte, 136)
	binary.BigEndian.PutUint32(sqlca[12:16], uint32(code))
	binary.BigEndian.PutUint32(sqlca[100:104], uint32(resultSets))
	binary.BigEndian.PutUint32(sqlca[104:108], uint32(updateCount))
	if state != "" {
		stateBytes, err := EncodeEBCDIC37(state)
		if err != nil {
			panic(err)
		}
		copy(sqlca[131:136], stateBytes)
	}
	return appendTestLLCP(nil, 0x3807, sqlca)
}

// uqParamFormat builds a parameter marker format code point with one entry per field.
func uqParamFormat(fields ...parameterMarkerField) []byte {
	data := make([]byte, 8+54*len(fields))
	binary.BigEndian.PutUint16(data[4:6], uint16(len(fields)))
	recordSize := 0
	for _, field := range fields {
		recordSize += field.Length
	}
	binary.BigEndian.PutUint16(data[6:8], uint16(recordSize))
	for i, field := range fields {
		base := 8 + 54*i
		binary.BigEndian.PutUint16(data[base+0:], 54)
		binary.BigEndian.PutUint16(data[base+2:], uint16(field.SQLType))
		binary.BigEndian.PutUint16(data[base+4:], uint16(field.Length))
		binary.BigEndian.PutUint16(data[base+6:], uint16(field.Scale))
		binary.BigEndian.PutUint16(data[base+8:], uint16(field.Precision))
		binary.BigEndian.PutUint16(data[base+10:], uint16(field.CCSID))
		data[base+12] = byte(field.ParameterType)
	}
	return appendTestLLCP(nil, CodePointParameterMarkerFormat, data)
}

// uqIntParam is one INTEGER input parameter.
var uqIntParam = parameterMarkerField{SQLType: db2TypeInteger, Length: 4}

// uqPrepareReply is the prepare reply for a statement with the given parameters.
func uqPrepareReply(fields ...parameterMarkerField) uqReply {
	payload := append(uqSQLCA(0, "", 0, 0), uqParamFormat(fields...)...)
	return uqReplyWith(payload)
}

// uqOpenReply is the open reply for a result set with one INTEGER column.
func uqOpenReply() uqReply {
	payload := append(uqSQLCA(0, "", 1, 0), buildTestDescribePayload()...)
	return uqReplyWith(payload)
}

// uqFetch builds an extended result data block with one INTEGER column. Each
// row carries an indicator of indicatorSize bytes; nulls marks NULL rows
// (indicator 0xFFFF) and needs indicatorSize >= 2.
func uqFetch(indicatorSize int, values []int32, nulls []bool) []byte {
	data := appendU32(nil, 0) // consistency token
	data = appendU32(data, uint32(len(values)))
	data = appendU16(data, 1)
	data = appendU16(data, uint16(indicatorSize))
	data = appendU32(data, 0) // reserved
	data = appendU32(data, 4) // row size
	for i := range values {
		if indicatorSize == 0 {
			continue
		}
		indicator := make([]byte, indicatorSize)
		if nulls != nil && nulls[i] {
			indicator[0], indicator[1] = 0xFF, 0xFF
		}
		data = append(data, indicator...)
	}
	for _, v := range values {
		data = appendU32(data, uint32(v))
	}
	return appendTestLLCP(nil, 0x380E, data)
}

// uqFetchReply answers a fetch with one block and the given return code. Class
// 2 / code 700 closes the cursor, class 2 / code 701 marks the last block, and
// class 1 / code 100 with no rows is end of data.
func uqFetchReply(class uint16, code int32, indicatorSize int, values []int32, nulls []bool) uqReply {
	payload := uqFetch(indicatorSize, values, nulls)
	return func([]byte) []byte { return buildTestReplyPacketWithRC(payload, class, code) }
}

// uqEndOfData is the empty fetch reply that ends a result set.
func uqEndOfData() uqReply {
	return uqFetchReply(1, 100, 0, nil, nil)
}

// uqSQLError is a reply that carries an SQL error with the given SQLCODE and state.
func uqSQLError(code int32, state string) uqReply {
	return uqReplyRC(1, code, uqSQLCA(code, state, 0, 0))
}

// uqPushSingleRowQuery queues the prepare, open and fetch replies for a SELECT
// over one INTEGER parameter that returns one row (42) and ends with class 2 /
// code 701.
func uqPushSingleRowQuery(f *uqFake) {
	f.push(FunctionPrepareDescribe, uqPrepareReply(uqIntParam))
	f.push(FunctionOpenDescribe, uqOpenReply())
	f.push(FunctionFetch, uqFetchReply(2, 701, 0, []int32{42}, nil))
}

package i400

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ucSignonAddr is where connectSystemInfo always dials (SignonPort). The
// address is fixed by the driver, so tests that go through the signon exchange
// bind this loopback port and skip when it is busy.
var ucSignonAddr = fmt.Sprintf("127.0.0.1:%d", SignonPort)

// ucMapperAddr is where ResolvePort always dials (the port mapper, port 449).
var ucMapperAddr = fmt.Sprintf("127.0.0.1:%d", PortMapperPort)

// ucServerSeed is the server seed the fake hosts send in both exchanges.
var ucServerSeed = []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}

const (
	ucUser     = "QSECOFR"
	ucPassword = "Secret1"
	ucJobName  = "123456/QUSER/QZDASOINIT"
	// ucJobID is 26 characters, the width of the server attributes job identifier.
	ucJobID = "123456/QUSER/QZDASOINIT   "
)

// ucRecord is one host-server request received by a ucHost.
type ucRecord struct {
	ReqRepID uint16
	ServerID uint16
	Raw      []byte
}

// ucReplyFunc returns the reply packet for a request, or nil to stay silent.
type ucReplyFunc func(rec ucRecord) []byte

// ucHost is a fake IBM i host server on a loopback port. It records every
// request it receives and answers with the reply function.
type ucHost struct {
	t       *testing.T
	ln      net.Listener
	reply   ucReplyFunc
	mu      sync.Mutex
	records []ucRecord
	conns   []net.Conn
}

// ucListen starts a fake host on addr. A busy fixed address skips the test.
func ucListen(t *testing.T, addr string, reply ucReplyFunc) *ucHost {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	h := &ucHost{t: t, ln: ln, reply: reply}
	t.Cleanup(h.close)
	go h.acceptLoop()
	return h
}

func (h *ucHost) port() int {
	return h.ln.Addr().(*net.TCPAddr).Port
}

func (h *ucHost) close() {
	_ = h.ln.Close()
	h.mu.Lock()
	conns := h.conns
	h.conns = nil
	h.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (h *ucHost) acceptLoop() {
	for {
		conn, err := h.ln.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		h.conns = append(h.conns, conn)
		h.mu.Unlock()
		go h.serve(conn)
	}
}

func (h *ucHost) serve(conn net.Conn) {
	for {
		packet, err := readPacket(conn)
		if err != nil {
			return
		}
		header, err := ParseHeader(packet)
		if err != nil {
			return
		}
		rec := ucRecord{ReqRepID: header.ReqRepID, ServerID: header.ServerID, Raw: packet}
		h.mu.Lock()
		h.records = append(h.records, rec)
		h.mu.Unlock()
		if h.reply == nil {
			continue
		}
		if out := h.reply(rec); out != nil {
			if _, err := conn.Write(out); err != nil {
				return
			}
		}
	}
}

func (h *ucHost) requests() []ucRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ucRecord(nil), h.records...)
}

func (h *ucHost) count(reqRepID uint16) int {
	n := 0
	for _, rec := range h.requests() {
		if rec.ReqRepID == reqRepID {
			n++
		}
	}
	return n
}

// waitFor returns the first request with the given request id, failing the
// test if it does not arrive within five seconds.
func (h *ucHost) waitFor(reqRepID uint16) ucRecord {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if rec, ok := ucFind(h.requests(), reqRepID); ok {
			return rec
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("host on port %d did not receive request 0x%x", h.port(), reqRepID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func ucFind(recs []ucRecord, reqRepID uint16) (ucRecord, bool) {
	for _, rec := range recs {
		if rec.ReqRepID == reqRepID {
			return rec, true
		}
	}
	return ucRecord{}, false
}

// ucDialTo starts a fake host with reply and returns it with a connection to it.
func ucDialTo(t *testing.T, reply ucReplyFunc) (*ucHost, net.Conn) {
	t.Helper()
	h := ucListen(t, "127.0.0.1:0", reply)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", h.port()))
	if err != nil {
		t.Fatalf("dial fake host: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return h, conn
}

// ucSimpleReply builds a reply with a return code and payload, the framing of
// the signon and database seed and start-server replies.
func ucSimpleReply(serverID, reqRepID uint16, rc int32, payload []byte) []byte {
	packet := buildHeader(uint32(24+len(payload)), 0, serverID, 0, reqRepID)
	packet = appendI32(packet, rc)
	return append(packet, payload...)
}

type ucItem struct {
	cp   uint16
	data []byte
}

func ucItems(items ...ucItem) []byte {
	var out []byte
	for _, item := range items {
		out = AppendLLCP(out, item.cp, item.data)
	}
	return out
}

func ucU16(v uint16) []byte { return appendU16(nil, v) }
func ucU32(v uint32) []byte { return appendU32(nil, v) }

// ucEBCDIC returns text in EBCDIC 37, blank padded to size bytes.
func ucEBCDIC(text string, size int) []byte {
	encoded, err := EncodeEBCDIC37BlankPad(text, size)
	if err != nil {
		panic(err)
	}
	return encoded
}

// ucEBCDICExact returns text in EBCDIC 37 without padding.
func ucEBCDICExact(text string) []byte {
	encoded, err := EncodeEBCDIC37(text)
	if err != nil {
		panic(err)
	}
	return encoded
}

// ucAttributesBlock builds the 0x3804 server attributes payload: the functional
// level at offset 52 (10 bytes) and the job identifier at offset 90 (26 bytes),
// as the live server lays them out (see serverAttributesFunctionalLevelOffset).
func ucAttributesBlock(functionalLevel, jobIdentifier string) []byte {
	block := bytes.Repeat([]byte{0x40}, 116)
	copy(block[52:62], ucEBCDIC(functionalLevel, 10))
	copy(block[90:116], ucEBCDIC(jobIdentifier, 26))
	return block
}

// ucSignonScript describes how the fake signon server answers.
type ucSignonScript struct {
	vrm           uint32
	dsLevel       uint16
	passwordLevel byte
	serverSeed    []byte
	signonRC      int32
	serverCCSID   uint32
}

func ucDefaultSignon(passwordLevel byte) ucSignonScript {
	return ucSignonScript{
		vrm:           0x070200, // V7R2M0
		dsLevel:       5,
		passwordLevel: passwordLevel,
		serverSeed:    ucServerSeed,
		serverCCSID:   37,
	}
}

func ucSignonReply(s ucSignonScript) ucReplyFunc {
	return func(rec ucRecord) []byte {
		switch rec.ReqRepID {
		case RequestExchangeAttributes:
			return ucSimpleReply(SignonServerID, ReplyExchangeAttributes, 0, ucItems(
				ucItem{CodePointClientVersion, ucU32(s.vrm)},
				ucItem{CodePointClientDSLevel, ucU16(s.dsLevel)},
				ucItem{CodePointClientSeed, s.serverSeed},
				ucItem{CodePointPasswordLevel, []byte{s.passwordLevel}},
			))
		case RequestSignonInfo:
			return ucSimpleReply(SignonServerID, ReplySignonInfo, s.signonRC, ucItems(
				ucItem{CodePointServerCCSID, ucU32(s.serverCCSID)},
			))
		}
		return nil
	}
}

// ucDBScript describes how the fake database server answers.
type ucDBScript struct {
	serverSeed      []byte
	startRC         int32
	jobName         string
	functionalLevel string
	jobIdentifier   string
	libraryRC       int32
}

func ucDefaultDB() ucDBScript {
	return ucDBScript{
		serverSeed:      ucServerSeed,
		jobName:         ucJobName,
		functionalLevel: "V7R2M005",
		jobIdentifier:   ucJobID,
	}
}

func ucDBReply(s ucDBScript) ucReplyFunc {
	return func(rec ucRecord) []byte {
		switch rec.ReqRepID {
		case FunctionTestConnection:
			return buildTestReplyPacketWithRC(nil, 7, -201)
		case RequestExchangeRandomSeeds:
			return ucSimpleReply(DatabaseServerID, ReplyExchangeRandomSeeds, 0, s.serverSeed)
		case RequestStartServer:
			jobPayload := append(ucU32(0), ucEBCDICExact(s.jobName)...)
			return ucSimpleReply(DatabaseServerID, ReplyStartServer, s.startRC, ucItems(
				ucItem{CodePointJobName, jobPayload},
			))
		case FunctionSetAttributes:
			return buildTestReplyPacketWithRC(ucItems(
				ucItem{CodePointServerAttributes, ucAttributesBlock(s.functionalLevel, s.jobIdentifier)},
			), 0, 0)
		case FunctionAddLibraryList:
			if s.libraryRC != 0 {
				return buildTestReplyPacketWithRC(nil, 1, s.libraryRC)
			}
			return buildTestReplyPacket(nil)
		}
		return nil
	}
}

// ucStartRig starts the fixed-port signon host and a database host on a free port.
func ucStartRig(t *testing.T, signon ucSignonScript, db ucDBScript) (*ucHost, *ucHost) {
	t.Helper()
	signonHost := ucListen(t, ucSignonAddr, ucSignonReply(signon))
	dbHost := ucListen(t, "127.0.0.1:0", ucDBReply(db))
	return signonHost, dbHost
}

// ucDSN builds a DSN for the loopback fake hosts. A zero port leaves the port
// out so the driver resolves it through the port mapper.
func ucDSN(port int, query string) string {
	var params []string
	if port > 0 {
		params = append(params, "port="+strconv.Itoa(port))
	}
	if query != "" {
		params = append(params, query)
	}
	dsn := "as400://qsecofr:" + ucPassword + "@127.0.0.1/"
	if len(params) > 0 {
		dsn += "?" + strings.Join(params, "&")
	}
	return dsn
}

// ucStartMapper answers port mapper requests on ucMapperAddr. The returned
// channel receives each service name the driver asked for.
func ucStartMapper(t *testing.T, reply func(service string) []byte) <-chan string {
	t.Helper()
	ln, err := net.Listen("tcp", ucMapperAddr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ucMapperAddr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	seen := make(chan string, 16)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 128)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				service := string(buf[:n])
				seen <- service
				_, _ = c.Write(reply(service))
			}(conn)
		}
	}()
	return seen
}

// ucMapperPort builds a successful port mapper reply: '+' followed by the port.
func ucMapperPort(port int) []byte {
	return append([]byte{'+'}, ucU32(uint32(port))...)
}

// ucCheckPasswordRequest verifies the password substitute sent in a signon or
// start-server request. The scheme byte is at offset 20; the LLCP items start at
// itemsOffset. The expected length and scheme are fixed per password level.
func ucCheckPasswordRequest(t *testing.T, what string, raw []byte, itemsOffset, level int, clientSeed []byte) {
	t.Helper()
	expect := map[int]struct {
		scheme byte
		length int
	}{
		0: {0x01, 8},
		2: {0x03, 20},
		4: {0x07, 64},
	}[level]
	if got := raw[20]; got != expect.scheme {
		t.Errorf("%s: auth scheme = 0x%02x, want 0x%02x for level %d", what, got, expect.scheme, level)
	}
	password, found, err := readTestLLCPValue(raw[itemsOffset:], CodePointPassword)
	if err != nil || !found {
		t.Fatalf("%s: password item found=%v err=%v", what, found, err)
	}
	if len(password) != expect.length {
		t.Errorf("%s: substitute length = %d, want %d for level %d", what, len(password), expect.length, level)
	}
	want, err := passwordSubstitute(ucUser, ucPassword, level, clientSeed, ucServerSeed)
	if err != nil {
		t.Fatalf("passwordSubstitute() error = %v", err)
	}
	if !bytes.Equal(password, want) {
		t.Errorf("%s: substitute = %x, want %x", what, password, want)
	}
	user, found, err := readTestLLCPValue(raw[itemsOffset:], CodePointUserID)
	if err != nil || !found {
		t.Fatalf("%s: user item found=%v err=%v", what, found, err)
	}
	if !bytes.Equal(user, ucEBCDIC(ucUser, 10)) {
		t.Errorf("%s: user = %x, want EBCDIC %q padded to 10", what, user, ucUser)
	}
}

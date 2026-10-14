package i400

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// clientInfoTestVRM is V6R1M0 in JTOpen's VRM encoding.
const clientInfoTestVRM = 0x060100

func TestClientInfoAttributeCodePoints(t *testing.T) {
	tests := []struct {
		name      string
		codePoint uint16
	}{
		{"ApplicationName", 0x3838},
		{"ClientUser", 0x3839},
		{"ClientAccounting", 0x3837},
		{"ClientHostname", 0x383A},
		{"ClientProgramID", 0x383B},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp, ok := clientInfoCodePoint(tt.name)
			if !ok || cp != tt.codePoint {
				t.Fatalf("clientInfoCodePoint(%q) = 0x%x, %v; want 0x%x", tt.name, cp, ok, tt.codePoint)
			}
			request, err := buildSetClientInfoRequest(cp, "abc")
			if err != nil {
				t.Fatalf("buildSetClientInfoRequest() error = %v", err)
			}
			if header, err := ParseHeader(request[4:]); err != nil || header.ReqRepID != FunctionSetAttributes {
				t.Fatalf("request id = 0x%x (err %v), want set attributes", header.ReqRepID, err)
			}
			payload, found, err := readTestLLCPValue(request[40:], tt.codePoint)
			if err != nil || !found {
				t.Fatalf("0x%x found = %v, err = %v", tt.codePoint, found, err)
			}
			// Payload: CCSID (2), length (2), EBCDIC bytes.
			want, _ := EncodeEBCDIC37("abc")
			wantPayload := append([]byte{0x00, 37, 0x00, byte(len(want))}, want...)
			if !bytes.Equal(payload, wantPayload) {
				t.Fatalf("0x%x payload = % x, want % x", tt.codePoint, payload, wantPayload)
			}
		})
	}
	if _, ok := clientInfoCodePoint("ClientHostnameX"); ok {
		t.Fatal("unknown name has a code point")
	}
}

func TestClientInfoClientBytesLimit(t *testing.T) {
	long := make([]byte, clientInfoMaxBytes+1)
	for i := range long {
		long[i] = 'A'
	}
	if _, err := buildSetClientInfoRequest(codePointClientUser, string(long)); err == nil {
		t.Fatal("expected error for a value longer than 255 bytes")
	}
	cfg := Config{ClientUser: string(long)}
	if _, _, err := appendVRMOptionAttributes(nil, cfg, clientInfoTestVRM); err == nil {
		t.Fatal("expected error for DSN value longer than 255 bytes")
	}
}

func TestClientInfoDSNAttributesByVRM(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql&applicationName=MYAPP&clientUser=ALICE&clientAccounting=ACCT1&clientHostname=HOST1&clientProgramID=PGM1")
	tests := []struct {
		name    string
		vrm     int
		wantSet bool
	}{
		{name: "V6R1M0", vrm: clientInfoTestVRM, wantSet: true},
		{name: "V7R5M0", vrm: 0x070500, wantSet: true},
		{name: "V5R4M0", vrm: 0x050400, wantSet: false},
		{name: "unknown", vrm: 0, wantSet: false},
	}
	codePoints := []uint16{codePointApplicationName, codePointClientUser, codePointClientAccounting, codePointClientHostname, codePointClientProgramID}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request, err := buildSetServerAttributesRequestVRM(cfg, false, cfg.CommitmentControl, tt.vrm)
			if err != nil {
				t.Fatalf("buildSetServerAttributesRequestVRM() error = %v", err)
			}
			for _, cp := range codePoints {
				_, found, err := readTestLLCPValue(request[40:], cp)
				if err != nil {
					t.Fatalf("parse request: %v", err)
				}
				if found != tt.wantSet {
					t.Fatalf("0x%x found = %v, want %v", cp, found, tt.wantSet)
				}
			}
		})
	}
}

func TestClientInfoUnsetDSNValuesNotSent(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql&applicationName=MYAPP")
	request, err := buildSetServerAttributesRequestVRM(cfg, false, cfg.CommitmentControl, clientInfoTestVRM)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequestVRM() error = %v", err)
	}
	if _, found, _ := readTestLLCPValue(request[40:], codePointClientUser); found {
		t.Fatal("unset ClientUser was sent")
	}
	if _, found, _ := readTestLLCPValue(request[40:], codePointApplicationName); !found {
		t.Fatal("ApplicationName was not sent")
	}
}

func TestQueryStorageLimitDSNDefaultAndAttribute(t *testing.T) {
	def := parseAttributeTestDSN(t, "naming=sql")
	if def.QueryStorageLimit != -1 {
		t.Fatalf("default QueryStorageLimit = %d, want -1", def.QueryStorageLimit)
	}
	request, err := buildSetServerAttributesRequestVRM(def, false, def.CommitmentControl, clientInfoTestVRM)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequestVRM() error = %v", err)
	}
	if _, found, _ := readTestLLCPValue(request[40:], codePointQueryStorageLimit); found {
		t.Fatal("default queryStorageLimit (-1) was sent")
	}

	tests := []struct {
		name  string
		query string
		vrm   int
		want  []byte
		found bool
	}{
		{"explicit value", "queryStorageLimit=1000", clientInfoTestVRM, []byte{0x00, 0x00, 0x03, 0xE8}, true},
		{"below -1 clamps to -1", "queryStorageLimit=-5", clientInfoTestVRM, []byte{0xFF, 0xFF, 0xFF, 0xFF}, true},
		{"above max clamps to max", "queryStorageLimit=2147483647", clientInfoTestVRM, []byte{0x7F, 0xFE, 0x00, 0x02}, true},
		{"old VRM not sent", "queryStorageLimit=1000", 0x050400, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseAttributeTestDSN(t, "naming=sql&"+tt.query)
			request, err := buildSetServerAttributesRequestVRM(cfg, false, cfg.CommitmentControl, tt.vrm)
			if err != nil {
				t.Fatalf("buildSetServerAttributesRequestVRM() error = %v", err)
			}
			value, found, err := readTestLLCPValue(request[40:], codePointQueryStorageLimit)
			if err != nil {
				t.Fatalf("parse request: %v", err)
			}
			if found != tt.found {
				t.Fatalf("0x3834 found = %v, want %v", found, tt.found)
			}
			if tt.found && !bytes.Equal(value, tt.want) {
				t.Fatalf("0x3834 = % x, want % x", value, tt.want)
			}
		})
	}
}

func TestQueryTimeoutMechanismDSN(t *testing.T) {
	if cfg := parseAttributeTestDSN(t, "naming=sql"); cfg.QueryTimeoutMechanism != queryTimeoutMechanismCancel {
		t.Fatalf("default mechanism = %q, want cancel", cfg.QueryTimeoutMechanism)
	}
	if cfg := parseAttributeTestDSN(t, "queryTimeoutMechanism=qqrytimlmt"); cfg.QueryTimeoutMechanism != queryTimeoutMechanismQQRYTIMLMT {
		t.Fatalf("mechanism = %q, want qqrytimlmt", cfg.QueryTimeoutMechanism)
	}
	if _, err := ParseDSN("as400://user:pass@host/qgpl?queryTimeoutMechanism=bogus"); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("invalid mechanism error = %v, want ErrInvalidDSN", err)
	}
}

func TestRemainingSeconds(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want int32
	}{
		{0, 1},
		{-time.Second, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{1200 * time.Millisecond, 2},
		{5 * time.Second, 5},
		{5*time.Second + time.Nanosecond, 6},
	}
	for _, tt := range tests {
		if got := remainingSeconds(tt.d); got != tt.want {
			t.Errorf("remainingSeconds(%v) = %d, want %d", tt.d, got, tt.want)
		}
	}
}

// serveOneRequest reads one request from server, hands it to check and sends
// a reply with the given return code class and code.
func serveOneRequest(t *testing.T, server net.Conn, rcClass uint16, rcCode int32, check func(functionID uint16, packet []byte)) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse header: %v", err)
			return
		}
		check(header.ReqRepID, packet)
		if _, err := server.Write(buildTestReplyPacketWithRC(nil, rcClass, rcCode)); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}()
	return done
}

// readOneRequestNoReply reads one request from server and hands it to check
// without sending a reply, as the server does for an RPB change (JTOpen's
// connection.send).
func readOneRequestNoReply(t *testing.T, server net.Conn, check func(functionID uint16, packet []byte)) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		header, err := ParseHeader(packet[4:])
		if err != nil {
			t.Errorf("parse header: %v", err)
			return
		}
		check(header.ReqRepID, packet)
	}()
	return done
}

func TestSetClientInfoSendsOneAttributeAndUpdatesValue(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cfg := parseAttributeTestDSN(t, "naming=sql&clientUser=DSNUSER")
	conn := &Conn{cfg: cfg, netConn: client, systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}

	done := serveOneRequest(t, server, 0, 0, func(id uint16, packet []byte) {
		if id != FunctionSetAttributes {
			t.Errorf("request id = 0x%x, want set attributes", id)
		}
		body := packet[40:]
		if _, found, _ := readTestLLCPValue(body, codePointApplicationName); !found {
			t.Errorf("0x3838 not found in request")
		}
		if _, found, _ := readTestLLCPValue(body, codePointClientUser); found {
			t.Errorf("request also carries ClientUser")
		}
		if len(body) != 10+len(mustEBCDIC(t, "MYAPP")) {
			t.Errorf("body length = %d, want only the ApplicationName attribute", len(body))
		}
	})

	if got := conn.ClientInfo("ApplicationName"); got != "" {
		t.Fatalf("initial ApplicationName = %q, want empty", got)
	}
	if err := conn.SetClientInfo(context.Background(), "ApplicationName", "MYAPP"); err != nil {
		t.Fatalf("SetClientInfo() error = %v", err)
	}
	<-done
	if got := conn.ClientInfo("ApplicationName"); got != "MYAPP" {
		t.Fatalf("ClientInfo(ApplicationName) = %q, want MYAPP", got)
	}
	if got := conn.ClientInfo("ClientUser"); got != "DSNUSER" {
		t.Fatalf("ClientInfo(ClientUser) = %q, want DSN value DSNUSER", got)
	}
}

func TestSetClientInfoServerErrorRestoresPreviousValue(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	conn := &Conn{netConn: client, systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}
	done := serveOneRequest(t, server, 2, -199, func(uint16, []byte) {})
	if err := conn.SetClientInfo(context.Background(), "ClientHostname", "HOST2"); err == nil {
		t.Fatal("SetClientInfo() error = nil, want server error")
	}
	<-done
	if got := conn.ClientInfo("ClientHostname"); got != "" {
		t.Fatalf("ClientHostname after failed set = %q, want previous empty value", got)
	}
}

func TestSetClientInfoUnknownNameAndOldVRM(t *testing.T) {
	conn := &Conn{cfg: Config{ClientUser: "DSN"}, systemInfo: &SystemInfo{ServerVersion: 0x050400}}
	if err := conn.SetClientInfo(context.Background(), "NoSuchName", "x"); !errors.Is(err, ErrUnknownClientInfo) {
		t.Fatalf("unknown name error = %v, want ErrUnknownClientInfo", err)
	}
	if got := conn.ClientInfo("NoSuchName"); got != "" {
		t.Fatalf("ClientInfo(unknown) = %q, want empty", got)
	}
	// Below V6R1 nothing is sent (netConn is nil, so any request would fail).
	if err := conn.SetClientInfo(context.Background(), "ClientUser", "RUNTIME"); err != nil {
		t.Fatalf("SetClientInfo() below V6R1 error = %v", err)
	}
	if got := conn.ClientInfo("ClientUser"); got != "RUNTIME" {
		t.Fatalf("ClientInfo(ClientUser) = %q, want RUNTIME", got)
	}
}

func TestResetSessionRestoresDSNClientInfo(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cfg := Config{ClientUser: "DSNUSER"}
	conn := &Conn{cfg: cfg, netConn: client, systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}
	conn.clientInfo = map[string]string{"ClientUser": "RUNTIME"}

	done := serveOneRequest(t, server, 0, 0, func(id uint16, packet []byte) {
		if id != FunctionSetAttributes {
			t.Errorf("request id = 0x%x, want set attributes", id)
		}
		value, found, _ := readTestLLCPValue(packet[40:], codePointClientUser)
		if !found {
			t.Errorf("0x3839 not sent on restore")
			return
		}
		if want := mustEBCDIC(t, "DSNUSER"); !bytes.Contains(value, want) {
			t.Errorf("restored value = % x, want DSNUSER", value)
		}
	})
	if err := conn.restoreClientInfo(context.Background()); err != nil {
		t.Fatalf("restoreClientInfo() error = %v", err)
	}
	<-done
	if got := conn.ClientInfo("ClientUser"); got != "DSNUSER" {
		t.Fatalf("ClientInfo(ClientUser) after restore = %q, want DSNUSER", got)
	}
}

func TestQueryTimeoutRPBWithDeadlineQQRYTIMLMT(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cfg := parseAttributeTestDSN(t, "queryTimeoutMechanism=qqrytimlmt")
	conn := &Conn{cfg: cfg, netConn: client, systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The server sends no reply to the RPB change, so syncQueryTimeoutRPB must
	// return without waiting for one.
	done := readOneRequestNoReply(t, server, func(id uint16, packet []byte) {
		if id != FunctionChangeRPB {
			t.Errorf("request id = 0x%x, want change RPB", id)
		}
		value, found, _ := readTestLLCPValue(packet[40:], codePointQueryTimeout)
		if !found {
			t.Errorf("0x3817 not found")
			return
		}
		if got := int32(readBE32(value)); got < 4 || got > 5 {
			t.Errorf("0x3817 = %d, want about 5 seconds", got)
		}
	})
	if err := conn.syncQueryTimeoutRPB(ctx, 0x0042); err != nil {
		t.Fatalf("syncQueryTimeoutRPB() error = %v", err)
	}
	<-done
}

func TestQueryTimeoutRPBNotSentForCancelOrWithoutDeadline(t *testing.T) {
	// netConn is nil: any request would fail, so a nil result means nothing was sent.
	cancelConn := &Conn{cfg: parseAttributeTestDSN(t, "naming=sql"), systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cancelConn.syncQueryTimeoutRPB(ctx, 1); err != nil {
		t.Fatalf("cancel mechanism sent a request: %v", err)
	}

	qConn := &Conn{cfg: parseAttributeTestDSN(t, "queryTimeoutMechanism=qqrytimlmt"), systemInfo: &SystemInfo{ServerVersion: clientInfoTestVRM}}
	if err := qConn.syncQueryTimeoutRPB(context.Background(), 1); err != nil {
		t.Fatalf("qqrytimlmt without deadline sent a request: %v", err)
	}
}

func TestChangeRPBQueryTimeoutRequestBytes(t *testing.T) {
	request := buildChangeRPBQueryTimeoutRequest(0x0042, -1)
	header, err := ParseHeader(request[4:])
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionChangeRPB {
		t.Fatalf("request id = 0x%x, want change RPB", header.ReqRepID)
	}
	value, found, err := readTestLLCPValue(request[40:], codePointQueryTimeout)
	if err != nil || !found {
		t.Fatalf("0x3817 found = %v, err = %v", found, err)
	}
	if !bytes.Equal(value, []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Fatalf("0x3817 = % x, want ff ff ff ff", value)
	}
}

func mustEBCDIC(t *testing.T, s string) []byte {
	t.Helper()
	b, err := EncodeEBCDIC37(s)
	if err != nil {
		t.Fatalf("EncodeEBCDIC37(%q) error = %v", s, err)
	}
	return b
}

func readBE32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

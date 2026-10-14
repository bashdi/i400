package i400

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// ucConfig parses a DSN for the loopback host with the given extra query.
func ucConfig(t *testing.T, query string) Config {
	t.Helper()
	cfg, err := ParseDSN(ucDSN(0, query))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	return *cfg
}

func TestUCSignonUserBytes(t *testing.T) {
	ebcdic10, err := EncodeEBCDIC37BlankPad("QSECOFR", 10)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		user    string
		level   int
		encrypt bool
		want    []byte
		wantErr bool
	}{
		{name: "level 0 is EBCDIC upper case", user: "qsecofr", want: ebcdic10},
		{name: "level 2 without encryption is EBCDIC", user: "qsecofr", level: 2, want: ebcdic10},
		{name: "level 2 for encryption is UTF-16BE padded to 20", user: "qsecofr", level: 2, encrypt: true, want: EncodeUTF16BEBlankPad("QSECOFR", 20)},
		{name: "level 4 for encryption is UTF-16BE", user: "ab", level: 4, encrypt: true, want: EncodeUTF16BEBlankPad("AB", 20)},
		{name: "ten characters is allowed", user: "ABCDEFGHIJ", want: ucMustEBCDIC(t, "ABCDEFGHIJ", 10)},
		{name: "eleven characters is rejected", user: "ABCDEFGHIJK", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := signonUserBytes(tt.user, tt.level, tt.encrypt)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("signonUserBytes() = %x, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("signonUserBytes() error = %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("signonUserBytes() = %x, want %x", got, tt.want)
			}
		})
	}
}

func ucMustEBCDIC(t *testing.T, text string, size int) []byte {
	t.Helper()
	b, err := EncodeEBCDIC37BlankPad(text, size)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUCSignonErrorCodes(t *testing.T) {
	tests := []struct {
		rc   int32
		want string
	}{
		{0x20001, "user ID is not known"},
		{0x3000B, "password may be incorrect"},
		{0x3000C, "will be revoked"},
		{0x3000D, "password is expired"},
		{0x12345, "0x12345"},
	}
	for _, tt := range tests {
		err := signonError(tt.rc)
		if !errors.Is(err, ErrAuthentication) {
			t.Errorf("signonError(0x%x) = %v, want ErrAuthentication", tt.rc, err)
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("signonError(0x%x) = %v, want message containing %q", tt.rc, err, tt.want)
		}
	}
}

func TestUCSignonExchangeParsesReply(t *testing.T) {
	h, conn := ucDialTo(t, ucSignonReply(ucDefaultSignon(2)))
	info, clientSeed, serverSeed, err := signonExchange(conn, "host.example")
	if err != nil {
		t.Fatalf("signonExchange() error = %v", err)
	}
	if info.System != "host.example" || info.ServerVersion != 0x070200 || info.ServerLevel != 5 || info.PasswordLevel != 2 {
		t.Errorf("info = %+v, want VRM 0x070200, DS level 5, password level 2", info)
	}
	if !bytes.Equal(serverSeed, ucServerSeed) {
		t.Errorf("serverSeed = %x, want %x", serverSeed, ucServerSeed)
	}
	req := h.waitFor(RequestExchangeAttributes)
	sent, found, err := readTestLLCPValue(req.Raw[20:], CodePointClientSeed)
	if err != nil || !found || !bytes.Equal(sent, clientSeed) {
		t.Errorf("client seed returned %x does not match the one sent %x (found=%v err=%v)", clientSeed, sent, found, err)
	}
}

func TestUCSignonExchangeErrors(t *testing.T) {
	t.Run("return code", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(SignonServerID, ReplyExchangeAttributes, 0x20001, nil)
		})
		if _, _, _, err := signonExchange(conn, "host"); !errors.Is(err, ErrAuthentication) {
			t.Fatalf("signonExchange() error = %v, want ErrAuthentication", err)
		}
	})
	t.Run("missing server seed", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(SignonServerID, ReplyExchangeAttributes, 0, ucItems(
				ucItem{CodePointClientVersion, ucU32(0x070200)},
			))
		})
		_, _, _, err := signonExchange(conn, "host")
		if err == nil || !strings.Contains(err.Error(), "missing server seed") {
			t.Fatalf("signonExchange() error = %v, want missing server seed", err)
		}
	})
}

func TestUCSignonAuthenticateReturnCodes(t *testing.T) {
	clientSeed := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	t.Run("password expired", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(SignonServerID, ReplySignonInfo, 0x3000D, nil)
		})
		info := &SystemInfo{PasswordLevel: 0}
		err := signonAuthenticate(conn, info, "QSECOFR", "Secret1", clientSeed, ucServerSeed)
		if !errors.Is(err, ErrAuthentication) || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("signonAuthenticate() error = %v, want expired ErrAuthentication", err)
		}
	})
	t.Run("server CCSID is recorded", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(SignonServerID, ReplySignonInfo, 0, ucItems(
				ucItem{CodePointServerCCSID, ucU32(273)},
			))
		})
		info := &SystemInfo{PasswordLevel: 0}
		if err := signonAuthenticate(conn, info, "QSECOFR", "Secret1", clientSeed, ucServerSeed); err != nil {
			t.Fatalf("signonAuthenticate() error = %v", err)
		}
		if info.ServerCCSID != 273 {
			t.Errorf("ServerCCSID = %d, want 273", info.ServerCCSID)
		}
	})
	t.Run("bad password is rejected before sending", func(t *testing.T) {
		// A nil connection proves nothing is written: the substitute fails first.
		info := &SystemInfo{PasswordLevel: 4}
		if err := signonAuthenticate(nil, info, "QSECOFR", "*star", clientSeed, ucServerSeed); err == nil {
			t.Fatal("signonAuthenticate() error = nil for a password starting with '*'")
		}
	})
}

func TestUCDatabaseExchangeSeeds(t *testing.T) {
	clientSeed := []byte{9, 8, 7, 6, 5, 4, 3, 2}
	t.Run("returns server seed", func(t *testing.T) {
		h, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(DatabaseServerID, ReplyExchangeRandomSeeds, 0, ucServerSeed)
		})
		got, err := databaseExchangeSeeds(conn, clientSeed)
		if err != nil {
			t.Fatalf("databaseExchangeSeeds() error = %v", err)
		}
		if !bytes.Equal(got, ucServerSeed) {
			t.Errorf("server seed = %x, want %x", got, ucServerSeed)
		}
		req := h.waitFor(RequestExchangeRandomSeeds)
		if len(req.Raw) != 28 || !bytes.Equal(req.Raw[20:28], clientSeed) {
			t.Errorf("request = %x, want client seed %x at offset 20", req.Raw, clientSeed)
		}
	})
	t.Run("short reply", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(DatabaseServerID, ReplyExchangeRandomSeeds, 0, []byte{1, 2, 3, 4})
		})
		if _, err := databaseExchangeSeeds(conn, clientSeed); err == nil || !strings.Contains(err.Error(), "too short") {
			t.Fatalf("databaseExchangeSeeds() error = %v, want too short", err)
		}
	})
	t.Run("return code", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(DatabaseServerID, ReplyExchangeRandomSeeds, 0x1234, ucServerSeed)
		})
		if _, err := databaseExchangeSeeds(conn, clientSeed); err == nil || !strings.Contains(err.Error(), "return code 0x1234") {
			t.Fatalf("databaseExchangeSeeds() error = %v, want return code 0x1234", err)
		}
	})
}

func TestUCDatabaseStartServer(t *testing.T) {
	clientSeed := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	t.Run("job name is recorded", func(t *testing.T) {
		_, conn := ucDialTo(t, ucDBReply(ucDefaultDB()))
		info := &SystemInfo{PasswordLevel: 0}
		if err := databaseStartServer(conn, info, "qsecofr", "Secret1", clientSeed, ucServerSeed); err != nil {
			t.Fatalf("databaseStartServer() error = %v", err)
		}
		if info.JobName != ucJobName {
			t.Errorf("JobName = %q, want %q", info.JobName, ucJobName)
		}
	})
	t.Run("return code", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return ucSimpleReply(DatabaseServerID, ReplyStartServer, 0x2, nil)
		})
		info := &SystemInfo{PasswordLevel: 0}
		err := databaseStartServer(conn, info, "qsecofr", "Secret1", clientSeed, ucServerSeed)
		if err == nil || !strings.Contains(err.Error(), "return code 0x2") {
			t.Fatalf("databaseStartServer() error = %v, want return code 0x2", err)
		}
		if info.JobName != "" {
			t.Errorf("JobName = %q after failure, want unchanged", info.JobName)
		}
	})
}

func TestUCDatabaseSetServerAttributes(t *testing.T) {
	cfg := ucConfig(t, "")
	t.Run("parses functional level and job identifier", func(t *testing.T) {
		h, conn := ucDialTo(t, ucDBReply(ucDefaultDB()))
		info := &SystemInfo{ServerVersion: 0x070200, JobName: "preset"}
		jobName, err := databaseSetServerAttributes(conn, cfg, info, true)
		if err != nil {
			t.Fatalf("databaseSetServerAttributes() error = %v", err)
		}
		if jobName != "preset" {
			t.Errorf("returned job name = %q, want the one already on info", jobName)
		}
		if info.ServerFunctionalLevel != 5 {
			t.Errorf("ServerFunctionalLevel = %d, want 5", info.ServerFunctionalLevel)
		}
		if got := strings.TrimRight(info.ServerJobIdentifier, " "); got != strings.TrimRight(ucJobID, " ") {
			t.Errorf("ServerJobIdentifier = %q, want %q", got, ucJobID)
		}
		h.waitFor(FunctionSetAttributes)
	})
	t.Run("error envelope", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte {
			return buildTestReplyPacketWithRC(nil, 1, -1)
		})
		_, err := databaseSetServerAttributes(conn, cfg, &SystemInfo{}, true)
		if err == nil || !strings.Contains(err.Error(), "class=1") {
			t.Fatalf("databaseSetServerAttributes() error = %v, want class=1 failure", err)
		}
	})
}

func TestUCParseServerAttributesPayload(t *testing.T) {
	tests := []struct {
		name      string
		info      *SystemInfo
		payload   []byte
		wantErr   string
		wantLevel int
		wantJob   string
	}{
		{name: "nil info is ignored", info: nil, payload: []byte{0, 0, 0, 3, 0, 0}},
		{name: "empty payload", info: &SystemInfo{}, payload: nil},
		{name: "unrelated code point", info: &SystemInfo{}, payload: AppendLLCP(nil, 0x1234, []byte{1, 2})},
		{name: "short block", info: &SystemInfo{}, payload: AppendLLCP(nil, CodePointServerAttributes, make([]byte, 100)), wantErr: "too short"},
		{name: "malformed LLCP", info: &SystemInfo{}, payload: []byte{0, 0, 0, 3, 0, 0}, wantErr: "invalid llcp length"},
		{name: "functional level 7", info: &SystemInfo{}, payload: AppendLLCP(nil, CodePointServerAttributes, ucAttributesBlock("V7R2M007", ucJobID)), wantLevel: 7, wantJob: strings.TrimRight(ucJobID, " ")},
		{name: "functional level too short", info: &SystemInfo{}, payload: AppendLLCP(nil, CodePointServerAttributes, ucAttributesBlock("V7R2M0", "")), wantLevel: 0},
		{name: "functional level not numeric", info: &SystemInfo{}, payload: AppendLLCP(nil, CodePointServerAttributes, ucAttributesBlock("V7R2MXX", "")), wantLevel: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseServerAttributesPayload(tt.payload, tt.info)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseServerAttributesPayload() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseServerAttributesPayload() error = %v", err)
			}
			if tt.info == nil {
				return
			}
			if tt.info.ServerFunctionalLevel != tt.wantLevel {
				t.Errorf("ServerFunctionalLevel = %d, want %d", tt.info.ServerFunctionalLevel, tt.wantLevel)
			}
			if got := strings.TrimRight(tt.info.ServerJobIdentifier, " "); got != tt.wantJob {
				t.Errorf("ServerJobIdentifier = %q, want %q", got, tt.wantJob)
			}
		})
	}
}

func TestUCDatabaseAddLibraryList(t *testing.T) {
	t.Run("no libraries sends nothing", func(t *testing.T) {
		// A nil connection proves nothing is written when the list is empty.
		if err := databaseAddLibraryList(nil, ucConfig(t, "")); err != nil {
			t.Fatalf("databaseAddLibraryList() error = %v", err)
		}
	})
	t.Run("success", func(t *testing.T) {
		h, conn := ucDialTo(t, func(rec ucRecord) []byte { return buildTestReplyPacket(nil) })
		if err := databaseAddLibraryList(conn, ucConfig(t, "libraries=LIB1")); err != nil {
			t.Fatalf("databaseAddLibraryList() error = %v", err)
		}
		if req := h.waitFor(FunctionAddLibraryList); req.ServerID != NativeDatabaseServerID {
			t.Errorf("ServerID = 0x%x, want 0x%x", req.ServerID, NativeDatabaseServerID)
		}
	})
	t.Run("error envelope", func(t *testing.T) {
		_, conn := ucDialTo(t, func(rec ucRecord) []byte { return buildTestReplyPacketWithRC(nil, 1, -1) })
		err := databaseAddLibraryList(conn, ucConfig(t, "libraries=LIB1"))
		if err == nil || !strings.Contains(err.Error(), "add library list failed") {
			t.Fatalf("databaseAddLibraryList() error = %v, want add library list failure", err)
		}
	})
}

func TestUCConnectDatabaseRejectsInvalidPort(t *testing.T) {
	cfg := ucConfig(t, "")
	cfg.Port = -1
	_, _, err := connectDatabase(context.Background(), cfg, &SystemInfo{}, "qsecofr", "Secret1")
	if !errors.Is(err, ErrConnection) || !strings.Contains(err.Error(), "invalid database port") {
		t.Fatalf("connectDatabase() error = %v, want invalid database port", err)
	}
}

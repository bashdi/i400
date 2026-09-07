package i400

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestBuildSetServerAttributesRequestUsesConfig(t *testing.T) {
	cfg := Config{
		Naming:            NamingSystem,
		Schema:            "qgpl",
		DateFormat:        defaultDateFormat,
		DateSeparator:     defaultDateSeparator,
		TimeFormat:        defaultTimeFormat,
		TimeSeparator:     defaultTimeSeparator,
		DecimalSeparator:  defaultDecimalSeparator,
		CommitmentControl: 4,
	}

	request, err := buildSetServerAttributesRequest(cfg, true, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionSetAttributes {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionSetAttributes)
	}
	if got := requestBodyShortValue(t, request, CodePointNamingConvention); got != 1 {
		t.Fatalf("naming convention = %d, want 1", got)
	}
	if got := requestBodyShortValue(t, request, CodePointCommitmentControlLevel); got != 0 {
		t.Fatalf("commitment control = %d, want 0", got)
	}
	payload, found, err := readTestLLCPValue(request[40:], CodePointDefaultSQLLibrary)
	if err != nil {
		t.Fatalf("read default SQL library attribute: %v", err)
	}
	if !found {
		t.Fatal("default SQL library attribute not found")
	}
	if len(payload) < 4 {
		t.Fatalf("default SQL library payload length = %d, want >= 4", len(payload))
	}
	if got := binary.BigEndian.Uint16(payload[0:2]); got != 37 {
		t.Fatalf("default SQL library CCSID = %d, want 37", got)
	}
	text, err := DecodeEBCDIC37(payload[4:])
	if err != nil {
		t.Fatalf("DecodeEBCDIC37() error = %v", err)
	}
	if text != "QGPL" {
		t.Fatalf("default SQL library = %q, want %q", text, "QGPL")
	}
}

func TestBuildAddLibraryListRequestPrependsSchema(t *testing.T) {
	cfg := Config{Schema: "qgpl", Libraries: []string{"lib1", "lib2"}}

	request, ok, err := buildAddLibraryListRequest(cfg)
	if err != nil {
		t.Fatalf("buildAddLibraryListRequest() error = %v", err)
	}
	if !ok {
		t.Fatal("buildAddLibraryListRequest() returned ok=false")
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ServerID != NativeDatabaseServerID {
		t.Fatalf("ServerID = 0x%x, want 0x%x", header.ServerID, NativeDatabaseServerID)
	}
	if header.ReqRepID != FunctionAddLibraryList {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionAddLibraryList)
	}
	payload, found, err := readTestLLCPValue(request[40:], CodePointListOfLibraries)
	if err != nil {
		t.Fatalf("read list of libraries attribute: %v", err)
	}
	if !found {
		t.Fatal("list of libraries attribute not found")
	}
	entries, err := parseTestLibraryListEntries(payload)
	if err != nil {
		t.Fatalf("parseTestLibraryListEntries() error = %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].Indicator != 'C' || entries[0].Name != "QGPL" {
		t.Fatalf("entry[0] = %+v, want C/QGPL", entries[0])
	}
	if entries[1].Indicator != 'L' || entries[1].Name != "LIB1" {
		t.Fatalf("entry[1] = %+v, want L/LIB1", entries[1])
	}
	if entries[2].Indicator != 'L' || entries[2].Name != "LIB2" {
		t.Fatalf("entry[2] = %+v, want L/LIB2", entries[2])
	}
}

func TestResetSessionRestoresDefaults(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	cfg := Config{
		Naming:            NamingSystem,
		Schema:            "QGPL",
		Libraries:         []string{"LIB1", "LIB2"},
		DateFormat:        defaultDateFormat,
		DateSeparator:     defaultDateSeparator,
		TimeFormat:        defaultTimeFormat,
		TimeSeparator:     defaultTimeSeparator,
		DecimalSeparator:  defaultDecimalSeparator,
		CommitmentControl: defaultCommitmentControl,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		packet, err := readPacket(server)
		if err != nil {
			t.Errorf("read rollback request: %v", err)
			return
		}
		header, err := ParseHeader(packet)
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
			t.Errorf("read set attributes request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse set attributes header: %v", err)
			return
		}
		if header.ReqRepID != FunctionSetAttributes {
			t.Errorf("set attributes ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionSetAttributes)
			return
		}
		if got := requestBodyAutoCommitFlag(t, packet); got != 0xE8 {
			t.Errorf("set attributes autocommit indicator = 0x%x, want 0xE8", got)
			return
		}
		if got := requestBodyShortValue(t, packet, CodePointCommitmentControlLevel); got != 0 {
			t.Errorf("set attributes commitment control = %d, want 0", got)
			return
		}
		if got := requestBodyShortValue(t, packet, CodePointNamingConvention); got != 1 {
			t.Errorf("set attributes naming convention = %d, want 1", got)
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write set attributes reply: %v", err)
			return
		}

		packet, err = readPacket(server)
		if err != nil {
			t.Errorf("read add library list request: %v", err)
			return
		}
		header, err = ParseHeader(packet)
		if err != nil {
			t.Errorf("parse add library list header: %v", err)
			return
		}
		if header.ServerID != NativeDatabaseServerID {
			t.Errorf("add library list ServerID = 0x%x, want 0x%x", header.ServerID, NativeDatabaseServerID)
			return
		}
		if header.ReqRepID != FunctionAddLibraryList {
			t.Errorf("add library list ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionAddLibraryList)
			return
		}
		payload, found, err := readTestLLCPValue(packet[40:], CodePointListOfLibraries)
		if err != nil {
			t.Errorf("read add library list attribute: %v", err)
			return
		}
		if !found {
			t.Errorf("add library list attribute not found")
			return
		}
		entries, err := parseTestLibraryListEntries(payload)
		if err != nil {
			t.Errorf("parse library list entries: %v", err)
			return
		}
		if len(entries) != 3 {
			t.Errorf("len(entries) = %d, want 3", len(entries))
			return
		}
		if _, err := server.Write(buildTestReplyPacket(nil)); err != nil {
			t.Errorf("write add library list reply: %v", err)
			return
		}
	}()

	conn := &Conn{
		cfg:                      cfg,
		netConn:                  client,
		autoCommit:               false,
		currentCommitmentControl: 4,
		lastWarning:              &SQLWarning{Message: "warn"},
	}
	if err := conn.ResetSession(context.Background()); err != nil {
		t.Fatalf("ResetSession() error = %v", err)
	}
	if !conn.autoCommit {
		t.Fatal("ResetSession() left autocommit disabled")
	}
	if conn.currentCommitmentMode() != defaultCommitmentControl {
		t.Fatalf("current commitment control = %d, want %d", conn.currentCommitmentMode(), defaultCommitmentControl)
	}
	if conn.Warnings() != nil {
		t.Fatal("ResetSession() did not clear warnings")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for reset session test server")
	}
}

type testLibraryListEntry struct {
	Indicator byte
	Name      string
}

func parseTestLibraryListEntries(payload []byte) ([]testLibraryListEntry, error) {
	if len(payload) < 4 {
		return nil, nil
	}
	count := int(binary.BigEndian.Uint16(payload[2:4]))
	offset := 4
	entries := make([]testLibraryListEntry, 0, count)
	for i := 0; i < count; i++ {
		if offset+3 > len(payload) {
			return nil, context.DeadlineExceeded
		}
		indicator, err := DecodeEBCDIC37(payload[offset : offset+1])
		if err != nil {
			return nil, err
		}
		nameLength := int(binary.BigEndian.Uint16(payload[offset+1 : offset+3]))
		offset += 3
		if offset+nameLength > len(payload) {
			return nil, context.DeadlineExceeded
		}
		name, err := DecodeEBCDIC37(payload[offset : offset+nameLength])
		if err != nil {
			return nil, err
		}
		offset += nameLength
		entries = append(entries, testLibraryListEntry{Indicator: indicator[0], Name: name})
	}
	return entries, nil
}

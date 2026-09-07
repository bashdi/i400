package i400

import (
	"encoding/binary"
	"testing"
)

func TestParseServerAttributesBlockExtractsJobIdentifier(t *testing.T) {
	data := make([]byte, 114)
	functionalLevel, err := EncodeEBCDIC37("V7R2M05   ")
	if err != nil {
		t.Fatalf("EncodeEBCDIC37() error = %v", err)
	}
	copy(data[50:60], functionalLevel)
	jobIdentifier, err := EncodeEBCDIC37("QSQSRVR   123456USER      ")
	if err != nil {
		t.Fatalf("EncodeEBCDIC37() error = %v", err)
	}
	copy(data[88:114], jobIdentifier)

	info := &SystemInfo{}
	if err := parseServerAttributesBlock(data, info); err != nil {
		t.Fatalf("parseServerAttributesBlock() error = %v", err)
	}
	if info.ServerFunctionalLevel != 5 {
		t.Fatalf("ServerFunctionalLevel = %d, want 5", info.ServerFunctionalLevel)
	}
	if info.ServerJobIdentifier != "QSQSRVR   123456USER      " {
		t.Fatalf("ServerJobIdentifier = %q, want %q", info.ServerJobIdentifier, "QSQSRVR   123456USER      ")
	}
}

func TestBuildCancelRequestIncludesJobIdentifier(t *testing.T) {
	request, err := buildCancelRequest(0x1234, "QSQSRVR   123456USER      ")
	if err != nil {
		t.Fatalf("buildCancelRequest() error = %v", err)
	}
	header, err := ParseHeader(request)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if header.ReqRepID != FunctionCancel {
		t.Fatalf("ReqRepID = 0x%x, want 0x%x", header.ReqRepID, FunctionCancel)
	}
	if got := binary.BigEndian.Uint16(request[34:36]); got != 0x1234 {
		t.Fatalf("rpb handle = 0x%x, want 0x1234", got)
	}
	payload, found, err := readTestLLCPValue(request[40:], CodePointJobIdentifier)
	if err != nil {
		t.Fatalf("readTestLLCPValue() error = %v", err)
	}
	if !found {
		t.Fatal("cancel request missing job identifier")
	}
	if len(payload) != 30 {
		t.Fatalf("job identifier payload length = %d, want 30", len(payload))
	}
	decoded, err := DecodeEBCDIC37(payload[4:])
	if err != nil {
		t.Fatalf("DecodeEBCDIC37() error = %v", err)
	}
	if got := decoded; got != "QSQSRVR   123456USER      " {
		t.Fatalf("job identifier = %q, want %q", got, "QSQSRVR   123456USER      ")
	}
}

package i400

import "testing"

func TestHeaderMarshalBinary(t *testing.T) {
	head := Header{
		Length:         20,
		HeaderID:       0,
		ServerID:       DatabaseServerID,
		CSInstance:     1,
		CorrelationID:  2,
		TemplateLength: 20,
		ReqRepID:       FunctionExecuteImmediate,
	}

	data, err := head.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	parsed, err := ParseHeader(data)
	if err != nil {
		t.Fatalf("ParseHeader() error = %v", err)
	}
	if parsed.ServerID != DatabaseServerID || parsed.ReqRepID != FunctionExecuteImmediate {
		t.Fatalf("parsed header mismatch: %+v", parsed)
	}
}

func TestAppendLLCP(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	data := AppendLLCP(nil, CodePointPassword, payload)
	if len(data) != 9 {
		t.Fatalf("len = %d, want %d", len(data), 9)
	}
	codePoint, gotPayload, rest, err := ParseLLCP(data)
	if err != nil {
		t.Fatalf("ParseLLCP() error = %v", err)
	}
	if codePoint != CodePointPassword {
		t.Fatalf("codePoint = %#x, want %#x", codePoint, CodePointPassword)
	}
	if len(rest) != 0 {
		t.Fatalf("rest length = %d, want 0", len(rest))
	}
	if string(gotPayload) != string(payload) {
		t.Fatalf("payload mismatch")
	}
}

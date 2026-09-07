package i400

import "testing"

func TestReadSimpleReplyForID(t *testing.T) {
	packet := buildHeader(24, 0, SignonServerID, 0, ReplyExchangeAttributes)
	packet = appendU32(packet, 0)

	rc, payload, err := readSimpleReplyForID(packet, ReplyExchangeAttributes)
	if err != nil {
		t.Fatalf("readSimpleReplyForID() error = %v", err)
	}
	if rc != 0 {
		t.Fatalf("return code = %d, want 0", rc)
	}
	if len(payload) != 0 {
		t.Fatalf("payload length = %d, want 0", len(payload))
	}
}

func TestReadSimpleReplyForIDUnexpectedReply(t *testing.T) {
	packet := buildHeader(24, 0, SignonServerID, 0, ReplyExchangeAttributes)
	packet = appendU32(packet, 0)

	if _, _, err := readSimpleReplyForID(packet, ReplySignonInfo); err == nil {
		t.Fatal("readSimpleReplyForID() error = nil, want unexpected reply id")
	}
}

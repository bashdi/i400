package i400

import (
	"errors"
	"testing"
)

func TestUTF16BERoundTrip(t *testing.T) {
	const input = "Hello, IBM i"
	encoded := EncodeUTF16BE(input)
	decoded, err := DecodeUTF16BE(encoded)
	if err != nil {
		t.Fatalf("DecodeUTF16BE() error = %v", err)
	}
	if decoded != input {
		t.Fatalf("round trip = %q, want %q", decoded, input)
	}
}

func TestEncodeTextBytesUnsupportedCCSID(t *testing.T) {
	if _, err := encodeTextBytes("Hello", 5000); err == nil {
		t.Fatal("encodeTextBytes() error = nil, want unsupported CCSID error")
	} else if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("encodeTextBytes() error = %v, want ErrUnsupported", err)
	}
}

func TestDecodeTextByCCSIDUnsupportedCCSID(t *testing.T) {
	if _, err := decodeTextByCCSID([]byte{0x81}, 5000); err == nil {
		t.Fatal("decodeTextByCCSID() error = nil, want unsupported CCSID error")
	} else if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("decodeTextByCCSID() error = %v, want ErrUnsupported", err)
	}
}

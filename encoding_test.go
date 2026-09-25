package i400

import (
	"errors"
	"fmt"
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

func TestTextCCSIDRoundTrips(t *testing.T) {
	ccsids := []int{37, 273, 277, 278, 280, 284, 285, 297, 437, 500, 819, 850, 858, 912, 930, 933, 935, 937, 939, 1026, 1141, 1200, 1208, 1252, 13488, 65535}
	for _, ccsid := range ccsids {
		t.Run(fmt.Sprint(ccsid), func(t *testing.T) {
			const input = "Hello IBM i"
			encoded, err := encodeTextBytes(input, ccsid)
			if err != nil {
				t.Fatalf("encodeTextBytes() error = %v", err)
			}
			decoded, err := decodeTextByCCSID(encoded, ccsid)
			if err != nil {
				t.Fatalf("decodeTextByCCSID() error = %v", err)
			}
			if decoded != input {
				t.Fatalf("round trip = %q, want %q", decoded, input)
			}
		})
	}
}

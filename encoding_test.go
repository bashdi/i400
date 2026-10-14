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

func TestTextCCSIDRoundTrip(t *testing.T) {
	cases := []struct {
		ccsid int
		text  string
	}{
		{37, "Hello, IBM i"},
		{273, "Grüße [ÄÖÜ]"},
		{1141, "Preis: 10 €"},
		{500, "Bonjour à tous"},
		{870, "Zażółć gęślą jaźń"},
		{1025, "Привет"},
		{819, "café"},
		{1252, "€ café"},
		{930, "ABC 日本語 ｶﾀｶﾅ"},
		{1399, "東京 abc"},
		{935, "中文 abc"},
		{937, "繁體 abc"},
		{933, "한국어 abc"},
		{835, "繁體"},
		{1200, "Unicode 𝄞"},
		{1202, "little endian"},
		{1208, "UTF-8 ✓"},
	}
	for _, tc := range cases {
		encoded, err := encodeTextBytes(tc.text, tc.ccsid)
		if err != nil {
			t.Fatalf("encodeTextBytes(%d) error = %v", tc.ccsid, err)
		}
		decoded, err := decodeTextByCCSID(encoded, tc.ccsid)
		if err != nil {
			t.Fatalf("decodeTextByCCSID(%d) error = %v", tc.ccsid, err)
		}
		if decoded != tc.text {
			t.Errorf("CCSID %d round trip = %q, want %q", tc.ccsid, decoded, tc.text)
		}
	}
}

func TestFitTextBytesToLengthUsesCCSIDBlank(t *testing.T) {
	cases := []struct {
		ccsid  int
		length int
		want   []byte
	}{
		{37, 3, []byte{0xC1, 0x40, 0x40}},
		{819, 3, []byte{0x41, 0x20, 0x20}},
		{1200, 4, []byte{0x00, 0x41, 0x00, 0x20}},
		{835, 4, []byte{0x42, 0xC1, 0x40, 0x40}},
	}
	for _, tc := range cases {
		raw, err := encodeTextBytes("A", tc.ccsid)
		if tc.ccsid == 835 {
			raw, err = encodeTextBytes("Ａ", tc.ccsid)
		}
		if err != nil {
			t.Fatal(err)
		}
		got := fitTextBytesToLength(raw, tc.length, tc.ccsid)
		if string(got) != string(tc.want) {
			t.Errorf("CCSID %d: got % x, want % x", tc.ccsid, got, tc.want)
		}
	}
}

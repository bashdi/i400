package i400

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bashdi/i400/internal/ccsid"
)

// statementTextAttr parses a 0x3831 extended statement text attribute and
// returns its CCSID tag and data.
func statementTextAttr(t *testing.T, attr []byte) (uint16, uint16, []byte) {
	t.Helper()
	if len(attr) < 12 {
		t.Fatalf("attribute too short: % x", attr)
	}
	length := binary.BigEndian.Uint32(attr[0:4])
	if int(length) != len(attr) {
		t.Fatalf("attribute length = %d, want %d", length, len(attr))
	}
	cp := binary.BigEndian.Uint16(attr[4:6])
	tag := binary.BigEndian.Uint16(attr[6:8])
	dataLen := binary.BigEndian.Uint32(attr[8:12])
	if int(dataLen) != len(attr)-12 {
		t.Fatalf("data length = %d, want %d", dataLen, len(attr)-12)
	}
	return cp, tag, attr[12:]
}

func TestStatementTextCCSIDFor(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		serverCCSID int
		serverVRM   int
		want        int
	}{
		{"default", Config{PackageCCSID: defaultPackageCCSID}, 0, 0, 13488},
		{"zero means default", Config{}, 0, 0, 13488},
		{"1200", Config{PackageCCSID: 1200}, 0, 0x070100, 1200},
		{"1200 on V5R2 falls back", Config{PackageCCSID: 1200}, 0, 0x050200, 13488},
		{"1200 on V5R3 keeps 1200", Config{PackageCCSID: 1200}, 0, 0x050300, 1200},
		{"explicit EBCDIC", Config{PackageCCSID: 37}, 273, 0, 37},
		{"system uses job CCSID", Config{PackageCCSIDSystem: true}, 273, 0, 273},
		{"system unknown job CCSID", Config{PackageCCSIDSystem: true}, 0, 0, 37},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statementTextCCSIDFor(tt.cfg, tt.serverCCSID, tt.serverVRM); got != tt.want {
				t.Fatalf("statementTextCCSIDFor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConnStatementTextCCSID(t *testing.T) {
	var nilConn *Conn
	if got := nilConn.statementTextCCSID(); got != defaultPackageCCSID {
		t.Fatalf("nil conn = %d, want %d", got, defaultPackageCCSID)
	}
	c := &Conn{cfg: Config{PackageCCSIDSystem: true}, systemInfo: &SystemInfo{ServerCCSID: 273}}
	if got := c.statementTextCCSID(); got != 273 {
		t.Fatalf("system conn = %d, want 273", got)
	}
	c = &Conn{cfg: Config{PackageCCSID: 1200}, systemInfo: &SystemInfo{ServerVersion: 0x050200}}
	if got := c.statementTextCCSID(); got != 13488 {
		t.Fatalf("1200 on V5R2 conn = %d, want 13488", got)
	}
}

func TestStatementTextAttributeByMode(t *testing.T) {
	const query = "SELECT 1"
	utf16 := []byte{0x00, 'S', 0x00, 'E', 0x00, 'L', 0x00, 'E', 0x00, 'C', 0x00, 'T', 0x00, ' ', 0x00, '1'}
	ebcdic273 := []byte{0xE2, 0xC5, 0xD3, 0xC5, 0xC3, 0xE3, 0x40, 0xF1}

	tests := []struct {
		name     string
		ccsid    int
		wantTag  uint16
		wantData []byte
	}{
		{"13488 UTF-16", 13488, 13488, utf16},
		{"1200 UTF-16", 1200, 1200, utf16},
		{"273 EBCDIC", 273, 273, ebcdic273},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attr, err := appendExtendedSQLTextAttribute(query, tt.ccsid)
			if err != nil {
				t.Fatalf("appendExtendedSQLTextAttribute() error = %v", err)
			}
			cp, tag, data := statementTextAttr(t, attr)
			if cp != 0x3831 {
				t.Fatalf("code point = %#x, want 0x3831", cp)
			}
			if tag != tt.wantTag {
				t.Fatalf("CCSID tag = %d, want %d", tag, tt.wantTag)
			}
			if !bytes.Equal(data, tt.wantData) {
				t.Fatalf("data = % x, want % x", data, tt.wantData)
			}
		})
	}
}

func TestStatementTextMixedCCSIDRoundTrip(t *testing.T) {
	// Mixed SBCS/DBCS (shift-out/shift-in) text in CCSID 930.
	const query = "SELECT 'A', 'あ' FROM T"
	attr, err := appendExtendedSQLTextAttribute(query, 930)
	if err != nil {
		t.Fatalf("appendExtendedSQLTextAttribute() error = %v", err)
	}
	_, tag, data := statementTextAttr(t, attr)
	if tag != 930 {
		t.Fatalf("CCSID tag = %d, want 930", tag)
	}
	if !bytes.Contains(data, []byte{0x0E}) || !bytes.Contains(data, []byte{0x0F}) {
		t.Fatalf("data % x lacks shift-out/shift-in codes", data)
	}
	got, err := ccsid.Decode(data, 930)
	if err != nil {
		t.Fatalf("ccsid.Decode() error = %v", err)
	}
	if got != query {
		t.Fatalf("round trip = %q, want %q", got, query)
	}
}

func TestStatementTextUnsupportedCCSID(t *testing.T) {
	if _, err := appendExtendedSQLTextAttribute("SELECT 1", 99999); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestExecuteImmediateRequestUsesStatementCCSID(t *testing.T) {
	const query = "SELECT 1"
	ebcdicAttr, err := appendExtendedSQLTextAttribute(query, 273)
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildExecuteImmediateRequestCCSID(query, sqlStatementTypeSelect, nil, 273)
	if err != nil {
		t.Fatalf("buildExecuteImmediateRequestCCSID() error = %v", err)
	}
	if !bytes.Contains(request, ebcdicAttr) {
		t.Fatalf("request lacks 273 statement text attribute: % x", request)
	}

	defaultRequest, err := buildExecuteImmediateRequest(query, sqlStatementTypeSelect, nil)
	if err != nil {
		t.Fatal(err)
	}
	utf16Attr, err := appendExtendedSQLTextAttribute(query, defaultPackageCCSID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(defaultRequest, utf16Attr) {
		t.Fatalf("default request lacks 13488 statement text attribute")
	}
}

func TestPrepareRequestUsesStatementCCSID(t *testing.T) {
	const query = "SELECT 1"
	attr, err := appendExtendedSQLTextAttribute(query, 930)
	if err != nil {
		t.Fatal(err)
	}
	request, err := buildPrepareAndDescribeRequestCCSID(0x1234, "STMT0001", query, nil, 930)
	if err != nil {
		t.Fatalf("buildPrepareAndDescribeRequestCCSID() error = %v", err)
	}
	if !bytes.Contains(request, attr) {
		t.Fatalf("request lacks 930 statement text attribute: % x", request)
	}
}

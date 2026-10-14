package i400

import (
	"database/sql/driver"
	"testing"
	"unicode/utf16"

	"github.com/bashdi/i400/internal/bidi"
	"github.com/bashdi/i400/internal/ccsid"
)

func TestBidiDSNDefaults(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.BidiStringType != 5 {
		t.Errorf("default bidiStringType = %d, want 5 (JTOpen ST5)", cfg.BidiStringType)
	}
	if !cfg.BidiImplicitReordering {
		t.Error("default bidiImplicitReordering = false, want true")
	}
	if cfg.BidiNumericOrdering {
		t.Error("default bidiNumericOrdering = true, want false")
	}
}

func TestBidiDSNOptions(t *testing.T) {
	cases := []struct {
		dsn      string
		wantType int
		wantErr  bool
	}{
		{"as400://u:p@h/?bidiStringType=", -1, false},
		{"as400://u:p@h/?bidiStringType=-1", -1, false},
		{"as400://u:p@h/?bidiStringType=none", -1, false},
		{"as400://u:p@h/?bidiStringType=0", 0, false},
		{"as400://u:p@h/?bidiStringType=4", 4, false},
		{"as400://u:p@h/?bidiStringType=11", 11, false},
		{"as400://u:p@h/?bidiStringType=3", 0, true},
		{"as400://u:p@h/?bidiStringType=12", 0, true},
		{"as400://u:p@h/?bidiStringType=abc", 0, true},
	}
	for _, tc := range cases {
		cfg, err := ParseDSN(tc.dsn)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseDSN(%q) error = nil, want error", tc.dsn)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseDSN(%q) error = %v", tc.dsn, err)
			continue
		}
		if cfg.BidiStringType != tc.wantType {
			t.Errorf("ParseDSN(%q) bidiStringType = %d, want %d", tc.dsn, cfg.BidiStringType, tc.wantType)
		}
	}

	cfg, err := ParseDSN("as400://u:p@h/?bidiImplicitReordering=false&bidiNumericOrdering=true")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	if cfg.BidiImplicitReordering || !cfg.BidiNumericOrdering {
		t.Errorf("implicit=%t numeric=%t, want false/true", cfg.BidiImplicitReordering, cfg.BidiNumericOrdering)
	}
	if _, err := ParseDSN("as400://u:p@h/?bidiImplicitReordering=maybe"); err == nil {
		t.Error("ParseDSN with invalid bidiImplicitReordering returned no error")
	}
	if _, err := ParseDSN("as400://u:p@h/?bidiNumericOrdering=maybe"); err == nil {
		t.Error("ParseDSN with invalid bidiNumericOrdering returned no error")
	}
}

func TestBidiConfigValidate(t *testing.T) {
	if err := (Config{Host: "h", BidiStringType: 3}).Validate(); err == nil {
		t.Error("Validate with bidiStringType 3 returned no error")
	}
	for _, v := range []int{-1, 0, 4, 11} {
		if err := (Config{Host: "h", BidiStringType: v}).Validate(); err != nil {
			t.Errorf("Validate with bidiStringType %d: %v", v, err)
		}
	}
}

// TestBidiNotSetUsesColumnStringType checks JTOpen's "not set" semantics: the column's CCSID
// string type is used (SQLChar), and CCSIDs without a bidi table are not converted.
func TestBidiNotSetUsesColumnStringType(t *testing.T) {
	bs := bidiSettings{StringType: bidiStringTypeNotSet, ImplicitReordering: true}
	if got := bs.properties(420).StringType; got != bidi.StringTypeForCCSID(420) {
		t.Errorf("not set on CCSID 420: string type %d, want %d", got, bidi.StringTypeForCCSID(420))
	}
	if got := bs.properties(420).NoImplicitReordering; got {
		t.Error("implicit reordering on, but NoImplicitReordering is set")
	}
}

// TestBidiNonBidiCCSIDUnchanged checks that CCSIDs without a bidi table decode and encode as before,
// whatever the settings.
func TestBidiNonBidiCCSIDUnchanged(t *testing.T) {
	raw := []byte{0xC1, 0x40, 0x81, 0x99}
	bs := bidiSettings{StringType: 6, ImplicitReordering: true, NumericOrdering: true}
	plain, err := decodeTextByCCSID(raw, 37)
	if err != nil {
		t.Fatalf("decodeTextByCCSID: %v", err)
	}
	got, err := decodeTextWithBidi(raw, 37, bs)
	if err != nil || got != plain {
		t.Fatalf("decodeTextWithBidi(37) = %q, %v; want %q", got, err, plain)
	}
	wantBytes, err := encodeTextBytes(plain, 37)
	if err != nil {
		t.Fatalf("encodeTextBytes: %v", err)
	}
	gotBytes, err := encodeTextWithBidi(plain, 37, bs)
	if err != nil || string(gotBytes) != string(wantBytes) {
		t.Fatalf("encodeTextWithBidi(37) = % X, %v; want % X", gotBytes, err, wantBytes)
	}
}

// TestBidiColumnDecodeUsesSettings checks that a CHAR column of a bidi CCSID goes through the
// bidi layout with the connection's settings: the value equals the layout of bidi.ToLogicalPadded
// with the properties of the setting.
func TestBidiColumnDecodeUsesSettings(t *testing.T) {
	cfg, err := ParseDSN("as400://u:p@h/?bidiStringType=6&bidiImplicitReordering=false")
	if err != nil {
		t.Fatalf("ParseDSN: %v", err)
	}
	bs := bidiSettingsFromConfig(*cfg)
	raw := []byte{0xC1, 0xC2, 0x40, 0xF1, 0x42, 0xC3}
	col := columnMeta{Name: "C", Type: db2TypeChar, Length: len(raw), CCSID: 424}
	got, err := decodeColumnValueWithBidi(col, raw, defaultDateTimeSettings(), bs)
	if err != nil {
		t.Fatalf("decodeColumnValueWithBidi: %v", err)
	}
	units, err := ccsid.DecodeUnits(raw, 424)
	if err != nil {
		t.Fatalf("DecodeUnits: %v", err)
	}
	want := string(utf16.Decode(bidi.ToLogicalPadded(units, 424, bs.properties(424))))
	if got != want {
		t.Fatalf("decoded %q, want %q", got, want)
	}
}

func TestBidiStatementTextWiring(t *testing.T) {
	const query = "SELECT A, B FROM LIB.T WHERE C = 'x' AND D <> ?"

	noBidi := &Conn{cfg: Config{BidiStringType: bidiStringTypeNotSet, BidiImplicitReordering: true}}
	if got := noBidi.bidiStatementText(query); got != query {
		t.Errorf("not set: statement changed to %q", got)
	}

	c := &Conn{
		cfg:        Config{BidiStringType: 5, BidiImplicitReordering: true},
		systemInfo: &SystemInfo{ServerCCSID: 37, ServerVersion: 0x050400},
	}
	if got := c.bidiStatementText(query); got != query {
		t.Errorf("ASCII statement with default setting changed to %q", got)
	}

	// The layout must be the one of bidi.ConvertSQLToHost with the connection's values.
	hebrew := "SELECT 'אבג' FROM T"
	want := string(utf16.Decode(bidi.ConvertSQLToHost(utf16.Encode([]rune(hebrew)), 5, c.bidiPackageCCSID(), true,
		c.bidiSystemInfo())))
	if got := c.bidiStatementText(hebrew); got != want {
		t.Errorf("statement layout = %q, want %q", got, want)
	}
}

func TestBidiColumnNameWiring(t *testing.T) {
	name := "אב X"
	off := &Conn{cfg: Config{BidiStringType: 5, BidiImplicitReordering: false},
		systemInfo: &SystemInfo{ServerCCSID: 424, ServerVersion: 0x050400}}
	if got := off.bidiColumnName(name, 424); got != name {
		t.Errorf("implicit reordering off: name changed to %q", got)
	}
	on := &Conn{cfg: Config{BidiStringType: 5, BidiImplicitReordering: true},
		systemInfo: &SystemInfo{ServerCCSID: 424, ServerVersion: 0x050400}}
	want := string(utf16.Decode(bidi.ConvertDataFromHost(utf16.Encode([]rune(name)), 5, 424,
		on.bidiSystemInfo())))
	if got := on.bidiColumnName(name, 424); got != want {
		t.Errorf("column name = %q, want %q", got, want)
	}
}

func TestBidiParameterFieldDefault(t *testing.T) {
	// A field without bidi settings (for example built by a test) encodes with JTOpen's defaults.
	field := parameterMarkerField{SQLType: 448, Length: 0, CCSID: 37}
	if field.textBidi() != defaultBidiSettings() {
		t.Fatalf("textBidi() = %+v, want defaults", field.textBidi())
	}
	raw, err := encodeVarcharParameter(driver.Value("ABC"), field)
	if err != nil {
		t.Fatalf("encodeVarcharParameter: %v", err)
	}
	if len(raw) != 5 {
		t.Fatalf("encoded length %d, want 5 (2-byte prefix + 3)", len(raw))
	}
}

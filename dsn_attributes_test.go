package i400

import (
	"bytes"
	"errors"
	"testing"
)

func parseAttributeTestDSN(t *testing.T, query string) Config {
	t.Helper()
	cfg, err := ParseDSN("as400://user:pass@host/qgpl?" + query)
	if err != nil {
		t.Fatalf("ParseDSN(%q) error = %v", query, err)
	}
	return *cfg
}

func TestParseDSNServerAttributeDefaults(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql")
	if cfg.QueryOptimizeGoal != 0 {
		t.Fatalf("QueryOptimizeGoal = %d, want 0", cfg.QueryOptimizeGoal)
	}
	if cfg.DecimalMaxPrecision != 31 {
		t.Fatalf("DecimalMaxPrecision = %d, want 31", cfg.DecimalMaxPrecision)
	}
	if cfg.DecimalMaxScale != 31 {
		t.Fatalf("DecimalMaxScale = %d, want 31", cfg.DecimalMaxScale)
	}
	if cfg.MinimumDivideScale != 0 {
		t.Fatalf("MinimumDivideScale = %d, want 0", cfg.MinimumDivideScale)
	}
	if cfg.LOBThreshold != 1024*1024 {
		t.Fatalf("LOBThreshold = %d, want %d", cfg.LOBThreshold, 1024*1024)
	}
	if cfg.BlockSize != 256 {
		t.Fatalf("BlockSize = %d, want 256", cfg.BlockSize)
	}
	if cfg.TrueAutocommit {
		t.Fatalf("TrueAutocommit = true, want false")
	}
	if cfg.DecfloatRoundingMode != 0 {
		t.Fatalf("DecfloatRoundingMode = %d, want 0", cfg.DecfloatRoundingMode)
	}
	if got := cfg.fetchBufferBytes(); got != 256*1024 {
		t.Fatalf("fetchBufferBytes() = %d, want %d", got, 256*1024)
	}
}

func TestParseDSNServerAttributeValidValues(t *testing.T) {
	tests := []struct {
		name  string
		query string
		check func(Config) bool
	}{
		{"queryOptimizeGoal numeric", "queryOptimizeGoal=1", func(c Config) bool { return c.QueryOptimizeGoal == 1 }},
		{"queryOptimizeGoal name", "queryOptimizeGoal=*ALLIO", func(c Config) bool { return c.QueryOptimizeGoal == 2 }},
		{"query optimize goal JTOpen spelling", "query+optimize+goal=firstio", func(c Config) bool { return c.QueryOptimizeGoal == 1 }},
		{"decimalMaxPrecision 63", "decimalMaxPrecision=63", func(c Config) bool { return c.DecimalMaxPrecision == 63 }},
		{"decimalMaxScale 63", "decimalMaxScale=63", func(c Config) bool { return c.DecimalMaxScale == 63 }},
		{"decimalMaxScale 0", "decimalMaxScale=0", func(c Config) bool { return c.DecimalMaxScale == 0 }},
		{"minimumDivideScale 9", "minimumDivideScale=9", func(c Config) bool { return c.MinimumDivideScale == 9 }},
		{"lobThreshold max", "lobThreshold=15728640", func(c Config) bool { return c.LOBThreshold == 15728640 }},
		{"lobThreshold zero", "lobThreshold=0", func(c Config) bool { return c.LOBThreshold == 0 }},
		{"blockSize 0", "blockSize=0", func(c Config) bool { return c.BlockSize == 0 }},
		{"blockSize 32", "blockSize=32", func(c Config) bool { return c.BlockSize == 32 }},
		{"blockSize 1024 (JTOpen range)", "blockSize=1024", func(c Config) bool { return c.BlockSize == 1024 }},
		{"trueAutocommit", "trueAutocommit=true", func(c Config) bool { return c.TrueAutocommit }},
		{"true autocommit JTOpen spelling", "true+autocommit=true", func(c Config) bool { return c.TrueAutocommit }},
		{"decfloat half-up", "decfloatRoundingMode=half-up", func(c Config) bool { return c.DecfloatRoundingMode == 1 }},
		{"decfloat up", "decfloatRoundingMode=up", func(c Config) bool { return c.DecfloatRoundingMode == 6 }},
		{"decfloat numeric", "decfloatRoundingMode=4", func(c Config) bool { return c.DecfloatRoundingMode == 4 }},
		{"decfloat JTOpen spelling", "decfloat+rounding+mode=half+down", func(c Config) bool { return c.DecfloatRoundingMode == 5 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseAttributeTestDSN(t, tt.query)
			if !tt.check(cfg) {
				t.Fatalf("ParseDSN(%q) produced unexpected config: %+v", tt.query, cfg)
			}
		})
	}
}

func TestParseDSNServerAttributeInvalidValues(t *testing.T) {
	invalid := []string{
		"queryOptimizeGoal=3",
		"queryOptimizeGoal=fast",
		"decimalMaxPrecision=30",
		"decimalMaxPrecision=abc",
		"decimalMaxScale=64",
		"decimalMaxScale=-1",
		"minimumDivideScale=10",
		"lobThreshold=15728641",
		"lobThreshold=-5",
		"lobThreshold=big",
		"blockSize=100",
		"blockSize=-8",
		"trueAutocommit=maybe",
		"decfloatRoundingMode=7",
		"decfloatRoundingMode=bankers",
	}
	for _, query := range invalid {
		t.Run(query, func(t *testing.T) {
			_, err := ParseDSN("as400://user:pass@host/qgpl?" + query)
			if err == nil {
				t.Fatalf("ParseDSN(%q) error = nil, want error", query)
			}
			if !errors.Is(err, ErrInvalidDSN) {
				t.Fatalf("ParseDSN(%q) error = %v, want ErrInvalidDSN", query, err)
			}
		})
	}
}

func TestServerAttributeRequestDefaults(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql")
	request, err := buildSetServerAttributesRequest(cfg, false, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}

	precision, found, err := readTestLLCPValue(request[40:], CodePointDecimalPrecision)
	if err != nil || !found {
		t.Fatalf("0x3827 found=%v err=%v", found, err)
	}
	if want := []byte{0x00, 0x1F, 0x00, 0x1F, 0x00, 0x00}; !bytes.Equal(precision, want) {
		t.Fatalf("0x3827 = % x, want % x", precision, want)
	}

	lob, found, err := readTestLLCPValue(request[40:], CodePointLOBFieldThreshold)
	if err != nil || !found {
		t.Fatalf("0x3822 found=%v err=%v", found, err)
	}
	if want := []byte{0x00, 0x10, 0x00, 0x00}; !bytes.Equal(lob, want) {
		t.Fatalf("0x3822 = % x, want % x (1 MiB)", lob, want)
	}

	if _, found, _ := readTestLLCPValue(request[40:], CodePointQueryOptimizeGoal); found {
		t.Fatalf("0x3833 sent with default queryOptimizeGoal")
	}
	if _, found, _ := readTestLLCPValue(request[40:], CodePointDecfloatRoundingMode); found {
		t.Fatalf("0x3835 sent with default decfloatRoundingMode")
	}
}

func TestServerAttributeRequestNonDefaultValues(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql&queryOptimizeGoal=2&decimalMaxScale=40&minimumDivideScale=6&lobThreshold=32768&decfloatRoundingMode=6")
	request, err := buildSetServerAttributesRequest(cfg, false, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}

	precision, _, _ := readTestLLCPValue(request[40:], CodePointDecimalPrecision)
	// Scale 40 forces precision 63 (JTOpen behaviour).
	if want := []byte{0x00, 0x3F, 0x00, 0x28, 0x00, 0x06}; !bytes.Equal(precision, want) {
		t.Fatalf("0x3827 = % x, want % x", precision, want)
	}

	goal, found, _ := readTestLLCPValue(request[40:], CodePointQueryOptimizeGoal)
	if !found || !bytes.Equal(goal, []byte{0xC1}) {
		t.Fatalf("0x3833 = % x found=%v, want C1 (*ALLIO)", goal, found)
	}

	lob, _, _ := readTestLLCPValue(request[40:], CodePointLOBFieldThreshold)
	if want := []byte{0x00, 0x00, 0x80, 0x00}; !bytes.Equal(lob, want) {
		t.Fatalf("0x3822 = % x, want % x", lob, want)
	}

	if got := requestBodyShortValue(t, request, CodePointDecfloatRoundingMode); got != 6 {
		t.Fatalf("0x3835 = %d, want 6", got)
	}
}

func TestServerAttributeFirstIOGoal(t *testing.T) {
	cfg := parseAttributeTestDSN(t, "naming=sql&queryOptimizeGoal=1")
	request, err := buildSetServerAttributesRequest(cfg, false, cfg.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	goal, found, _ := readTestLLCPValue(request[40:], CodePointQueryOptimizeGoal)
	if !found || !bytes.Equal(goal, []byte{0xC6}) {
		t.Fatalf("0x3833 = % x found=%v, want C6 (*FIRSTIO)", goal, found)
	}
}

func TestServerCommitmentControlWithTrueAutocommit(t *testing.T) {
	off := parseAttributeTestDSN(t, "naming=sql&commitmentControl=all")
	on := parseAttributeTestDSN(t, "naming=sql&commitmentControl=all&trueAutocommit=true")

	// Default: autocommit forces *NONE, as before.
	if got := serverCommitmentControl(off, true, 3); got != 0 {
		t.Fatalf("autocommit without trueAutocommit = %d, want 0", got)
	}
	// trueAutocommit keeps the configured commitment control level.
	if got := serverCommitmentControl(on, true, 3); got != 3 {
		t.Fatalf("autocommit with trueAutocommit = %d, want 3", got)
	}
	// Without autocommit the configured level is always used.
	if got := serverCommitmentControl(off, false, 3); got != 3 {
		t.Fatalf("manual commit = %d, want 3", got)
	}

	request, err := buildSetServerAttributesRequest(on, true, on.CommitmentControl)
	if err != nil {
		t.Fatalf("buildSetServerAttributesRequest() error = %v", err)
	}
	if got := requestBodyShortValue(t, request, CodePointCommitmentControlLevel); got != 3 {
		t.Fatalf("request commitment control = %d, want 3", got)
	}
	indicator, _, _ := readTestLLCPValue(request[40:], CodePointTrueAutoCommitIndicator)
	if !bytes.Equal(indicator, []byte{0xE8}) {
		t.Fatalf("0x3824 = % x, want E8", indicator)
	}
}

func TestLOBThresholdValueClamping(t *testing.T) {
	tests := map[int]uint32{-1: 0, 0: 0, 4096: 4096, 15728640: 15728640, 20000000: 15728640}
	for in, want := range tests {
		cfg := Config{LOBThreshold: in}
		if got := cfg.lobThresholdValue(); got != want {
			t.Fatalf("lobThresholdValue(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestFetchBufferBytes(t *testing.T) {
	tests := map[int]uint32{0: 256 * 1024, 8: 8 * 1024, 32: 32 * 1024, 512: 512 * 1024}
	for in, want := range tests {
		cfg := Config{BlockSize: in}
		if got := cfg.fetchBufferBytes(); got != want {
			t.Fatalf("fetchBufferBytes(blockSize=%d) = %d, want %d", in, got, want)
		}
	}
}

func TestAutoCommitToggleHonoursTrueAutocommit(t *testing.T) {
	cfg := Config{CommitmentControl: 2}
	if got := serverCommitmentControl(cfg, true, 2); got != 0 {
		t.Fatalf("autocommit without trueAutocommit level = %d, want 0", got)
	}
	cfg.TrueAutocommit = true
	if got := serverCommitmentControl(cfg, true, 2); got != 2 {
		t.Fatalf("autocommit with trueAutocommit level = %d, want 2", got)
	}
	plain := buildAutoCommitRequest(true, 2)
	resolved := buildAutoCommitRequestLevel(true, 0)
	if string(plain) != string(resolved) {
		t.Fatal("buildAutoCommitRequest must match buildAutoCommitRequestLevel for the default rule")
	}
}

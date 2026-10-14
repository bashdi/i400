package i400

import (
	"context"
	"testing"
	"time"
)

// encodeEBCDICText encodes the characters used in USA time text as CCSID 37 bytes.
func encodeEBCDICText(s string) []byte {
	table := map[rune]byte{':': 0x7A, ' ': 0x40, 'A': 0xC1, 'M': 0xD4, 'P': 0xD7}
	out := make([]byte, len(s))
	for i, ch := range s {
		if b, ok := table[ch]; ok {
			out[i] = b
		} else {
			out[i] = ebcdic37Byte(ch)
		}
	}
	return out
}

func dtSettings(dateFormat, dateSep, timeFormat, timeSep int) dateTimeSettings {
	return dateTimeSettings{DateFormat: dateFormat, DateSeparator: dateSep, TimeFormat: timeFormat, TimeSeparator: timeSep}
}

func TestFormatDateCombinations(t *testing.T) {
	day := time.Date(2026, time.April, 22, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		cfg  dateTimeSettings
		want string
	}{
		{"iso ignores separator", dtSettings(dateFormatISO, 1, 0, 0), "2026-04-22"},
		{"usa ignores separator", dtSettings(dateFormatUSA, 1, 0, 0), "04/22/2026"},
		{"eur ignores separator", dtSettings(dateFormatEUR, 0, 0, 0), "22.04.2026"},
		{"jis ignores separator", dtSettings(dateFormatJIS, 2, 0, 0), "2026-04-22"},
		{"mdy slash", dtSettings(dateFormatMDY, 0, 0, 0), "04/22/26"},
		{"mdy dash", dtSettings(dateFormatMDY, 1, 0, 0), "04-22-26"},
		{"mdy dot", dtSettings(dateFormatMDY, 2, 0, 0), "04.22.26"},
		{"mdy comma", dtSettings(dateFormatMDY, 3, 0, 0), "04,22,26"},
		{"mdy space", dtSettings(dateFormatMDY, 4, 0, 0), "04 22 26"},
		{"dmy slash", dtSettings(dateFormatDMY, 0, 0, 0), "22/04/26"},
		{"dmy dash", dtSettings(dateFormatDMY, 1, 0, 0), "22-04-26"},
		{"ymd slash", dtSettings(dateFormatYMD, 0, 0, 0), "26/04/22"},
		{"ymd dot", dtSettings(dateFormatYMD, 2, 0, 0), "26.04.22"},
		{"julian slash", dtSettings(dateFormatJulian, 0, 0, 0), "26/112"},
		{"julian space", dtSettings(dateFormatJulian, 4, 0, 0), "26 112"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDate(day, tt.cfg); got != tt.want {
				t.Errorf("formatDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDateRoundTripAllFormatsAndSeparators(t *testing.T) {
	// Dates inside 2000-2039 so 2-digit year formats round-trip exactly.
	dates := []time.Time{
		time.Date(2026, time.April, 22, 0, 0, 0, 0, time.UTC),
		time.Date(2000, time.February, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2039, time.December, 31, 0, 0, 0, 0, time.UTC),
	}
	for dateFormat := 0; dateFormat <= 7; dateFormat++ {
		for dateSep := 0; dateSep <= 4; dateSep++ {
			cfg := dtSettings(dateFormat, dateSep, 0, 0)
			for _, d := range dates {
				text := formatDate(d, cfg)
				got, err := parseDate(text, cfg)
				if err != nil {
					t.Errorf("fmt=%d sep=%d parseDate(%q) error: %v", dateFormat, dateSep, text, err)
					continue
				}
				if !got.Equal(d) {
					t.Errorf("fmt=%d sep=%d round trip %q: got %v, want %v", dateFormat, dateSep, text, got, d)
				}
			}
		}
	}
}

func TestParseDateTwoDigitYearWindow(t *testing.T) {
	tests := []struct {
		text string
		cfg  dateTimeSettings
		want time.Time
	}{
		{"04/22/39", dtSettings(dateFormatMDY, 0, 0, 0), time.Date(2039, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"04/22/40", dtSettings(dateFormatMDY, 0, 0, 0), time.Date(1940, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"22.04.99", dtSettings(dateFormatDMY, 2, 0, 0), time.Date(1999, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"22-04-99", dtSettings(dateFormatDMY, 1, 0, 0), time.Date(1999, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"26/04/22", dtSettings(dateFormatYMD, 0, 0, 0), time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"99/04/22", dtSettings(dateFormatYMD, 0, 0, 0), time.Date(1999, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"26/112", dtSettings(dateFormatJulian, 0, 0, 0), time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"40/113", dtSettings(dateFormatJulian, 0, 0, 0), time.Date(1940, 4, 22, 0, 0, 0, 0, time.UTC)},
		{"04/22/2039", dtSettings(dateFormatMDY, 0, 0, 0), time.Date(2039, 4, 22, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		got, err := parseDate(tt.text, tt.cfg)
		if err != nil {
			t.Errorf("parseDate(%q) error: %v", tt.text, err)
			continue
		}
		if !got.Equal(tt.want) {
			t.Errorf("parseDate(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestParseDateUSAAndEUR(t *testing.T) {
	got, err := parseDate("04/22/2026", dtSettings(dateFormatUSA, 0, 0, 0))
	if err != nil || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("USA parse = %v, %v", got, err)
	}
	got, err = parseDate("22.04.2026", dtSettings(dateFormatEUR, 0, 0, 0))
	if err != nil || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("EUR parse = %v, %v", got, err)
	}
}

func TestParseDateInvalid(t *testing.T) {
	cases := []struct {
		text string
		cfg  dateTimeSettings
	}{
		{"02/30/2026", dtSettings(dateFormatUSA, 0, 0, 0)},
		{"13/01/2026", dtSettings(dateFormatUSA, 0, 0, 0)},
		{"2026/366", dtSettings(dateFormatJulian, 0, 0, 0)}, // 2026 is not a leap year
		{"abc", dtSettings(dateFormatISO, 0, 0, 0)},
		{"2026-4", dtSettings(dateFormatISO, 0, 0, 0)},
	}
	for _, tc := range cases {
		if _, err := parseDate(tc.text, tc.cfg); err == nil {
			t.Errorf("parseDate(%q) expected error", tc.text)
		}
	}
}

func TestFormatParseTimeUSA(t *testing.T) {
	cfg := dtSettings(dateFormatISO, 0, timeFormatUSA, 0)
	tests := []struct {
		hour, minute int
		text         string
	}{
		{0, 0, "12:00 AM"},
		{0, 5, "12:05 AM"},
		{1, 5, "01:05 AM"},
		{11, 59, "11:59 AM"},
		{12, 0, "12:00 PM"},
		{12, 30, "12:30 PM"},
		{13, 5, "01:05 PM"},
		{23, 59, "11:59 PM"},
	}
	for _, tt := range tests {
		tm := time.Date(0, time.January, 1, tt.hour, tt.minute, 0, 0, time.UTC)
		if got := formatTime(tm, cfg); got != tt.text {
			t.Errorf("formatTime(%02d:%02d) = %q, want %q", tt.hour, tt.minute, got, tt.text)
		}
		got, err := parseTime(tt.text, cfg)
		if err != nil {
			t.Errorf("parseTime(%q) error: %v", tt.text, err)
			continue
		}
		if !got.Equal(tm) {
			t.Errorf("parseTime(%q) = %v, want %v", tt.text, got, tm)
		}
	}
}

// The server renders midnight in USA format as "00:00 AM" (seen on PUB400).
func TestParseTimeUSAZeroHour(t *testing.T) {
	got, err := parseTime("00:00 AM", dtSettings(0, 0, timeFormatUSA, 0))
	if err != nil {
		t.Fatalf("parseTime(00:00 AM) error: %v", err)
	}
	if got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("parseTime(00:00 AM) = %v, want midnight", got)
	}
}

func TestParseTimeUSAInvalidMarker(t *testing.T) {
	if _, err := parseTime("01:05 XM", dtSettings(0, 0, timeFormatUSA, 0)); err == nil {
		t.Error("expected error for invalid AM/PM marker")
	}
}

func TestTimeRoundTripFormats(t *testing.T) {
	// The USA format carries no seconds (as in JTOpen), so use a whole-minute time.
	tm := time.Date(0, time.January, 1, 13, 45, 0, 0, time.UTC)
	for timeFormat := 0; timeFormat <= 4; timeFormat++ {
		for timeSep := 0; timeSep <= 3; timeSep++ {
			cfg := dtSettings(dateFormatISO, 0, timeFormat, timeSep)
			text := formatTime(tm, cfg)
			got, err := parseTime(text, cfg)
			if err != nil {
				t.Errorf("fmt=%d sep=%d parseTime(%q) error: %v", timeFormat, timeSep, text, err)
				continue
			}
			if !got.Equal(tm) {
				t.Errorf("fmt=%d sep=%d round trip %q: got %v", timeFormat, timeSep, text, got)
			}
		}
	}
}

func TestFormatTimeSeparators(t *testing.T) {
	tm := time.Date(0, time.January, 1, 9, 5, 7, 0, time.UTC)
	tests := []struct {
		cfg  dateTimeSettings
		want string
	}{
		{dtSettings(0, 0, timeFormatHMS, 0), "09:05:07"},
		{dtSettings(0, 0, timeFormatHMS, 1), "09.05.07"},
		{dtSettings(0, 0, timeFormatHMS, 2), "09,05,07"},
		{dtSettings(0, 0, timeFormatHMS, 3), "09 05 07"},
		{dtSettings(0, 0, timeFormatISO, 0), "09.05.07"},
		{dtSettings(0, 0, timeFormatEUR, 0), "09.05.07"},
		{dtSettings(0, 0, timeFormatJIS, 1), "09:05:07"},
	}
	for _, tt := range tests {
		if got := formatTime(tm, tt.cfg); got != tt.want {
			t.Errorf("formatTime(timeFormat=%d timeSep=%d) = %q, want %q", tt.cfg.TimeFormat, tt.cfg.TimeSeparator, got, tt.want)
		}
	}
}

func TestParseTimeAcceptsEitherSeparatorAndMidnight24(t *testing.T) {
	cfg := dtSettings(dateFormatISO, 0, timeFormatHMS, 0)
	got, err := parseTime("13.45.30", cfg)
	if err != nil || !got.Equal(time.Date(0, 1, 1, 13, 45, 30, 0, time.UTC)) {
		t.Fatalf("parseTime(dot) = %v, %v", got, err)
	}
	got, err = parseTime("24.00.00", cfg)
	if err != nil || !got.Equal(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("parseTime(24.00.00) = %v, %v", got, err)
	}
	if _, err := parseTime("25:00:00", cfg); err == nil {
		t.Error("expected error for hour 25")
	}
}

func TestDefaultSettingsAreISO(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/schema")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	dt := dateTimeSettingsFromConfig(*cfg)
	if dt != defaultDateTimeSettings() {
		t.Fatalf("config defaults = %+v, want %+v", dt, defaultDateTimeSettings())
	}
	day := time.Date(2026, time.April, 22, 0, 0, 0, 0, time.UTC)
	if got := formatDate(day, dt); got != "2026-04-22" {
		t.Errorf("default formatDate = %q, want 2026-04-22", got)
	}
	if got, err := parseDate("2026-04-22", dt); err != nil || !got.Equal(day) {
		t.Errorf("default parseDate = %v, %v", got, err)
	}
}

func TestDecodeColumnValueDateUsesConnectionFormat(t *testing.T) {
	raw := encodeEBCDIC37("04/22/2026")
	col := columnMeta{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 10}
	val, err := decodeColumnValueWithSettings(col, raw, dtSettings(dateFormatUSA, 0, 0, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := val.(time.Time); !ok || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("decoded USA date = %#v", val)
	}
}

func TestDecodeColumnValueTimeUsesConnectionFormat(t *testing.T) {
	raw := encodeEBCDICText("01:05 PM")
	col := columnMeta{Type: db2TypeTime, CCSID: 37, Offset: 0, Length: 8}
	val, err := decodeColumnValueWithSettings(col, raw, dtSettings(0, 0, timeFormatUSA, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := val.(time.Time); !ok || !got.Equal(time.Date(0, 1, 1, 13, 5, 0, 0, time.UTC)) {
		t.Errorf("decoded USA time = %#v", val)
	}
}

func TestDecodeColumnValueDateInvalidIsError(t *testing.T) {
	raw := encodeEBCDIC37("2026-04-22")
	col := columnMeta{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 10}
	// Server text is ISO but the connection says USA: must not be silently returned as a string.
	if _, err := decodeColumnValueWithSettings(col, raw, dtSettings(dateFormatUSA, 0, 0, 0)); err == nil {
		t.Error("expected error for DATE text that does not match the configured format")
	}
}

func TestDecodeColumnValueWithContextUsesConnConfig(t *testing.T) {
	raw := encodeEBCDIC37("22.04.2026")
	col := columnMeta{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 10}
	conn := &Conn{cfg: Config{DateFormat: dateFormatEUR}}
	val, err := decodeColumnValueWithContext(context.Background(), conn, col, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := val.(time.Time); !ok || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("decoded EUR date via conn = %#v", val)
	}
}

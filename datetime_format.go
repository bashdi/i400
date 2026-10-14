package i400

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"time"
)

// DATE and TIME text formatting/parsing driven by the connection's
// dateFormat / dateSeparator / timeFormat / timeSeparator settings.
//
// Mirrors JTOpen SQLDate.stringToDate/dateToString and
// SQLTime.stringToTime/timeToString. Format codes follow SQLConversionSettings:
//   date format:  0 julian, 1 mdy, 2 dmy, 3 ymd, 4 usa, 5 iso, 6 eur, 7 jis
//   date sep:     0 "/", 1 "-", 2 ".", 3 ",", 4 " "
//   time format:  0 hms, 1 usa, 2 iso, 3 eur, 4 jis
//   time sep:     0 ":", 1 ".", 2 ",", 3 " "
// USA, ISO, EUR and JIS ignore the separator setting and use fixed separators,
// as JTOpen does.

const (
	dateFormatJulian = 0
	dateFormatMDY    = 1
	dateFormatDMY    = 2
	dateFormatYMD    = 3
	dateFormatUSA    = 4
	dateFormatISO    = 5
	dateFormatEUR    = 6
	dateFormatJIS    = 7

	timeFormatHMS = 0
	timeFormatUSA = 1
	timeFormatISO = 2
	timeFormatEUR = 3
	timeFormatJIS = 4
)

var dateSeparatorText = [...]string{"/", "-", ".", ",", " "}
var timeSeparatorText = [...]string{":", ".", ",", " "}

// dateTimeSettings holds the numeric DSN codes that control DATE/TIME text.
type dateTimeSettings struct {
	DateFormat    int
	DateSeparator int
	TimeFormat    int
	TimeSeparator int
	// TimestampString decodes TIMESTAMP columns to IBM i text instead of time.Time.
	TimestampString bool
}

// defaultDateTimeSettings returns the DSN defaults (ISO dates, HMS times).
func defaultDateTimeSettings() dateTimeSettings {
	return dateTimeSettings{
		DateFormat:    defaultDateFormat,
		DateSeparator: defaultDateSeparator,
		TimeFormat:    defaultTimeFormat,
		TimeSeparator: defaultTimeSeparator,
	}
}

func dateTimeSettingsFromConfig(cfg Config) dateTimeSettings {
	return dateTimeSettings{
		DateFormat:      cfg.DateFormat,
		DateSeparator:   cfg.DateSeparator,
		TimeFormat:      cfg.TimeFormat,
		TimeSeparator:   cfg.TimeSeparator,
		TimestampString: cfg.TimestampString,
	}
}

// dateTimeSettingsFor returns the settings used to decode DATE and TIME values of
// a result set. The super extended data format (code point 0x3812) sent with the
// result set carries the server's date and time format. As in JTOpen
// (JDServerRow, SQLDate.convertFromRawBytes, SQLTime.convertFromRawBytes), those
// two formats override the connection's only for stored procedure result sets,
// and only when the server sent the format and its VRM is at least V6R1 (JTOpen
// checks vrm610). An unknown VRM (0) does not override. Its separators are not
// used for decoding, as in JTOpen. Otherwise the connection settings apply.
func (m *resultSetMeta) dateTimeSettingsFor(conn *Conn, storedProc bool) dateTimeSettings {
	dt := dateTimeSettingsForConn(conn)
	if m != nil && storedProc && m.hasDataFormat && conn.serverVRMAtLeast(storedProcDateTimeMinVRM) {
		dt.DateFormat = m.DateFormat
		dt.TimeFormat = m.TimeFormat
	}
	return dt
}

// storedProcDateTimeMinVRM is V6R1M0 in JTOpen's VRM encoding ((V<<16)|(R<<8)|M).
const storedProcDateTimeMinVRM = 0x060100

// serverVRMAtLeast reports whether the server VRM is known and at least min.
// The VRM comes from the signon info; 0 means unknown.
func (c *Conn) serverVRMAtLeast(min int) bool {
	return c != nil && c.systemInfo != nil && c.systemInfo.ServerVersion != 0 && c.systemInfo.ServerVersion >= min
}

// markStoredProcResult flags a CALL result set so that its DATE and TIME values
// use the format reported by the server. Other statements are left unchanged.
func markStoredProcResult(rows driver.Rows, statementType int) {
	if qr, ok := rows.(*queryRows); ok && statementType == sqlStatementTypeCall {
		qr.storedProcResult = true
	}
}

// dateTimeSettingsForConn returns the settings of conn, or the defaults when conn is nil.
func dateTimeSettingsForConn(conn *Conn) dateTimeSettings {
	if conn == nil {
		return defaultDateTimeSettings()
	}
	return dateTimeSettingsFromConfig(conn.cfg)
}

func (s dateTimeSettings) dateSep() string {
	if s.DateSeparator < 0 || s.DateSeparator >= len(dateSeparatorText) {
		return dateSeparatorText[0]
	}
	return dateSeparatorText[s.DateSeparator]
}

func (s dateTimeSettings) timeSep() string {
	if s.TimeSeparator < 0 || s.TimeSeparator >= len(timeSeparatorText) {
		return timeSeparatorText[0]
	}
	return timeSeparatorText[s.TimeSeparator]
}

// formatDate renders the date part of t using the configured date format.
func formatDate(t time.Time, cfg dateTimeSettings) string {
	year, month, day := t.Date()
	sep := cfg.dateSep()
	switch cfg.DateFormat {
	case dateFormatUSA:
		return fmt.Sprintf("%02d/%02d/%04d", int(month), day, year)
	case dateFormatEUR:
		return fmt.Sprintf("%02d.%02d.%04d", day, int(month), year)
	case dateFormatJulian:
		return fmt.Sprintf("%02d%s%03d", year%100, sep, t.YearDay())
	case dateFormatMDY:
		return fmt.Sprintf("%02d%s%02d%s%02d", int(month), sep, day, sep, year%100)
	case dateFormatDMY:
		return fmt.Sprintf("%02d%s%02d%s%02d", day, sep, int(month), sep, year%100)
	case dateFormatYMD:
		return fmt.Sprintf("%02d%s%02d%s%02d", year%100, sep, int(month), sep, day)
	default: // ISO and JIS
		return fmt.Sprintf("%04d-%02d-%02d", year, int(month), day)
	}
}

// formatTime renders the time-of-day part of t using the configured time format.
func formatTime(t time.Time, cfg dateTimeSettings) string {
	hour, minute, second := t.Clock()
	sep := cfg.timeSep()
	switch cfg.TimeFormat {
	case timeFormatUSA:
		marker := "A"
		switch {
		case hour > 12:
			hour -= 12
			marker = "P"
		case hour == 12:
			marker = "P"
		case hour == 0:
			hour = 12
		}
		return fmt.Sprintf("%02d:%02d %sM", hour, minute, marker)
	case timeFormatISO, timeFormatEUR:
		return fmt.Sprintf("%02d.%02d.%02d", hour, minute, second)
	case timeFormatJIS:
		return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second)
	default: // HMS
		return fmt.Sprintf("%02d%s%02d%s%02d", hour, sep, minute, sep, second)
	}
}

// parseDate parses DATE text in the configured format. Like JTOpen, a blank or
// all-zero value yields the epoch (a NULL-like placeholder). Separators are not
// validated; fields are read by position.
func parseDate(s string, cfg dateTimeSettings) (time.Time, error) {
	s = strings.TrimSpace(s)
	if strings.Trim(s, "0") == "" {
		return time.Unix(0, 0).UTC(), nil
	}
	r := &fieldReader{s: s}
	if cfg.DateFormat == dateFormatJulian {
		var year, dayOfYear int
		if len(s) <= 6 { // yy/ddd
			year = expandTwoDigitYear(r.num(0, 2))
			dayOfYear = r.num(3, 3)
		} else { // yyyy/ddd
			year = r.num(0, 4)
			dayOfYear = r.num(5, 3)
		}
		if r.err != nil {
			return time.Time{}, fmt.Errorf("invalid julian date %q: %w", s, r.err)
		}
		t := time.Date(year, time.January, dayOfYear, 0, 0, 0, 0, time.UTC)
		if dayOfYear < 1 || t.Year() != year || t.YearDay() != dayOfYear {
			return time.Time{}, fmt.Errorf("invalid julian date %q", s)
		}
		return t, nil
	}

	var year, month, day int
	switch cfg.DateFormat {
	case dateFormatUSA: // mm/dd/yyyy
		month, day, year = r.num(0, 2), r.num(3, 2), r.num(6, 4)
	case dateFormatEUR: // dd.mm.yyyy
		day, month, year = r.num(0, 2), r.num(3, 2), r.num(6, 4)
	case dateFormatMDY: // mm/dd/yy or mm/dd/yyyy
		month, day = r.num(0, 2), r.num(3, 2)
		year = r.yearAt(6, len(s))
	case dateFormatDMY: // dd/mm/yy or dd/mm/yyyy
		day, month = r.num(0, 2), r.num(3, 2)
		year = r.yearAt(6, len(s))
	case dateFormatYMD: // yy/mm/dd or yyyy/mm/dd
		if len(s) <= 8 {
			year, month, day = expandTwoDigitYear(r.num(0, 2)), r.num(3, 2), r.num(6, 2)
		} else {
			year, month, day = r.num(0, 4), r.num(5, 2), r.num(8, 2)
		}
	default: // ISO and JIS: yyyy-mm-dd
		year, month, day = r.num(0, 4), r.num(5, 2), r.num(8, 2)
	}
	if r.err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", s, r.err)
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, fmt.Errorf("invalid date %q", s)
	}
	return t, nil
}

// parseTime parses TIME text in the configured format. A blank value yields
// midnight. If the text is not a time but is an IBM i timestamp, its time part
// is used, as JTOpen does.
func parseTime(s string, cfg dateTimeSettings) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC), nil
	}
	t, err := parseTimeFields(s, cfg)
	if err == nil {
		return t, nil
	}
	if len(s) >= 19 && s[4] == '-' && s[7] == '-' {
		if ts, tsErr := parseIBMiTimestamp(s); tsErr == nil {
			return time.Date(0, time.January, 1, ts.Hour(), ts.Minute(), ts.Second(), 0, time.UTC), nil
		}
	}
	return time.Time{}, err
}

func parseTimeFields(s string, cfg dateTimeSettings) (time.Time, error) {
	r := &fieldReader{s: s}
	var hour, minute, second int
	if cfg.TimeFormat == timeFormatUSA { // hh:mm AM / hh:mm PM
		if len(s) < 7 {
			return time.Time{}, fmt.Errorf("invalid time %q", s)
		}
		hour, minute = r.num(0, 2), r.num(3, 2)
		if r.err != nil {
			return time.Time{}, fmt.Errorf("invalid time %q: %w", s, r.err)
		}
		// The server renders midnight as "00:00 AM"; JTOpen accepts any hour
		// up to 12 here.
		if hour > 12 {
			return time.Time{}, fmt.Errorf("invalid time %q: hour out of range", s)
		}
		switch marker := s[6] | 0x20; marker { // lower-case ASCII
		case 'a':
			if hour == 12 {
				hour = 0
			}
		case 'p':
			if hour != 12 {
				hour += 12
			}
		default:
			return time.Time{}, fmt.Errorf("invalid time %q: expected AM or PM", s)
		}
	} else { // hh:mm:ss or hh.mm.ss
		hour, minute, second = r.num(0, 2), r.num(3, 2), r.num(6, 2)
		if r.err != nil {
			return time.Time{}, fmt.Errorf("invalid time %q: %w", s, r.err)
		}
		if hour == 24 && minute == 0 && second == 0 { // DB2 accepts 24.00.00
			hour = 0
		}
	}
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, fmt.Errorf("invalid time %q: field out of range", s)
	}
	return time.Date(0, time.January, 1, hour, minute, second, 0, time.UTC), nil
}

// decodeDateText converts DATE text from the wire into a time.Time. Blank text is
// returned unchanged as a string. Text that does not parse is an error.
func decodeDateText(s string, cfg dateTimeSettings) (driver.Value, error) {
	if s == "" {
		return s, nil
	}
	t, err := parseDate(s, cfg)
	if err != nil {
		return nil, fmt.Errorf("i400: decode DATE value: %w", err)
	}
	return t.UTC(), nil
}

// decodeTimeText converts TIME text from the wire into a time.Time. Blank text is
// returned unchanged as a string. Text that does not parse is an error.
func decodeTimeText(s string, cfg dateTimeSettings) (driver.Value, error) {
	if s == "" {
		return s, nil
	}
	t, err := parseTime(s, cfg)
	if err != nil {
		return nil, fmt.Errorf("i400: decode TIME value: %w", err)
	}
	return t.UTC(), nil
}

// expandTwoDigitYear maps yy to a year in the 1940-2039 window, as JTOpen does.
func expandTwoDigitYear(yy int) int {
	if yy <= 39 {
		return 2000 + yy
	}
	return 1900 + yy
}

// fieldReader reads fixed-position numeric fields and records the first error.
type fieldReader struct {
	s   string
	err error
}

func (r *fieldReader) num(start, n int) int {
	if r.err != nil {
		return 0
	}
	if start < 0 || start+n > len(r.s) {
		r.err = fmt.Errorf("field at offset %d out of range", start)
		return 0
	}
	v := 0
	for i := start; i < start+n; i++ {
		c := r.s[i]
		if c < '0' || c > '9' {
			r.err = fmt.Errorf("unexpected character %q at offset %d", c, i)
			return 0
		}
		v = v*10 + int(c-'0')
	}
	return v
}

// yearAt reads a 2-digit year at start when the text is short (total length <= 8),
// and a 4-digit year otherwise, mirroring JTOpen's mdy/dmy handling.
func (r *fieldReader) yearAt(start, total int) int {
	if total <= 8 {
		return expandTwoDigitYear(r.num(start, 2))
	}
	return r.num(start, 4)
}

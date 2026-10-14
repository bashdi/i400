package i400

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxTimestampPicoseconds is the largest sub-second value a TIMESTAMP(12) holds,
// 999999999999 picoseconds.
const maxTimestampPicoseconds = 999999999999

// Timestamp holds an IBM i TIMESTAMP with picosecond precision. time.Time only
// has nanosecond resolution, so the sub-second part is carried in Picoseconds
// as JTOpen's SQLTimestamp does.
//
// Time provides the date and the time to the second. Picoseconds is the full
// sub-second part in picoseconds, 0 through 999999999999. Scan keeps the two
// consistent: Time.Nanosecond() is Picoseconds/1000, truncated. When Value
// is called with Picoseconds set to 0 it uses Time's nanosecond part, so a
// Timestamp built from a plain time.Time keeps its digits.
type Timestamp struct {
	Time        time.Time
	Picoseconds int64
}

// NullTimestamp is a Timestamp that may be NULL, like sql.NullTime.
type NullTimestamp struct {
	Timestamp Timestamp
	Valid     bool
}

var (
	_ sql.Scanner   = (*Timestamp)(nil)
	_ driver.Valuer = Timestamp{}
	_ sql.Scanner   = (*NullTimestamp)(nil)
	_ driver.Valuer = NullTimestamp{}
)

// Scan implements sql.Scanner. It accepts time.Time, and string or []byte text in
// the IBM i form "YYYY-MM-DD-HH.MM.SS[.f{1,12}]" or the ISO form
// "YYYY-MM-DD HH:MM:SS[.f{1,12}]". NULL is an error here; use NullTimestamp.
func (ts *Timestamp) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return errors.New("i400: cannot scan NULL into Timestamp; use NullTimestamp")
	case time.Time:
		*ts = Timestamp{Time: v, Picoseconds: int64(v.Nanosecond()) * 1000}
		return nil
	case string:
		return ts.parse(v)
	case []byte:
		return ts.parse(string(v))
	default:
		return fmt.Errorf("i400: cannot scan %T into Timestamp", src)
	}
}

func (ts *Timestamp) parse(s string) error {
	t, picos, err := parseTimestampText(s)
	if err != nil {
		return err
	}
	*ts = Timestamp{Time: t, Picoseconds: picos}
	return nil
}

// Value implements driver.Valuer. It returns the IBM i text
// "YYYY-MM-DD-HH.MM.SS.ffffffffffff" with all twelve fractional digits, so the
// value binds without loss to TIMESTAMP(12). The parameter encoder truncates
// the text to the column length, so TIMESTAMP(6) keeps six digits.
func (ts Timestamp) Value() (driver.Value, error) {
	return timestampText(ts)
}

// String returns the IBM i text form of the timestamp, the same as Value.
func (ts Timestamp) String() string {
	text, err := timestampText(ts)
	if err != nil {
		return fmt.Sprintf("Timestamp(invalid: %v)", err)
	}
	return text
}

// Scan implements sql.Scanner. A NULL sets Valid to false.
func (n *NullTimestamp) Scan(value any) error {
	if value == nil {
		n.Timestamp, n.Valid = Timestamp{}, false
		return nil
	}
	n.Valid = true
	return n.Timestamp.Scan(value)
}

// Value implements driver.Valuer. An invalid (NULL) value returns nil.
func (n NullTimestamp) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Timestamp.Value()
}

// timestampText formats ts as "YYYY-MM-DD-HH.MM.SS.ffffffffffff".
func timestampText(ts Timestamp) (string, error) {
	if ts.Picoseconds < 0 || ts.Picoseconds > maxTimestampPicoseconds {
		return "", fmt.Errorf("i400: Timestamp picoseconds %d out of range", ts.Picoseconds)
	}
	picos := ts.Picoseconds
	if picos == 0 {
		picos = int64(ts.Time.Nanosecond()) * 1000
	}
	return ts.Time.Format("2006-01-02-15.04.05") + fmt.Sprintf(".%012d", picos), nil
}

// parseTimestampText parses "YYYY-MM-DD-HH.MM.SS[.f{1,12}]" or
// "YYYY-MM-DD HH:MM:SS[.f{1,12}]". It returns the time, truncated to nanoseconds,
// and the full sub-second part in picoseconds. Fractional digits beyond twelve
// are an error.
func parseTimestampText(s string) (time.Time, int64, error) {
	s = strings.TrimSpace(s)
	if len(s) < 19 {
		return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
	}
	if s[4] != '-' || s[7] != '-' || (s[10] != '-' && s[10] != ' ' && s[10] != 'T') ||
		(s[13] != '.' && s[13] != ':') || (s[16] != '.' && s[16] != ':') {
		return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
	}
	year, errY := strconv.Atoi(s[0:4])
	month, errMo := strconv.Atoi(s[5:7])
	day, errD := strconv.Atoi(s[8:10])
	hour, errH := strconv.Atoi(s[11:13])
	minute, errMi := strconv.Atoi(s[14:16])
	second, errS := strconv.Atoi(s[17:19])
	for _, err := range []error{errY, errMo, errD, errH, errMi, errS} {
		if err != nil {
			return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
		}
	}
	if hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
	}
	date := time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC)
	if date.Year() != year || int(date.Month()) != month || date.Day() != day {
		return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
	}

	var picos int64
	if rest := s[19:]; rest != "" {
		frac := rest
		if frac[0] != '.' {
			return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp %q", s)
		}
		frac = frac[1:]
		if len(frac) < 1 || len(frac) > 12 || !isASCIIDigits(frac) {
			return time.Time{}, 0, fmt.Errorf("i400: invalid timestamp fraction in %q", s)
		}
		padded := frac + strings.Repeat("0", 12-len(frac))
		picos, _ = strconv.ParseInt(padded, 10, 64)
	}
	// Truncate, do not round, to nanoseconds as JTOpen Timestamp.getNanos does.
	nanos := int(picos / 1000)
	return time.Date(year, time.Month(month), day, hour, minute, second, nanos, time.UTC), picos, nil
}

// parseTimestampPrecisionOption parses the timestampPrecision DSN value. It returns
// true for "string" and false for "nanoseconds".
func parseTimestampPrecisionOption(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "nanoseconds":
		return false, nil
	case "string":
		return true, nil
	}
	return false, fmt.Errorf("invalid timestampPrecision %q", value)
}

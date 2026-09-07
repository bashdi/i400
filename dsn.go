package i400

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func ParseDSN(dsn string) (*Config, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("%w: empty DSN", ErrInvalidDSN)
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDSN, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "as400" && scheme != "as400s" {
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidDSN, parsed.Scheme)
	}

	config := &Config{
		DSN:               dsn,
		UseTLS:            scheme == "as400s",
		Naming:            NamingSQL,
		DateFormat:        defaultDateFormat,
		DateSeparator:     defaultDateSeparator,
		TimeFormat:        defaultTimeFormat,
		TimeSeparator:     defaultTimeSeparator,
		DecimalSeparator:  defaultDecimalSeparator,
		CommitmentControl: defaultCommitmentControl,
		RawQuery:          parsed.Query(),
	}

	if parsed.User != nil {
		config.User = parsed.User.Username()
		if password, ok := parsed.User.Password(); ok {
			config.Password = password
		}
	}

	config.Host = parsed.Hostname()
	if config.Host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrInvalidDSN)
	}

	if portValue := strings.TrimSpace(config.RawQuery.Get("port")); portValue != "" {
		port, err := strconv.Atoi(portValue)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid port %q", ErrInvalidDSN, portValue)
		}
		config.Port = port
	} else if portValue := strings.TrimSpace(config.RawQuery.Get("portNumber")); portValue != "" {
		port, err := strconv.Atoi(portValue)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid port %q", ErrInvalidDSN, portValue)
		}
		config.Port = port
	} else if portValue := parsed.Port(); portValue != "" {
		port, err := strconv.Atoi(portValue)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid port %q", ErrInvalidDSN, portValue)
		}
		config.Port = port
	}

	if path := strings.TrimPrefix(parsed.Path, "/"); path != "" {
		if slash := strings.IndexByte(path, '/'); slash >= 0 {
			config.Schema = path[:slash]
		} else {
			config.Schema = path
		}
	}

	if value := strings.TrimSpace(config.RawQuery.Get("tls")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid tls value %q", ErrInvalidDSN, value)
		}
		config.UseTLS = enabled
	}
	if value := strings.TrimSpace(config.RawQuery.Get("secure")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid secure value %q", ErrInvalidDSN, value)
		}
		config.UseTLS = enabled
	}

	if value := strings.TrimSpace(config.RawQuery.Get("naming")); value != "" {
		switch strings.ToLower(value) {
		case string(NamingSQL):
			config.Naming = NamingSQL
		case string(NamingSystem):
			config.Naming = NamingSystem
		default:
			return nil, fmt.Errorf("%w: invalid naming value %q", ErrInvalidDSN, value)
		}
	}

	if value := strings.TrimSpace(config.RawQuery.Get("dateFormat")); value != "" {
		option, err := parseDateFormatOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid dateFormat value %q", ErrInvalidDSN, value)
		}
		config.DateFormat = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("dateSeparator")); value != "" {
		option, err := parseDateSeparatorOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid dateSeparator value %q", ErrInvalidDSN, value)
		}
		config.DateSeparator = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("timeFormat")); value != "" {
		option, err := parseTimeFormatOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid timeFormat value %q", ErrInvalidDSN, value)
		}
		config.TimeFormat = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("timeSeparator")); value != "" {
		option, err := parseTimeSeparatorOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid timeSeparator value %q", ErrInvalidDSN, value)
		}
		config.TimeSeparator = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("decimalSeparator")); value != "" {
		option, err := parseDecimalSeparatorOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid decimalSeparator value %q", ErrInvalidDSN, value)
		}
		config.DecimalSeparator = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("commitmentControl")); value != "" {
		option, err := parseCommitmentControlOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid commitmentControl value %q", ErrInvalidDSN, value)
		}
		config.CommitmentControl = option
	}
	if value := strings.TrimSpace(config.RawQuery.Get("commitControl")); value != "" {
		option, err := parseCommitmentControlOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid commitControl value %q", ErrInvalidDSN, value)
		}
		config.CommitmentControl = option
	}

	if value := strings.TrimSpace(config.RawQuery.Get("libraries")); value != "" {
		parts := strings.Split(value, ",")
		config.Libraries = nil
		for _, part := range parts {
			library := strings.TrimSpace(part)
			if library != "" {
				config.Libraries = append(config.Libraries, library)
			}
		}
	}
	if value := strings.TrimSpace(config.RawQuery.Get("statementCacheSize")); value != "" {
		size, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid statementCacheSize value %q", ErrInvalidDSN, value)
		}
		config.StatementCacheSize = size
	}
	if value := strings.TrimSpace(config.RawQuery.Get("scrollableCursors")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid scrollableCursors value %q", ErrInvalidDSN, value)
		}
		config.ScrollableCursors = enabled
	}
	if value := strings.TrimSpace(config.RawQuery.Get("holdCursors")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid holdCursors value %q", ErrInvalidDSN, value)
		}
		config.HoldCursors = enabled
	}

	if value := strings.TrimSpace(config.RawQuery.Get("connectTimeout")); value != "" {
		duration, err := parseDurationValue(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid connectTimeout value %q", ErrInvalidDSN, value)
		}
		config.ConnectTimeout = duration
	} else if value := strings.TrimSpace(config.RawQuery.Get("timeout")); value != "" {
		duration, err := parseDurationValue(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid timeout value %q", ErrInvalidDSN, value)
		}
		config.ConnectTimeout = duration
	}

	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = 15 * time.Second
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

func parseDurationValue(value string) (time.Duration, error) {
	if duration, err := time.ParseDuration(value); err == nil {
		return duration, nil
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func parseDateFormatOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 7, map[string]int{
		"julian": 0,
		"mdy":    1,
		"dmy":    2,
		"ymd":    3,
		"usa":    4,
		"iso":    5,
		"eur":    6,
		"jis":    7,
	})
}

func parseTimeFormatOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 4, map[string]int{
		"hms": 0,
		"usa": 1,
		"iso": 2,
		"eur": 3,
		"jis": 4,
	})
}

func parseDateSeparatorOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 4, map[string]int{
		"/":      0,
		"slash":  0,
		"-":      1,
		"dash":   1,
		".":      2,
		"dot":    2,
		"period": 2,
		",":      3,
		"comma":  3,
		"space":  4,
	})
}

func parseTimeSeparatorOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 3, map[string]int{
		":":      0,
		"colon":  0,
		".":      1,
		"dot":    1,
		"period": 1,
		",":      2,
		"comma":  2,
		"space":  3,
	})
}

func parseDecimalSeparatorOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 1, map[string]int{
		".":      0,
		"dot":    0,
		"period": 0,
		",":      1,
		"comma":  1,
	})
}

func parseCommitmentControlOption(value string) (int, error) {
	return parseEnumeratedOption(value, 0, 4, map[string]int{
		"none":            0,
		"cs":              1,
		"cursorstability": 1,
		"chg":             2,
		"changedpage":     2,
		"all":             3,
		"rr":              4,
		"repeatableread":  4,
	})
}

func parseEnumeratedOption(value string, min, max int, mappings map[string]int) (int, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return 0, fmt.Errorf("empty option")
	}
	if parsed, err := strconv.Atoi(trimmed); err == nil {
		if parsed < min || parsed > max {
			return 0, fmt.Errorf("option %q out of range", value)
		}
		return parsed, nil
	}
	if parsed, ok := mappings[trimmed]; ok {
		return parsed, nil
	}
	normalized := normalizeOptionKey(trimmed)
	if parsed, ok := mappings[normalized]; ok {
		return parsed, nil
	}
	return 0, fmt.Errorf("unknown option %q", value)
}

func normalizeOptionKey(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case ' ', '_', '-':
			continue
		default:
			builder.WriteByte(value[i])
		}
	}
	return builder.String()
}

package i400

import (
	"fmt"
	"net/url"
	"sort"
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
		DSN:                   dsn,
		UseTLS:                scheme == "as400s",
		Naming:                NamingSQL,
		DateFormat:            defaultDateFormat,
		DateSeparator:         defaultDateSeparator,
		TimeFormat:            defaultTimeFormat,
		TimeSeparator:         defaultTimeSeparator,
		DecimalSeparator:      defaultDecimalSeparator,
		BidiStringType:        defaultBidiStringType,
		CommitmentControl:     defaultCommitmentControl,
		QueryOptimizeGoal:     defaultQueryOptimizeGoal,
		DecimalMaxPrecision:   defaultDecimalMaxPrecision,
		DecimalMaxScale:       defaultDecimalMaxScale,
		MinimumDivideScale:    defaultMinimumDivideScale,
		LOBThreshold:          defaultLOBThreshold,
		BlockSize:             defaultBlockSizeKB,
		QueryStorageLimit:     defaultQueryStorageLimit,
		QueryTimeoutMechanism: queryTimeoutMechanismCancel,
		RawQuery:              parsed.Query(),
	}
	config.BidiImplicitReordering = true

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

	if err := parseServerAttributeOptions(config); err != nil {
		return nil, err
	}
	if err := parseSocketOptions(config); err != nil {
		return nil, err
	}
	if err := parsePackageOptions(config); err != nil {
		return nil, err
	}
	if err := parseClientInfoOptions(config); err != nil {
		return nil, err
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
	if value := strings.TrimSpace(config.RawQuery.Get("timestampPrecision")); value != "" {
		asString, err := parseTimestampPrecisionOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid timestampPrecision value %q", ErrInvalidDSN, value)
		}
		config.TimestampString = asString
	}
	if value, ok := optionValue(config.RawQuery, "bidiStringType"); ok {
		bidiType, err := parseBidiStringTypeOption(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid bidiStringType value %q", ErrInvalidDSN, value)
		}
		config.BidiStringType = bidiType
	} else if _, present := config.RawQuery["bidiStringType"]; present {
		// An empty value is JTOpen's "not set" (BIDI_STRING_TYPE_NOTSET).
		config.BidiStringType = bidiStringTypeNotSet
	}
	if value := strings.TrimSpace(config.RawQuery.Get("bidiImplicitReordering")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid bidiImplicitReordering value %q", ErrInvalidDSN, value)
		}
		config.BidiImplicitReordering = enabled
	}
	if value := strings.TrimSpace(config.RawQuery.Get("bidiNumericOrdering")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid bidiNumericOrdering value %q", ErrInvalidDSN, value)
		}
		config.BidiNumericOrdering = enabled
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

// optionValue looks up an option by its camelCase name and also accepts the
// JTOpen spelling, ignoring case, spaces, underscores and dashes, so
// "queryOptimizeGoal", "query optimize goal" and "query_optimize_goal" are equivalent.
func optionValue(query url.Values, name string) (string, bool) {
	if value := strings.TrimSpace(query.Get(name)); value != "" {
		return value, true
	}
	want := normalizeOptionKey(strings.ToLower(name))
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if normalizeOptionKey(strings.ToLower(strings.TrimSpace(key))) != want {
			continue
		}
		for _, candidate := range query[key] {
			if value := strings.TrimSpace(candidate); value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func parseServerAttributeOptions(config *Config) error {
	q := config.RawQuery

	if value, ok := optionValue(q, "queryOptimizeGoal"); ok {
		option, err := parseEnumeratedOption(value, 0, 2, map[string]int{
			"default":  0,
			"firstio":  1,
			"*firstio": 1,
			"allio":    2,
			"*allio":   2,
		})
		if err != nil {
			return fmt.Errorf("%w: invalid queryOptimizeGoal value %q", ErrInvalidDSN, value)
		}
		config.QueryOptimizeGoal = option
	}

	if value, ok := optionValue(q, "decimalMaxPrecision"); ok {
		precision, err := strconv.Atoi(value)
		if err != nil || (precision != 31 && precision != 63) {
			return fmt.Errorf("%w: invalid decimalMaxPrecision value %q", ErrInvalidDSN, value)
		}
		config.DecimalMaxPrecision = precision
	}

	if value, ok := optionValue(q, "decimalMaxScale"); ok {
		scale, err := strconv.Atoi(value)
		if err != nil || scale < 0 || scale > 63 {
			return fmt.Errorf("%w: invalid decimalMaxScale value %q", ErrInvalidDSN, value)
		}
		config.DecimalMaxScale = scale
	}

	if value, ok := optionValue(q, "minimumDivideScale"); ok {
		scale, err := strconv.Atoi(value)
		if err != nil || scale < 0 || scale > 9 {
			return fmt.Errorf("%w: invalid minimumDivideScale value %q", ErrInvalidDSN, value)
		}
		config.MinimumDivideScale = scale
	}

	if value, ok := optionValue(q, "lobThreshold"); ok {
		threshold, err := strconv.Atoi(value)
		if err != nil || threshold < 0 || threshold > maxLOBThreshold {
			return fmt.Errorf("%w: invalid lobThreshold value %q", ErrInvalidDSN, value)
		}
		config.LOBThreshold = threshold
	}

	if value, ok := optionValue(q, "blockSize"); ok {
		blockSize, err := strconv.Atoi(value)
		if err != nil || !validBlockSize(blockSize) {
			return fmt.Errorf("%w: invalid blockSize value %q", ErrInvalidDSN, value)
		}
		config.BlockSize = blockSize
	}

	if value, ok := optionValue(q, "errors"); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "basic":
			config.ErrorsFull = false
		case "full":
			config.ErrorsFull = true
		default:
			return fmt.Errorf("%w: invalid errors value %q", ErrInvalidDSN, value)
		}
	}

	if value, ok := optionValue(q, "trueAutocommit"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid trueAutocommit value %q", ErrInvalidDSN, value)
		}
		config.TrueAutocommit = enabled
	}

	if value, ok := optionValue(q, "useBlockUpdate"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid useBlockUpdate value %q", ErrInvalidDSN, value)
		}
		config.UseBlockUpdate = enabled
	}

	if value, ok := optionValue(q, "returnGeneratedKeys"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid returnGeneratedKeys value %q", ErrInvalidDSN, value)
		}
		config.ReturnGeneratedKeys = enabled
	}

	if value, ok := optionValue(q, "decfloatRoundingMode"); ok {
		option, err := parseEnumeratedOption(value, 0, 6, map[string]int{
			"halfeven": 0,
			"halfup":   1,
			"down":     2,
			"ceiling":  3,
			"floor":    4,
			"halfdown": 5,
			"up":       6,
		})
		if err != nil {
			return fmt.Errorf("%w: invalid decfloatRoundingMode value %q", ErrInvalidDSN, value)
		}
		config.DecfloatRoundingMode = option
	}
	return nil
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

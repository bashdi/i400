package i400

import (
	"database/sql/driver"
	"fmt"
	"strings"
)

// DB2 BOOLEAN wire encoding (matches JTOpen SQLBoolean): one byte, 0xF1 for
// true and 0xF0 for false. On decode, any value other than 0xF0 is true.
const (
	booleanWireTrue  byte = 0xF1
	booleanWireFalse byte = 0xF0
)

// decodeBooleanValue decodes a one-byte DB2 BOOLEAN value.
func decodeBooleanValue(raw []byte) (driver.Value, error) {
	if len(raw) < 1 {
		return nil, fmt.Errorf("BOOLEAN value requires 1 byte, got %d", len(raw))
	}
	return raw[0] != booleanWireFalse, nil
}

// encodeBooleanParameter encodes a bind value as a one-byte DB2 BOOLEAN.
// bool and int64 (0 is false, non-zero is true) are accepted, as are strings
// "true", "false", "1", "0" (case-insensitive, surrounding spaces ignored).
func encodeBooleanParameter(value driver.Value) ([]byte, error) {
	var v bool
	switch typed := value.(type) {
	case bool:
		v = typed
	case int64:
		v = typed != 0
	case string:
		parsed, err := parseBooleanString(typed)
		if err != nil {
			return nil, err
		}
		v = parsed
	default:
		return nil, errPreparedStatementBindingUnsupported
	}
	if v {
		return []byte{booleanWireTrue}, nil
	}
	return []byte{booleanWireFalse}, nil
}

func parseBooleanString(text string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("invalid BOOLEAN parameter %q: want true, false, 1 or 0", text)
	}
}

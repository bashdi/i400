package i400

import (
	"database/sql/driver"
	"encoding/binary"
	"fmt"
)

// isInlineLOBParameterField reports whether a LOB parameter marker is sent
// inline (the server did not return an input locator). JTOpen's SQLClob and
// SQLBlob carry the value in the row itself: a 4-byte length followed by the
// data, in a field whose length comes from the parameter marker format.
func isInlineLOBParameterField(field parameterMarkerField) bool {
	if field.LOBLocator > 0 || field.LOBMaxSize <= 0 {
		return false
	}
	switch parameterFieldDB2Type(field) {
	case db2TypeBlob, db2TypeClob, db2TypeDbclob:
		return true
	default:
		return false
	}
}

// encodeInlineLOBParameterValue encodes a LOB parameter value inline. The
// length prefix is in bytes for BLOB and CLOB and in characters for DBCLOB,
// matching JTOpen.
func encodeInlineLOBParameterValue(field parameterMarkerField, value any, hasInputValue bool, isNull bool) ([]byte, error) {
	size := field.Length
	if size <= 0 {
		size = field.LOBMaxSize + 4
	}
	raw := make([]byte, size)
	if !hasInputValue || isNull || value == nil {
		return raw, nil
	}

	valueArg, ok := value.(driver.Value)
	if !ok {
		return nil, errPreparedStatementBindingUnsupported
	}

	var data []byte
	var length int
	var err error
	switch parameterFieldDB2Type(field) {
	case db2TypeBlob:
		data, err = parameterFieldBinaryBytes(valueArg)
		length = len(data)
	case db2TypeClob:
		var text string
		text, err = parameterFieldValueString(valueArg)
		if err == nil {
			data, err = encodeTextWithBidi(text, field.CCSID, field.textBidi())
			length = len(data)
		}
	case db2TypeDbclob:
		var text string
		text, err = parameterFieldValueString(valueArg)
		if err == nil {
			data, err = encodeTextWithBidi(text, field.CCSID, field.textBidi())
			length = len(data) / 2
		}
	default:
		return nil, errPreparedStatementBindingUnsupported
	}
	if err != nil {
		return nil, err
	}
	if len(data) > size-4 {
		return nil, fmt.Errorf("LOB value of %d bytes exceeds the inline maximum of %d bytes", len(data), size-4)
	}
	binary.BigEndian.PutUint32(raw, uint32(length))
	copy(raw[4:], data)
	return raw, nil
}

// inlineLOBColumnData strips the 4-byte length prefix (and the padding after
// the value) from an inline BLOB, CLOB or DBCLOB column, as JTOpen's SQLClob
// and SQLBlob do in convertFromRawBytes. Other columns are returned unchanged.
func inlineLOBColumnData(col columnMeta, raw []byte) ([]byte, error) {
	switch col.db2Type() {
	case db2TypeBlob, db2TypeClob, db2TypeDbclob:
	default:
		return raw, nil
	}
	if len(raw) < 4 {
		return nil, fmt.Errorf("inline LOB value too short: %d bytes", len(raw))
	}
	length := int(binary.BigEndian.Uint32(raw[:4]))
	if col.db2Type() == db2TypeDbclob {
		length *= 2
	}
	if length < 0 || length > len(raw)-4 {
		return nil, fmt.Errorf("inline LOB length %d exceeds %d bytes", length, len(raw)-4)
	}
	return raw[4 : 4+length], nil
}

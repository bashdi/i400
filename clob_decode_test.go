package i400

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

// lobColumn builds an inline (non-locator) LOB column at offset 0 of a row.
// Inline BLOB, CLOB and DBCLOB values carry a 4-byte big-endian length prefix
// ahead of the data (bytes for BLOB/CLOB, characters for DBCLOB), as JTOpen's
// SQLBlob and SQLClob read them. XML columns are not prefixed in this path.
func lobColumn(sqlType, ccsidID int, raw []byte) (columnMeta, []byte) {
	row := raw
	switch sqlType {
	case db2TypeBlob, db2TypeClob:
		row = inlinePrefixed(len(raw), raw)
	case db2TypeDbclob:
		row = inlinePrefixed(len(raw)/2, raw)
	}
	return columnMeta{Type: sqlType, Length: len(row), CCSID: ccsidID, Offset: 0}, row
}

func inlinePrefixed(count int, data []byte) []byte {
	row := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(row, uint32(count))
	copy(row[4:], data)
	return row
}

func mustEncodeText(t *testing.T, text string, ccsidID int) []byte {
	t.Helper()
	raw, err := encodeTextBytes(text, ccsidID)
	if err != nil {
		t.Fatalf("encodeTextBytes(%q, %d) error = %v", text, ccsidID, err)
	}
	return raw
}

func TestDecodeClobEBCDIC37ToString(t *testing.T) {
	raw := mustEncodeText(t, "Hello CLOB", 37)
	col, row := lobColumn(db2TypeClob, 37, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	got, ok := value.(string)
	if !ok {
		t.Fatalf("value type = %T, want string", value)
	}
	if got != "Hello CLOB" {
		t.Fatalf("value = %q, want %q", got, "Hello CLOB")
	}
}

func TestDecodeDbclobUTF16ToString(t *testing.T) {
	raw := mustEncodeText(t, "graphic text", 1200)
	col, row := lobColumn(db2TypeDbclob, 1200, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if got, ok := value.(string); !ok || got != "graphic text" {
		t.Fatalf("value = %#v, want string %q", value, "graphic text")
	}
}

func TestDecodeXMLUTF8ToString(t *testing.T) {
	raw := mustEncodeText(t, "<root><val>1</val></root>", 1208)
	col, row := lobColumn(db2TypeXML, 1208, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if got, ok := value.(string); !ok || got != "<root><val>1</val></root>" {
		t.Fatalf("value = %#v, want string xml", value)
	}
}

func TestDecodeXMLStripsUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, mustEncodeText(t, "<a/>", 1208)...)
	col, row := lobColumn(db2TypeXML, 1208, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if got, ok := value.(string); !ok || got != "<a/>" {
		t.Fatalf("value = %#v, want string %q", value, "<a/>")
	}
}

func TestDecodeClob65535StaysBytes(t *testing.T) {
	raw := []byte{0xC8, 0x85, 0x93}
	col, row := lobColumn(db2TypeClob, 65535, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	got, ok := value.([]byte)
	if !ok {
		t.Fatalf("value type = %T, want []byte", value)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("value = % x, want % x", got, raw)
	}
}

func TestDecodeXML65535StaysBytes(t *testing.T) {
	raw := []byte{0x00, 0x01, 0x02}
	col, row := lobColumn(db2TypeXML, 65535, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if _, ok := value.([]byte); !ok {
		t.Fatalf("value type = %T, want []byte", value)
	}
}

func TestDecodeBlobStaysBytes(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03}
	col, row := lobColumn(db2TypeBlob, 65535, raw)

	value, err := decodeColumnValue(col, row)
	if err != nil {
		t.Fatalf("decodeColumnValue() error = %v", err)
	}
	if got, ok := value.([]byte); !ok || !bytes.Equal(got, raw) {
		t.Fatalf("value = %#v, want bytes % x", value, raw)
	}
}

func TestDecodeLobTextValueLocatorBytes(t *testing.T) {
	// Locator retrieval hands the raw LOB bytes to decodeLobTextValue.
	raw := mustEncodeText(t, "from locator", 37)
	value, err := decodeLobTextValue(raw, columnMeta{Type: db2TypeClobLocator, CCSID: 37})
	if err != nil {
		t.Fatalf("decodeLobTextValue() error = %v", err)
	}
	if got, ok := value.(string); !ok || got != "from locator" {
		t.Fatalf("value = %#v, want string %q", value, "from locator")
	}

	dbcs := mustEncodeText(t, "dbcs locator", 1200)
	value, err = decodeLobTextValue(dbcs, columnMeta{Type: db2TypeDbclobLocator, CCSID: 1200})
	if err != nil {
		t.Fatalf("decodeLobTextValue() error = %v", err)
	}
	if got, ok := value.(string); !ok || got != "dbcs locator" {
		t.Fatalf("value = %#v, want string %q", value, "dbcs locator")
	}
}

func TestLobColumnScanTypes(t *testing.T) {
	stringType := reflect.TypeOf("")
	bytesType := reflect.TypeOf([]byte(nil))
	tests := []struct {
		name string
		col  columnMeta
		want reflect.Type
	}{
		{"CLOB", columnMeta{Type: db2TypeClob, CCSID: 37}, stringType},
		{"DBCLOB", columnMeta{Type: db2TypeDbclob, CCSID: 1200}, stringType},
		{"XML", columnMeta{Type: db2TypeXML, CCSID: 1208}, stringType},
		{"CLOB LOCATOR", columnMeta{Type: db2TypeClobLocator, CCSID: 37}, stringType},
		{"DBCLOB LOCATOR", columnMeta{Type: db2TypeDbclobLocator, CCSID: 1200}, stringType},
		{"XML LOCATOR", columnMeta{Type: db2TypeXMLLocator, CCSID: 1208}, stringType},
		{"CLOB 65535", columnMeta{Type: db2TypeClob, CCSID: 65535}, bytesType},
		{"XML 65535", columnMeta{Type: db2TypeXML, CCSID: 65535}, bytesType},
		{"BLOB", columnMeta{Type: db2TypeBlob, CCSID: 65535}, bytesType},
		{"BLOB LOCATOR", columnMeta{Type: db2TypeBlobLocator, CCSID: 65535}, bytesType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := columnScanType(tt.col); got != tt.want {
				t.Fatalf("columnScanType() = %v, want %v", got, tt.want)
			}
		})
	}
}

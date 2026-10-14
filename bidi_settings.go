package i400

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/bashdi/i400/internal/bidi"
	"github.com/bashdi/i400/internal/ccsid"
)

// Bidi layout of character data, following JTOpen's JDBC driver.
//
// JTOpen converts the CCSIDs that have a bidi table (ConvTableBidiMap) with a layout step
// after (read) or before (write) the single-byte table mapping. The JDBC driver chooses the
// string type per column (SQLChar): the connection setting "bidi string type", or, when the
// setting is not set (-1), the string type of the column's CCSID. The implicit reordering and
// numeric ordering settings are passed on as they are. The SQL statement text and the column
// names get their own layout steps (JDSQLStatement, JDServerRow).
//
// Deviations from JTOpen are in internal/bidi (layout.go, as400transform.go): the stale tail of
// the layout is padded with spaces (4b), and the flag set cache is not shared (4a).

// defaultBidiStringType is JTOpen's default for "bidi string type" (JDProperties: ST5).
const defaultBidiStringType = 5

// bidiStringTypeNotSet is the setting for "not set" (JTOpen's getInt("") = -1). The column's
// CCSID string type is used.
const bidiStringTypeNotSet = -1

// validBidiStringType reports whether t is a DSN value of bidiStringType: -1 (not set), 0 (default) or 4 to 11.
func validBidiStringType(t int) bool {
	return t == bidiStringTypeNotSet || t == bidi.StringTypeDefault || (t >= bidi.StringTypeST4 && t <= bidi.StringTypeST11)
}

// parseBidiStringTypeOption parses the DSN value of bidiStringType. "none" is accepted for -1.
// As in JTOpen, -1 means not set: the CCSID's string type is used, so no layout is turned off.
func parseBidiStringTypeOption(value string) (int, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "none" || trimmed == "notset" {
		return bidiStringTypeNotSet, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	if !validBidiStringType(parsed) {
		return 0, fmt.Errorf("bidi string type %d out of range", parsed)
	}
	return parsed, nil
}

// bidiSettings holds the bidi connection settings of a Config.
type bidiSettings struct {
	// StringType is the DSN bidiStringType (0, 4 to 11, or -1 for not set).
	StringType         int
	ImplicitReordering bool
	NumericOrdering    bool
}

// defaultBidiSettings returns JTOpen's JDBC defaults: string type ST5, implicit reordering on,
// numeric ordering off.
func defaultBidiSettings() bidiSettings {
	return bidiSettings{StringType: defaultBidiStringType, ImplicitReordering: true}
}

// bidiSettingsFromConfig returns the bidi settings of cfg.
func bidiSettingsFromConfig(cfg Config) bidiSettings {
	return bidiSettings{
		StringType:         cfg.BidiStringType,
		ImplicitReordering: cfg.BidiImplicitReordering,
		NumericOrdering:    cfg.BidiNumericOrdering,
	}
}

// bidiSettingsForConn returns the bidi settings of conn, or the JTOpen defaults when conn is nil.
func bidiSettingsForConn(conn *Conn) bidiSettings {
	if conn == nil {
		return defaultBidiSettings()
	}
	return bidiSettingsFromConfig(conn.cfg)
}

// properties returns the conversion properties of a column with CCSID id, built as JTOpen's
// SQLChar builds them: a "not set" string type takes the CCSID's string type.
func (s bidiSettings) properties(id int) bidi.Properties {
	stringType := s.StringType
	if stringType == bidiStringTypeNotSet {
		stringType = bidi.StringTypeForCCSID(id)
	}
	p := bidi.DefaultProperties()
	p.StringType = stringType
	p.NoImplicitReordering = !s.ImplicitReordering
	p.NumericOrderingRoundTrip = s.NumericOrdering
	return p
}

// decodeTextWithBidi decodes single-byte text of CCSID id with the bidi layout applied, as
// JTOpen's ConvTableBidiMap does. Other CCSIDs decode as decodeTextByCCSID.
func decodeTextWithBidi(data []byte, id int, bs bidiSettings) (string, error) {
	ccsidID := textCCSID(id)
	if !bidi.IsConversionBidiCCSID(ccsidID) {
		return decodeTextByCCSID(data, id)
	}
	if len(data) == 0 {
		return "", nil
	}
	units, err := ccsid.DecodeUnits(data, ccsidID)
	if err != nil {
		return "", err
	}
	layout := bidi.ToLogicalPadded(units, ccsidID, bs.properties(ccsidID))
	return string(utf16.Decode(layout)), nil
}

// encodeTextWithBidi encodes text to CCSID id with the bidi layout applied, as JTOpen's
// ConvTableBidiMap does. Other CCSIDs encode as encodeTextBytes.
func encodeTextWithBidi(text string, id int, bs bidiSettings) ([]byte, error) {
	ccsidID := textCCSID(id)
	if !bidi.IsConversionBidiCCSID(ccsidID) {
		return encodeTextBytes(text, id)
	}
	units := utf16.Encode([]rune(text))
	layout := bidi.ToHostPadded(units, ccsidID, bs.properties(ccsidID))
	return ccsid.EncodeUnits(layout, ccsidID)
}

// bidiSystemInfo describes the server for the bidi string functions.
func (c *Conn) bidiSystemInfo() bidi.SystemInfo {
	if c == nil || c.systemInfo == nil {
		return bidi.SystemInfo{}
	}
	return bidi.SystemInfo{Present: true, CCSID: c.systemInfo.ServerCCSID, VRM: c.systemInfo.ServerVersion}
}

// bidiPackageCCSID returns the package CCSID that JTOpen passes to getStringTypeX.
func (c *Conn) bidiPackageCCSID() int {
	if c == nil {
		return defaultPackageCCSID
	}
	if c.cfg.PackageCCSIDSystem {
		return c.bidiSystemInfo().CCSID
	}
	if c.cfg.PackageCCSID <= 0 {
		return defaultPackageCCSID
	}
	return c.cfg.PackageCCSID
}

// bidiStatementText applies the bidi layout to an SQL statement before it is sent, as
// JDSQLStatement does (AS400BidiTransform.convertSQLToHostCCSID).
func (c *Conn) bidiStatementText(query string) string {
	bs := bidiSettingsForConn(c)
	if bs.StringType == bidiStringTypeNotSet {
		return query
	}
	units := utf16.Encode([]rune(query))
	layout := bidi.ConvertSQLToHost(units, bs.StringType, c.bidiPackageCCSID(), bs.ImplicitReordering, c.bidiSystemInfo())
	if len(layout) == len(units) {
		same := true
		for i := range units {
			if units[i] != layout[i] {
				same = false
				break
			}
		}
		if same {
			return query
		}
	}
	return string(utf16.Decode(layout))
}

// bidiColumnName applies the bidi layout to a column name of CCSID id, as JDServerRow does
// (AS400BidiTransform.convertDataFromHostCCSID). It applies only when implicit reordering is on.
func (c *Conn) bidiColumnName(name string, id int) string {
	bs := bidiSettingsForConn(c)
	if !bs.ImplicitReordering || bs.StringType == bidiStringTypeNotSet {
		return name
	}
	units := utf16.Encode([]rune(name))
	return string(utf16.Decode(bidi.ConvertDataFromHost(units, bs.StringType, id, c.bidiSystemInfo())))
}

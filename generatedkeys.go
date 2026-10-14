package i400

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Generated keys (identity values) for sql.Result.LastInsertId.
//
// JTOpen has two mechanisms:
//   - Below V6R1 (and for any server that fills the key), the server returns the
//     last generated key in the SQLCA errmc area, bytes 72..87, as a 30,0 packed
//     decimal. JTOpen's AS400JDBCStatement reads it with DBReplySQLCA.getGeneratedKey
//     after a plain execute. No request code point or open attribute is needed for
//     this path; the server populates it for INSERT statements. The driver reads the
//     same bytes in parseSQLCAFromPayload (sql.go).
//   - V6R1 and later, JTOpen wraps the INSERT as
//     "SELECT *SQLGENCOLUMNS FROM NEW TABLE(<insert>)" (AS400JDBCConnectionImpl
//     makeGeneratedKeySelectStatement), runs it as a read-only, insensitive query
//     (open attribute 0x80) and reads the generated columns from the result set.
//     NEW TABLE is used rather than FINAL TABLE so AFTER INSERT triggers do not
//     break the statement.
//
// The SQLCA path is always active. The wrapping path changes the SQL that runs, so
// it is only used when the DSN option returnGeneratedKeys=true is set.

// errNoGeneratedKey is returned by LastInsertId when the statement did not report a key.
func errNoGeneratedKey() error {
	return fmt.Errorf("%w: no generated key was returned for this statement (LastInsertId needs an INSERT into a table with an identity or generated column; with the DSN option returnGeneratedKeys=true the INSERT is wrapped to read the key)", ErrUnsupported)
}

// isInsertStatement reports whether the SQL text starts with INSERT after leading
// whitespace and comments.
func isInsertStatement(query string) bool {
	text := skipLeadingSQLNoise(query)
	return len(text) >= len("INSERT") && strings.EqualFold(text[:len("INSERT")], "INSERT") &&
		(len(text) == len("INSERT") || isSQLDelimiter(text[len("INSERT")]))
}

func isSQLDelimiter(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '(' || b == '/' || b == '-'
}

// skipLeadingSQLNoise removes leading whitespace, "--" line comments and "/* */"
// block comments.
func skipLeadingSQLNoise(query string) string {
	s := query
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "--"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return ""
			}
			s = s[i+1:]
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s[2:], "*/")
			if i < 0 {
				return ""
			}
			s = s[2+i+2:]
		default:
			return s
		}
	}
}

// wrapInsertForGeneratedKeys returns the JTOpen-style wrapped statement that returns
// the inserted row's generated columns. Only INSERT statements can be wrapped.
// Leading and trailing whitespace and one trailing semicolon are removed. The
// closing parenthesis goes on its own line so a trailing line comment cannot
// swallow it.
func wrapInsertForGeneratedKeys(query string) (string, error) {
	if !isInsertStatement(query) {
		return "", fmt.Errorf("%w: returnGeneratedKeys applies only to INSERT statements", ErrUnsupported)
	}
	body := strings.TrimLeftFunc(query, unicode.IsSpace)
	body = strings.TrimRightFunc(body, unicode.IsSpace)
	body = strings.TrimSuffix(body, ";")
	body = strings.TrimRightFunc(body, unicode.IsSpace)
	return "SELECT *SQLGENCOLUMNS FROM NEW TABLE(\n" + body + "\n)", nil
}

// execInsertWithGeneratedKey runs an INSERT wrapped in SELECT *SQLGENCOLUMNS FROM
// NEW TABLE(...) and reports the key from the first column of the last returned row.
// RowsAffected is the number of rows returned.
func (c *Conn) execInsertWithGeneratedKey(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	wrapped, err := wrapInsertForGeneratedKeys(query)
	if err != nil {
		return nil, err
	}
	rows, err := c.QueryContext(ctx, wrapped, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return readGeneratedKeyRows(rows)
}

// readGeneratedKeyRows drains rows from a wrapped INSERT. The first column of each
// row is the generated key; the last row's key is reported.
func readGeneratedKeyRows(rows driver.Rows) (driver.Result, error) {
	if len(rows.Columns()) == 0 {
		return nil, fmt.Errorf("generated keys: wrapped INSERT returned no columns")
	}
	dest := make([]driver.Value, len(rows.Columns()))
	result := &execResult{}
	for {
		err := rows.Next(dest)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		result.rowsAffected++
		key, err := generatedKeyInt64(dest[0])
		if err != nil {
			return nil, fmt.Errorf("generated keys: %w", err)
		}
		result.lastInsertID = key
		result.hasLastInsert = true
	}
	if !result.hasLastInsert {
		result.lastInsertID = 0
	}
	return result, nil
}

// generatedKeyInt64 converts a generated key column value to int64.
func generatedKeyInt64(v driver.Value) (int64, error) {
	switch value := v.(type) {
	case int64:
		return value, nil
	case int32:
		return int64(value), nil
	case int:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case []byte:
		return parseGeneratedKeyText(string(value))
	case string:
		return parseGeneratedKeyText(value)
	case nil:
		return 0, fmt.Errorf("generated key is NULL")
	default:
		return 0, fmt.Errorf("generated key has unsupported type %T", v)
	}
}

func parseGeneratedKeyText(text string) (int64, error) {
	key, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("generated key %q is not an integer", text)
	}
	return key, nil
}

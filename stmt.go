package i400

import (
	"context"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stmt struct {
	conn                  *Conn
	query                 string
	statementName         string
	statementHandle       uint16
	statementType         int
	parameterMarkerFormat *parameterMarkerFormat
	// paramNames holds the :name placeholder names in the order they appear in
	// the rewritten (positional ?) query. Non-nil only for named-parameter queries.
	paramNames []string
	mu         sync.RWMutex
	cond       *sync.Cond
	closed     bool
	active     int
}

var _ driver.Stmt = (*Stmt)(nil)
var _ driver.StmtExecContext = (*Stmt)(nil)
var _ driver.StmtQueryContext = (*Stmt)(nil)

func newStmt(conn *Conn, query, statementName string, statementHandle uint16, statementType int, parameterMarkerFormat *parameterMarkerFormat, paramNames []string) *Stmt {
	return &Stmt{
		conn:                  conn,
		query:                 query,
		statementName:         statementName,
		statementHandle:       statementHandle,
		statementType:         statementType,
		parameterMarkerFormat: parameterMarkerFormat,
		paramNames:            paramNames,
	}
}

func (s *Stmt) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.cond == nil {
		s.cond = sync.NewCond(&s.mu)
	}
	for s.active > 0 {
		s.cond.Wait()
	}
	conn := s.conn
	statementName := s.statementName
	s.mu.Unlock()

	if conn == nil || statementName == "" || !conn.IsValid() {
		return nil
	}
	return conn.closePreparedStatement(context.Background(), statementName)
}

func (s *Stmt) acquire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cond == nil {
		s.cond = sync.NewCond(&s.mu)
	}
	if s.closed {
		return ErrConnectionClosed
	}
	s.active++
	return nil
}

func (s *Stmt) release() {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	if s.cond != nil {
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

func (s *Stmt) NumInput() int {
	if s == nil {
		return -1
	}
	if s.parameterMarkerFormat != nil {
		return s.parameterMarkerFormat.parameterCount()
	}
	count, err := countSQLPlaceholders(s.query)
	if err != nil {
		return -1
	}
	return count
}

func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	namedArgs := make([]driver.NamedValue, len(args))
	for i, value := range args {
		namedArgs[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return s.ExecContext(context.Background(), namedArgs)
}

func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	namedArgs := make([]driver.NamedValue, len(args))
	for i, value := range args {
		namedArgs[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return s.QueryContext(context.Background(), namedArgs)
}

func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if s == nil {
		return nil, ErrConnectionClosed
	}
	if err := s.acquire(); err != nil {
		return nil, err
	}
	defer s.release()
	if s.conn == nil || !s.conn.IsValid() {
		s.release()
		return nil, driver.ErrBadConn
	}
	if s.statementName != "" && s.parameterMarkerFormat != nil {
		ordered, err := reorderNamedArgs(s.paramNames, args)
		if err != nil {
			return nil, err
		}
		return s.conn.execPrepared(ctx, s.statementHandle, s.statementType, s.parameterMarkerFormat, ordered)
	}
	if len(args) == 0 {
		return s.conn.execContext(ctx, s.query, nil)
	}
	return nil, errPreparedStatementBindingUnsupported
}

func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if s == nil {
		return nil, ErrConnectionClosed
	}
	if err := s.acquire(); err != nil {
		return nil, err
	}
	if s.conn == nil || !s.conn.IsValid() {
		s.release()
		return nil, driver.ErrBadConn
	}
	if s.statementName != "" && len(args) == 0 {
		rows, err := s.conn.queryPrepared(ctx, s.statementHandle, s.statementName)
		if err != nil {
			s.release()
			return nil, err
		}
		if preparedRows, ok := rows.(*queryRows); ok {
			preparedRows.releaseStmt = s.release
			return rows, nil
		}
		s.release()
		return rows, nil
	}
	if s.statementName != "" && s.parameterMarkerFormat != nil && len(args) > 0 {
		ordered, err := reorderNamedArgs(s.paramNames, args)
		if err != nil {
			return nil, err
		}
		rows, err := s.conn.queryPreparedWithArgs(ctx, s.statementHandle, s.statementName, s.parameterMarkerFormat, ordered)
		if err != nil {
			s.release()
			return nil, err
		}
		if preparedRows, ok := rows.(*queryRows); ok {
			preparedRows.releaseStmt = s.release
			return rows, nil
		}
		s.release()
		return rows, nil
	}
	s.release()
	return nil, errPreparedStatementBindingUnsupported
}

func (s *Stmt) checkClosed() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrConnectionClosed
	}
	return nil
}

func expandSQLQuery(query string, args []driver.NamedValue) (string, error) {
	if len(args) == 0 {
		return query, nil
	}

	// Build name→value lookup for named parameters.
	byName := make(map[string]int, len(args))
	for i, a := range args {
		if a.Name != "" {
			byName[a.Name] = i
		}
	}

	var builder strings.Builder
	builder.Grow(len(query) + len(args)*8)
	argIndex := 0
	state := sqlScanNormal

	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch state {
		case sqlScanNormal:
			switch ch {
			case '\'':
				builder.WriteByte(ch)
				state = sqlScanSingleQuoted
			case '"':
				builder.WriteByte(ch)
				state = sqlScanDoubleQuoted
			case '-':
				if i+1 < len(query) && query[i+1] == '-' {
					builder.WriteByte(ch)
					i++
					builder.WriteByte(query[i])
					state = sqlScanLineComment
					continue
				}
				builder.WriteByte(ch)
			case '/':
				if i+1 < len(query) && query[i+1] == '*' {
					builder.WriteByte(ch)
					i++
					builder.WriteByte(query[i])
					state = sqlScanBlockComment
					continue
				}
				builder.WriteByte(ch)
			case '?':
				if argIndex >= len(args) {
					return "", fmt.Errorf("parameter count mismatch: got %d values for %d placeholders", len(args), argIndex)
				}
				literal, err := sqlLiteralFromNamedValue(args[argIndex])
				if err != nil {
					return "", err
				}
				builder.WriteString(literal)
				argIndex++
			case ':':
				// Doubled colon (e.g. ::type) – pass through unchanged.
				if i+1 < len(query) && query[i+1] == ':' {
					builder.WriteByte(ch)
					i++
					builder.WriteByte(query[i])
					break
				}
				// Collect :name identifier.
				j := i + 1
				if j < len(query) && isNameStartChar(query[j]) {
					j++
					for j < len(query) && isNameContinueChar(query[j]) {
						j++
					}
					name := query[i+1 : j]
					idx, ok := byName[name]
					if !ok {
						return "", fmt.Errorf("named parameter %q not provided", name)
					}
					literal, err := sqlLiteralFromNamedValue(args[idx])
					if err != nil {
						return "", err
					}
					builder.WriteString(literal)
					i = j - 1
				} else {
					builder.WriteByte(ch)
				}
			default:
				builder.WriteByte(ch)
			}
		case sqlScanSingleQuoted:
			builder.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					i++
					builder.WriteByte(query[i])
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanDoubleQuoted:
			builder.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					i++
					builder.WriteByte(query[i])
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanLineComment:
			builder.WriteByte(ch)
			if ch == '\n' {
				state = sqlScanNormal
			}
		case sqlScanBlockComment:
			builder.WriteByte(ch)
			if ch == '*' && i+1 < len(query) && query[i+1] == '/' {
				i++
				builder.WriteByte(query[i])
				state = sqlScanNormal
			}
		}
	}

	if state == sqlScanSingleQuoted || state == sqlScanDoubleQuoted || state == sqlScanBlockComment {
		return "", fmt.Errorf("unterminated SQL literal or comment")
	}
	// Only enforce count check when using positional ? placeholders.
	if len(byName) == 0 && argIndex != len(args) {
		return "", fmt.Errorf("parameter count mismatch: got %d values for %d placeholders", len(args), argIndex)
	}

	return builder.String(), nil
}

func countSQLPlaceholders(query string) (int, error) {
	count := 0
	state := sqlScanNormal

	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch state {
		case sqlScanNormal:
			switch ch {
			case '\'':
				state = sqlScanSingleQuoted
			case '"':
				state = sqlScanDoubleQuoted
			case '-':
				if i+1 < len(query) && query[i+1] == '-' {
					i++
					state = sqlScanLineComment
				}
			case '/':
				if i+1 < len(query) && query[i+1] == '*' {
					i++
					state = sqlScanBlockComment
				}
			case '?':
				count++
			case ':':
				// Doubled colon – skip both bytes, not a placeholder.
				if i+1 < len(query) && query[i+1] == ':' {
					i++
					break
				}
				// :name placeholder
				j := i + 1
				if j < len(query) && isNameStartChar(query[j]) {
					j++
					for j < len(query) && isNameContinueChar(query[j]) {
						j++
					}
					count++
					i = j - 1
				}
			}
		case sqlScanSingleQuoted:
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					i++
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanDoubleQuoted:
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					i++
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanLineComment:
			if ch == '\n' {
				state = sqlScanNormal
			}
		case sqlScanBlockComment:
			if ch == '*' && i+1 < len(query) && query[i+1] == '/' {
				i++
				state = sqlScanNormal
			}
		}
	}

	if state == sqlScanSingleQuoted || state == sqlScanDoubleQuoted || state == sqlScanBlockComment {
		return 0, fmt.Errorf("unterminated SQL literal or comment")
	}
	return count, nil
}

func sqlLiteralFromNamedValue(value driver.NamedValue) (string, error) {
	normalized, err := driver.DefaultParameterConverter.ConvertValue(value.Value)
	if err != nil {
		return "", err
	}

	switch v := normalized.(type) {
	case nil:
		return "NULL", nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case bool:
		if v {
			return "1", nil
		}
		return "0", nil
	case string:
		return "'" + strings.ReplaceAll(v, "'", "''") + "'", nil
	case []byte:
		return "X'" + strings.ToUpper(hex.EncodeToString(v)) + "'", nil
	case time.Time:
		return "TIMESTAMP('" + v.Format("2006-01-02-15.04.05.999999999") + "')", nil
	default:
		return "", fmt.Errorf("unsupported parameter type %T", v)
	}
}

type sqlScanState int

const (
	sqlScanNormal sqlScanState = iota
	sqlScanSingleQuoted
	sqlScanDoubleQuoted
	sqlScanLineComment
	sqlScanBlockComment
)

// isNameStartChar reports whether ch can be the first character of a :name placeholder.
func isNameStartChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

// isNameContinueChar reports whether ch can be a continuation character of a :name placeholder.
func isNameContinueChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}

// rewriteNamedParams rewrites :name placeholder tokens in query to positional ?
// markers and returns the ordered list of parameter names. When no :name
// tokens are found the original query string is returned unchanged with a nil
// paramNames slice. Doubled colons (::) are passed through unchanged.
func rewriteNamedParams(query string) (rewritten string, paramNames []string, err error) {
	// Fast path: no colon in the query at all.
	hasColon := false
	for i := 0; i < len(query); i++ {
		if query[i] == ':' {
			hasColon = true
			break
		}
	}
	if !hasColon {
		return query, nil, nil
	}

	var builder strings.Builder
	builder.Grow(len(query))
	state := sqlScanNormal

	for i := 0; i < len(query); i++ {
		ch := query[i]
		switch state {
		case sqlScanNormal:
			switch ch {
			case '\'':
				builder.WriteByte(ch)
				state = sqlScanSingleQuoted
			case '"':
				builder.WriteByte(ch)
				state = sqlScanDoubleQuoted
			case '-':
				builder.WriteByte(ch)
				if i+1 < len(query) && query[i+1] == '-' {
					i++
					builder.WriteByte(query[i])
					state = sqlScanLineComment
				}
			case '/':
				builder.WriteByte(ch)
				if i+1 < len(query) && query[i+1] == '*' {
					i++
					builder.WriteByte(query[i])
					state = sqlScanBlockComment
				}
			case ':':
				if i+1 < len(query) && query[i+1] == ':' {
					// Doubled colon – pass through.
					builder.WriteByte(ch)
					i++
					builder.WriteByte(query[i])
					break
				}
				j := i + 1
				if j < len(query) && isNameStartChar(query[j]) {
					j++
					for j < len(query) && isNameContinueChar(query[j]) {
						j++
					}
					paramNames = append(paramNames, query[i+1:j])
					builder.WriteByte('?')
					i = j - 1
				} else {
					builder.WriteByte(ch)
				}
			default:
				builder.WriteByte(ch)
			}
		case sqlScanSingleQuoted:
			builder.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					i++
					builder.WriteByte(query[i])
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanDoubleQuoted:
			builder.WriteByte(ch)
			if ch == '"' {
				if i+1 < len(query) && query[i+1] == '"' {
					i++
					builder.WriteByte(query[i])
				} else {
					state = sqlScanNormal
				}
			}
		case sqlScanLineComment:
			builder.WriteByte(ch)
			if ch == '\n' {
				state = sqlScanNormal
			}
		case sqlScanBlockComment:
			builder.WriteByte(ch)
			if ch == '*' && i+1 < len(query) && query[i+1] == '/' {
				i++
				builder.WriteByte(query[i])
				state = sqlScanNormal
			}
		}
	}

	if state == sqlScanSingleQuoted || state == sqlScanDoubleQuoted || state == sqlScanBlockComment {
		return "", nil, fmt.Errorf("unterminated SQL literal or comment")
	}
	if len(paramNames) == 0 {
		return query, nil, nil
	}
	return builder.String(), paramNames, nil
}

// reorderNamedArgs maps named args to the positional order described by
// paramNames. When paramNames is nil the original slice is returned unchanged.
// Returns an error if a required named parameter is missing from args.
func reorderNamedArgs(paramNames []string, args []driver.NamedValue) ([]driver.NamedValue, error) {
	if len(paramNames) == 0 {
		return args, nil
	}
	byName := make(map[string]driver.NamedValue, len(args))
	for _, a := range args {
		if a.Name != "" {
			byName[a.Name] = a
		}
	}
	out := make([]driver.NamedValue, len(paramNames))
	for i, name := range paramNames {
		arg, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("named parameter %q not provided", name)
		}
		out[i] = arg
		out[i].Ordinal = i + 1
		out[i].Name = "" // strip name so downstream positional code works
	}
	return out, nil
}

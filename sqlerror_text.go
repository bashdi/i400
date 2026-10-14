package i400

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
)

// Message text support for SQL errors, mirroring JTOpen's JDError.getReason.
//
// When the SQLCA reports an error, JTOpen issues a follow-up SEND_RESULTS_SET
// request (function 0x1F00) against the same statement handle, asking the
// server for the message ID (0x3801) and the first-level text (0x3802), plus
// the second-level text (0x3803) when the connection was configured with
// errors=full. The text code points carry a CCSID that the text is encoded in.

const (
	// functionSendResultsSet is DBSQLResultSetDS.FUNCTIONID_SEND_RESULTS_SET.
	functionSendResultsSet uint16 = 0x1F00

	// ORS bitmap bits from DBBaseRequestDS (JTOpen).
	orsMessageID       uint32 = 0x40000000
	orsFirstLevelText  uint32 = 0x20000000
	orsSecondLevelText uint32 = 0x10000000

	// sqlerrmcSeparator is the X'FF' byte that separates SQLERRMC tokens.
	sqlerrmcSeparator byte = 0xFF
)

// sqlMessageText holds the message fields carried by code points 0x3801-0x3803.
type sqlMessageText struct {
	MessageID       string
	FirstLevelText  string
	SecondLevelText string
}

// sqlMessageBitmap returns the ORS bitmap JTOpen requests for error text.
func sqlMessageBitmap(secondLevel bool) uint32 {
	bitmap := orsSendReplyImmediately | orsSQLCA | orsMessageID | orsFirstLevelText
	if secondLevel {
		bitmap |= orsSecondLevelText
	}
	return bitmap
}

// buildSendResultsSetRequest builds the follow-up request that asks the server
// for the stored message data of the statement identified by rpbHandle.
func buildSendResultsSetRequest(rpbHandle uint16, bitmap uint32) []byte {
	request := buildHeader(40, 0, DatabaseServerID, 20, functionSendResultsSet)
	return appendRequestTemplate(request, bitmap, rpbHandle, 0, 0)
}

// parseSQLMessageText reads the message code points from a reply payload.
//
// Layouts (after the LL and CP fields, as JTOpen DBBaseReplyDS reads them):
//   - 0x3801 message ID:          CCSID(2) text
//   - 0x3802 / 0x3803 message text: CCSID(2) stream length(2) text
//
// A length of 6 (no data) yields an empty string.
func parseSQLMessageText(payload []byte) (sqlMessageText, error) {
	var out sqlMessageText
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cp, rest, err := ParseLLCP(remaining)
		if err != nil {
			return sqlMessageText{}, err
		}
		remaining = rest
		switch codePoint {
		case CodePointMessageID:
			text, err := decodeMessageCodePoint(cp, 2)
			if err != nil {
				return sqlMessageText{}, err
			}
			out.MessageID = text
		case CodePointFirstLevelMessageText:
			text, err := decodeMessageCodePoint(cp, 4)
			if err != nil {
				return sqlMessageText{}, err
			}
			out.FirstLevelText = text
		case CodePointSecondLevelMessageText:
			text, err := decodeMessageCodePoint(cp, 4)
			if err != nil {
				return sqlMessageText{}, err
			}
			out.SecondLevelText = text
		}
	}
	return out, nil
}

// decodeMessageCodePoint decodes the text that starts at textOffset within a
// message code point payload, using the CCSID stored in its first two bytes.
func decodeMessageCodePoint(payload []byte, textOffset int) (string, error) {
	if len(payload) < textOffset {
		return "", nil
	}
	ccsidID := int(binary.BigEndian.Uint16(payload[0:2]))
	text, err := decodeTextByCCSID(payload[textOffset:], ccsidID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// applySQLMessageText copies message fields into an SQLError. The first-level
// text becomes Message when present; the existing Message is kept otherwise.
func applySQLMessageText(e *SQLError, text sqlMessageText) {
	if e == nil {
		return
	}
	e.MessageID = text.MessageID
	e.SecondLevelText = text.SecondLevelText
	if text.FirstLevelText != "" {
		e.MessageText = text.FirstLevelText
		e.Message = text.FirstLevelText
	}
}

// sqlerrmcTokens returns the SQLERRMC substitution tokens of a raw SQLCA, joined
// with single spaces. SQLERRML (offset 16) gives the used length of SQLERRMC,
// which starts at offset 18; tokens are separated by X'FF'.
func sqlerrmcTokens(sqlca []byte) string {
	if len(sqlca) < 18 {
		return ""
	}
	length := int(binary.BigEndian.Uint16(sqlca[16:18]))
	if length <= 0 {
		return ""
	}
	if length > 70 {
		length = 70
	}
	if len(sqlca) < 18+length {
		length = len(sqlca) - 18
	}
	raw := sqlca[18 : 18+length]
	var tokens []string
	for _, part := range splitBytes(raw, sqlerrmcSeparator) {
		if len(part) == 0 {
			continue
		}
		text, err := decodeTextByCCSID(part, 37)
		if err != nil {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			tokens = append(tokens, text)
		}
	}
	return strings.Join(tokens, " ")
}

func splitBytes(data []byte, sep byte) [][]byte {
	var parts [][]byte
	start := 0
	for i, b := range data {
		if b == sep {
			parts = append(parts, data[start:i])
			start = i + 1
		}
	}
	return append(parts, data[start:])
}

// newSQLError builds the SQLError for an SQLCA with SQLCODE < 0. It requests the
// message ID and text from the server (JTOpen-style follow-up) and falls back to
// the SQLCA-only error when the follow-up fails.
func (c *Conn) newSQLError(ctx context.Context, statementHandle uint16, info *sqlcaInfo) *SQLError {
	message := "SQL execution failed"
	if info.SQLState != "" {
		message = fmt.Sprintf("SQL execution failed (%s)", info.SQLState)
	}
	sqlErr := &SQLError{Code: int(info.SQLCode), State: info.SQLState, Message: message, Tokens: info.Tokens}
	if c == nil {
		return sqlErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request := buildSendResultsSetRequest(statementHandle, sqlMessageBitmap(c.cfg.ErrorsFull))
	// JTOpen reads the message data whatever the reply's error class: the
	// server reports the same SQL error on this reply and carries the message
	// code points in it. Only a transport failure leaves the SQLCA-only error.
	_, payload, err := c.doRequest(ctx, request)
	if err != nil {
		return sqlErr
	}
	text, err := parseSQLMessageText(payload)
	if err != nil {
		return sqlErr
	}
	applySQLMessageText(sqlErr, text)
	return sqlErr
}

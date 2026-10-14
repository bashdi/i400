package i400

import (
	"context"
	"fmt"
)

// JTOpen reads every database reply as an error class and a return code (see
// DBBaseReplyDS.getErrorClass and getReturnCode). The rules used here follow
// AS400JDBCStatement and JDCursor:
//
//   - class 0: success.
//   - class != 0 and return code > 0: a warning. The call succeeds and JTOpen
//     posts the warning (postWarning). Code 700 in class 2 is the server
//     telling a fetch that the cursor is already closed, so it is not a warning.
//   - class != 0 and return code < 0: an error. JTOpen calls
//     JDError.throwSQLException(connection, id, errorClass, returnCode), so the
//     return code is the SQLCODE. The SQLSTATE comes from the SQLCA, and the
//     message text comes from a follow-up request (getReason).
//
// Replies that carry an SQLCA are therefore SQL errors. A reply with an error
// envelope and no SQLCA is a host server error with no SQL diagnostics, and it
// stays a *ProtocolError.

// checkReply applies the JTOpen rules above to a reply envelope. It records a
// positive return code as a connection warning and returns nil, and it
// returns the error for a negative return code. statementHandle is the
// statement the request was for, or 0 for a connection-level request.
func (c *Conn) checkReply(ctx context.Context, statementHandle uint16, operation string, envelope replyEnvelope, payload []byte) error {
	if envelope.hasWarning() {
		if !(envelope.RCClass == 2 && envelope.RCCode == 700) {
			c.recordWarning(&SQLWarning{
				Code:    envelope.RCCode,
				Message: fmt.Sprintf("%s returned warning: class=%d code=%d", operation, envelope.RCClass, envelope.RCCode),
			})
		}
		return nil
	}
	if !envelope.hasError() {
		return nil
	}
	if !payloadHasSQLCA(payload) {
		return newProtocolError(operation, envelope)
	}
	info, err := parseSQLCAFromPayload(payload)
	if err != nil {
		return newProtocolError(operation, envelope)
	}
	// JTOpen uses the reply return code as the SQLCODE.
	info.SQLCode = envelope.RCCode
	return c.newSQLError(ctx, statementHandle, info)
}

// payloadHasSQLCA reports whether a reply payload carries an SQLCA
// (code point 0x3807).
func payloadHasSQLCA(payload []byte) bool {
	remaining := payload
	for len(remaining) > 0 {
		codePoint, _, rest, err := ParseLLCP(remaining)
		if err != nil {
			return false
		}
		if codePoint == 0x3807 {
			return true
		}
		remaining = rest
	}
	return false
}

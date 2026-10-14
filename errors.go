package i400

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidDSN       = errors.New("as400 driver: invalid DSN")
	ErrUnsupported      = errors.New("as400 driver: unsupported operation")
	ErrConnection       = errors.New("as400 driver: connection error")
	ErrConnectionClosed = errors.New("as400 driver: connection is closed")
	ErrAuthentication   = errors.New("as400 driver: authentication failed")

	// SQLSTATE class sentinels – use with errors.Is to check the error class
	// of a *SQLError without coupling to a specific error code or message.
	//
	//   var sqlErr *as400.SQLError
	//   if errors.Is(err, as400.ErrSQLNoData) { /* SQLSTATE 02xxx */ }
	//   if errors.As(err, &sqlErr) { /* access sqlErr.Code, .State, .Message */ }
	ErrSQLNoData              = errors.New("as400: no data (SQLSTATE 02xxx)")
	ErrSQLDataException       = errors.New("as400: data exception (SQLSTATE 22xxx)")
	ErrSQLConstraintViolation = errors.New("as400: constraint violation (SQLSTATE 23xxx)")
	ErrSQLInvalidCursor       = errors.New("as400: invalid cursor state (SQLSTATE 24xxx/25xxx)")
	ErrSQLSyntaxError         = errors.New("as400: syntax error or access violation (SQLSTATE 42xxx)")
)

// SQLError is an error reported by the server in the SQLCA.
//
// Message holds the first-level message text when the server returned one,
// otherwise a summary built from the SQLSTATE. MessageID (for example SQL0204),
// MessageText and SecondLevelText carry the server message as JTOpen exposes it;
// SecondLevelText is only requested when the DSN sets errors=full. Tokens holds
// the SQLERRMC substitution values from the SQLCA.
type SQLError struct {
	Code            int
	State           string
	Message         string
	MessageID       string
	MessageText     string
	SecondLevelText string
	Tokens          string
}

// ProtocolError describes an error returned by the IBM i host-server
// protocol outside the SQLCA payload.
type ProtocolError struct {
	Operation string
	Class     uint16
	Code      int32
}

// BindingError describes a value that cannot be bound to a prepared parameter.
type BindingError struct {
	Parameter int
	Operation string
	Err       error
}

// WireError describes malformed or unexpected host-server protocol data.
type WireError struct {
	Operation string
	Err       error
}

func (e *WireError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("as400 wire error during %s: %v", e.Operation, e.Err)
}

func (e *WireError) Unwrap() error { return e.Err }

// TransportError describes a network or TLS failure while connecting or communicating.
type TransportError struct {
	Operation string
	Err       error
}

func (e *TransportError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("as400 transport error during %s: %v", e.Operation, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

func (e *BindingError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("as400 parameter binding error at parameter %d", e.Parameter)
	}
	return fmt.Sprintf("as400 parameter binding error at parameter %d during %s: %v", e.Parameter, e.Operation, e.Err)
}

func (e *BindingError) Unwrap() error { return e.Err }

func (e *ProtocolError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("as400 protocol error during %s: class=%d code=0x%x", e.Operation, e.Class, uint32(e.Code))
}

func newProtocolError(operation string, envelope replyEnvelope) error {
	return &ProtocolError{Operation: operation, Class: envelope.RCClass, Code: envelope.RCCode}
}

// recordDeleteDescriptorFailure records a failed descriptor cleanup as a
// connection warning. Cleanup runs after the statement has already produced its
// result, so the failure must not replace that result; the warning is visible
// through Conn.Warnings instead.
func (c *Conn) recordDeleteDescriptorFailure(err error, envelope replyEnvelope) {
	switch {
	case err != nil:
		c.recordWarning(&SQLWarning{Message: fmt.Sprintf("delete descriptor request failed: %v", err)})
	case envelope.hasError():
		c.recordWarning(&SQLWarning{
			Code:    envelope.RCCode,
			Message: fmt.Sprintf("delete descriptor request failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode)),
		})
	}
}

func newWireError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &WireError{Operation: operation, Err: err}
}

type SQLWarning struct {
	Code    int32
	State   string
	Message string
	Next    *SQLWarning
}

func cloneSQLWarningChain(head *SQLWarning) *SQLWarning {
	if head == nil {
		return nil
	}
	cloneHead := &SQLWarning{Code: head.Code, State: head.State, Message: head.Message}
	cloneTail := cloneHead
	for current := head.Next; current != nil; current = current.Next {
		cloneTail.Next = &SQLWarning{Code: current.Code, State: current.State, Message: current.Message}
		cloneTail = cloneTail.Next
	}
	return cloneHead
}

func (e *SQLError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.MessageID != "" || e.MessageText != "" {
		// JTOpen style: "[SQL0204] text second-level (SQLSTATE 42704, SQLCODE -204)".
		var b strings.Builder
		if e.MessageID != "" {
			b.WriteString("[" + e.MessageID + "] ")
		}
		text := e.MessageText
		if text == "" {
			text = e.Message
		}
		b.WriteString(text)
		if e.SecondLevelText != "" {
			b.WriteString(" " + e.SecondLevelText)
		}
		if e.State != "" {
			fmt.Fprintf(&b, " (SQLSTATE %s, SQLCODE %d)", e.State, e.Code)
		} else {
			fmt.Fprintf(&b, " (SQLCODE %d)", e.Code)
		}
		return b.String()
	}
	if e.State == "" {
		return fmt.Sprintf("as400 sql error %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("as400 sql error %d (%s): %s", e.Code, e.State, e.Message)
}

// Is enables errors.Is comparisons against the SQLSTATE class sentinel errors
// (ErrSQLNoData, ErrSQLDataException, ErrSQLConstraintViolation,
// ErrSQLInvalidCursor, ErrSQLSyntaxError). The first two characters of the
// SQLSTATE value define the class.
func (e *SQLError) Is(target error) bool {
	if e == nil || len(e.State) < 2 {
		return false
	}
	class := e.State[:2]
	switch target {
	case ErrSQLNoData:
		return class == "02"
	case ErrSQLDataException:
		return class == "22"
	case ErrSQLConstraintViolation:
		return class == "23"
	case ErrSQLInvalidCursor:
		return class == "24" || class == "25"
	case ErrSQLSyntaxError:
		return class == "42"
	}
	return false
}

func unsupported(op string) error {
	if op == "" {
		return ErrUnsupported
	}
	return fmt.Errorf("%w: %s", ErrUnsupported, op)
}

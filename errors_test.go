package i400

import (
	"errors"
	"testing"
)

func TestBindingErrorExposesParameterDetails(t *testing.T) {
	err := bindingError(1, "parameter encoding", ErrUnsupported)
	var bindingErr *BindingError
	if !errors.As(err, &bindingErr) {
		t.Fatalf("errors.As() failed for %T", err)
	}
	if bindingErr.Parameter != 2 || bindingErr.Operation != "parameter encoding" {
		t.Fatalf("BindingError = %+v, want parameter 2 and encoding operation", bindingErr)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("errors.Is() = false, want ErrUnsupported")
	}
}

func TestProtocolErrorExposesReplyDetails(t *testing.T) {
	want := &ProtocolError{Operation: "execute", Class: 2, Code: -901}
	err := newProtocolError("execute", replyEnvelope{RCClass: 2, RCCode: -901})

	var got *ProtocolError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As() failed for %T", err)
	}
	if got.Operation != want.Operation || got.Class != want.Class || got.Code != want.Code {
		t.Fatalf("ProtocolError = %+v, want %+v", got, want)
	}
	if got.Error() == "" {
		t.Fatal("ProtocolError.Error() returned an empty message")
	}
}

func TestWireAndTransportErrorsUnwrap(t *testing.T) {
	inner := errors.New("inner")
	for _, err := range []error{
		&WireError{Operation: "read", Err: inner},
		&TransportError{Operation: "dial", Err: inner},
	} {
		if !errors.Is(err, inner) {
			t.Fatalf("errors.Is(%T, inner) = false", err)
		}
	}
}

func TestNewWireErrorPreservesCause(t *testing.T) {
	inner := errors.New("malformed payload")
	err := newWireError("parse LOB data", inner)
	var wireErr *WireError
	if !errors.As(err, &wireErr) || wireErr.Operation != "parse LOB data" {
		t.Fatalf("errors.As() = %v, error = %v", wireErr != nil, err)
	}
	if !errors.Is(err, inner) {
		t.Fatal("newWireError() did not preserve its cause")
	}
}

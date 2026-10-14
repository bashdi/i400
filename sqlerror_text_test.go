package i400

import (
	"encoding/binary"
	"errors"
	"testing"
)

// llcpForTest builds one LL CP payload as it appears in a reply.
func llcpForTest(codePoint uint16, payload []byte) []byte {
	out := make([]byte, 6, 6+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(6+len(payload)))
	binary.BigEndian.PutUint16(out[4:6], codePoint)
	return append(out, payload...)
}

func ebcdicForTest(t *testing.T, s string) []byte {
	t.Helper()
	b, err := EncodeEBCDIC37(s)
	if err != nil {
		t.Fatalf("EncodeEBCDIC37(%q) error = %v", s, err)
	}
	return b
}

// textCodePointForTest builds a 0x3802/0x3803 payload: CCSID, stream length, text.
func textCodePointForTest(t *testing.T, codePoint uint16, text string) []byte {
	t.Helper()
	data := ebcdicForTest(t, text)
	payload := []byte{0x00, 0x25, byte(len(data) >> 8), byte(len(data))}
	return llcpForTest(codePoint, append(payload, data...))
}

func TestParseSQLMessageTextReadsMessageCodePoints(t *testing.T) {
	idPayload := append([]byte{0x00, 0x25}, ebcdicForTest(t, "SQL0204")...)
	var reply []byte
	reply = append(reply, llcpForTest(CodePointSQLCA, make([]byte, 136))...)
	reply = append(reply, llcpForTest(CodePointMessageID, idPayload)...)
	reply = append(reply, textCodePointForTest(t, CodePointFirstLevelMessageText, "MYTABLE in MYLIB type *FILE not found.")...)
	reply = append(reply, textCodePointForTest(t, CodePointSecondLevelMessageText, "Cause . . . . . : MYTABLE.")...)

	got, err := parseSQLMessageText(reply)
	if err != nil {
		t.Fatalf("parseSQLMessageText() error = %v", err)
	}
	want := sqlMessageText{
		MessageID:       "SQL0204",
		FirstLevelText:  "MYTABLE in MYLIB type *FILE not found.",
		SecondLevelText: "Cause . . . . . : MYTABLE.",
	}
	if got != want {
		t.Fatalf("parseSQLMessageText() = %+v, want %+v", got, want)
	}
}

func TestParseSQLMessageTextNoDataYieldsEmptyFields(t *testing.T) {
	// A length of 6 means the server sent the code point without data.
	reply := llcpForTest(CodePointMessageID, nil)
	got, err := parseSQLMessageText(reply)
	if err != nil {
		t.Fatalf("parseSQLMessageText() error = %v", err)
	}
	if got != (sqlMessageText{}) {
		t.Fatalf("parseSQLMessageText() = %+v, want zero value", got)
	}
}

func TestSQLMessageBitmapBasicAndFull(t *testing.T) {
	basic := sqlMessageBitmap(false)
	if want := uint32(0xE2000000); basic != want {
		t.Fatalf("basic ORS bitmap = 0x%x, want 0x%x", basic, want)
	}
	full := sqlMessageBitmap(true)
	if want := uint32(0xF2000000); full != want {
		t.Fatalf("errors=full ORS bitmap = 0x%x, want 0x%x", full, want)
	}
}

func TestBuildSendResultsSetRequestLayout(t *testing.T) {
	request := buildSendResultsSetRequest(0x0042, sqlMessageBitmap(true))
	if len(request) != 40 {
		t.Fatalf("request length = %d, want 40", len(request))
	}
	if got := binary.BigEndian.Uint32(request[0:4]); got != 40 {
		t.Fatalf("length field = %d, want 40", got)
	}
	if got := binary.BigEndian.Uint16(request[18:20]); got != functionSendResultsSet {
		t.Fatalf("request id = 0x%x, want 0x%x", got, functionSendResultsSet)
	}
	if got := binary.BigEndian.Uint32(request[20:24]); got != sqlMessageBitmap(true) {
		t.Fatalf("ORS bitmap = 0x%x, want 0x%x", got, sqlMessageBitmap(true))
	}
	if got := binary.BigEndian.Uint16(request[34:36]); got != 0x0042 {
		t.Fatalf("statement handle = 0x%x, want 0x42", got)
	}
}

func TestApplySQLMessageTextBasicError(t *testing.T) {
	e := &SQLError{Code: -204, State: "42704", Message: "SQL execution failed (42704)"}
	applySQLMessageText(e, sqlMessageText{
		MessageID:      "SQL0204",
		FirstLevelText: "MYTABLE in MYLIB type *FILE not found.",
	})
	if e.MessageID != "SQL0204" || e.MessageText != "MYTABLE in MYLIB type *FILE not found." {
		t.Fatalf("fields = %+v", e)
	}
	if e.Message != "MYTABLE in MYLIB type *FILE not found." {
		t.Fatalf("Message = %q", e.Message)
	}
	if e.SecondLevelText != "" {
		t.Fatalf("SecondLevelText = %q, want empty for basic errors", e.SecondLevelText)
	}
	want := "[SQL0204] MYTABLE in MYLIB type *FILE not found. (SQLSTATE 42704, SQLCODE -204)"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestApplySQLMessageTextFullErrorAddsSecondLevel(t *testing.T) {
	e := &SQLError{Code: -204, State: "42704", Message: "SQL execution failed (42704)"}
	applySQLMessageText(e, sqlMessageText{
		MessageID:       "SQL0204",
		FirstLevelText:  "MYTABLE in MYLIB type *FILE not found.",
		SecondLevelText: "Cause . . . . . : MYTABLE.",
	})
	want := "[SQL0204] MYTABLE in MYLIB type *FILE not found. Cause . . . . . : MYTABLE. (SQLSTATE 42704, SQLCODE -204)"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestApplySQLMessageTextWithoutTextKeepsSQLStateMessage(t *testing.T) {
	e := &SQLError{Code: -601, State: "42710", Message: "SQL execution failed (42710)"}
	applySQLMessageText(e, sqlMessageText{})
	if e.Message != "SQL execution failed (42710)" {
		t.Fatalf("Message = %q, want the SQLSTATE summary", e.Message)
	}
	if e.MessageID != "" || e.MessageText != "" {
		t.Fatalf("message fields = %+v, want empty", e)
	}
}

func TestSQLErrorWithoutMessageTextUsesLegacyFormat(t *testing.T) {
	e := &SQLError{Code: -104, State: "42601", Message: "syntax error"}
	if got, want := e.Error(), "as400 sql error -104 (42601): syntax error"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestSQLErrorMessageTextWithoutSQLState(t *testing.T) {
	e := &SQLError{Code: -999, MessageID: "CPF9999", MessageText: "Function failed."}
	if got, want := e.Error(), "[CPF9999] Function failed. (SQLCODE -999)"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestSQLErrorMessageTextKeepsSQLStateSentinels(t *testing.T) {
	e := &SQLError{Code: -204, State: "42704", MessageID: "SQL0204", MessageText: "not found."}
	if !errors.Is(e, ErrSQLSyntaxError) {
		t.Fatalf("errors.Is(ErrSQLSyntaxError) = false for SQLSTATE 42704")
	}
}

func TestSQLERRMCTokensSplitOnFF(t *testing.T) {
	sqlca := make([]byte, 136)
	tokens := append(ebcdicForTest(t, "MYTABLE"), 0xFF)
	tokens = append(tokens, ebcdicForTest(t, "MYLIB")...)
	binary.BigEndian.PutUint16(sqlca[16:18], uint16(len(tokens)))
	copy(sqlca[18:], tokens)

	if got, want := sqlerrmcTokens(sqlca), "MYTABLE MYLIB"; got != want {
		t.Fatalf("sqlerrmcTokens() = %q, want %q", got, want)
	}
}

func TestSQLERRMCTokensEmptyWhenLengthZero(t *testing.T) {
	sqlca := make([]byte, 136)
	if got := sqlerrmcTokens(sqlca); got != "" {
		t.Fatalf("sqlerrmcTokens() = %q, want empty", got)
	}
}

func TestParseSQLCAFromPayloadStoresTokens(t *testing.T) {
	sqlca := make([]byte, 136)
	binary.BigEndian.PutUint32(sqlca[12:16], uint32(0xFFFFFF31)) // SQLCODE -207
	copy(sqlca[131:136], ebcdicForTest(t, "42703"))
	tokens := ebcdicForTest(t, "COLX")
	binary.BigEndian.PutUint16(sqlca[16:18], uint16(len(tokens)))
	copy(sqlca[18:], tokens)

	info, err := parseSQLCAFromPayload(llcpForTest(CodePointSQLCA, sqlca))
	if err != nil {
		t.Fatalf("parseSQLCAFromPayload() error = %v", err)
	}
	if info.SQLCode != -207 || info.SQLState != "42703" || info.Tokens != "COLX" {
		t.Fatalf("sqlca info = %+v", info)
	}
}

func TestNewSQLErrorWithoutConnectionFallsBack(t *testing.T) {
	var c *Conn
	e := c.newSQLError(nil, 1, &sqlcaInfo{SQLCode: -204, SQLState: "42704"})
	if e.Code != -204 || e.State != "42704" || e.Message != "SQL execution failed (42704)" {
		t.Fatalf("newSQLError() = %+v", e)
	}
	if e.MessageID != "" || e.MessageText != "" {
		t.Fatalf("message fields = %+v, want empty without a connection", e)
	}
}

func TestDSNErrorsOption(t *testing.T) {
	cfg, err := ParseDSN("as400://user:pass@host/schema")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.ErrorsFull {
		t.Fatalf("default ErrorsFull = true, want basic")
	}

	cfg, err = ParseDSN("as400://user:pass@host/schema?errors=full")
	if err != nil {
		t.Fatalf("ParseDSN(errors=full) error = %v", err)
	}
	if !cfg.ErrorsFull {
		t.Fatalf("errors=full: ErrorsFull = false")
	}

	cfg, err = ParseDSN("as400://user:pass@host/schema?errors=BASIC")
	if err != nil {
		t.Fatalf("ParseDSN(errors=BASIC) error = %v", err)
	}
	if cfg.ErrorsFull {
		t.Fatalf("errors=BASIC: ErrorsFull = true")
	}

	if _, err := ParseDSN("as400://user:pass@host/schema?errors=verbose"); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("errors=verbose error = %v, want ErrInvalidDSN", err)
	}
}

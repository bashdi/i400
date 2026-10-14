package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"testing"
	"time"
)

// v7r1VRM is V7R1M0 in JTOpen's VRM encoding, a release at or above V6R1.
const v7r1VRM = 0x070100

// superExtendedFormatPayload builds the body of a super extended data format
// (0x3812) with no columns: consistency token, field count, date format, time
// format, date separator, time separator, record size. Offsets match JTOpen
// DBSuperExtendedDataFormat (dateFormat at 8, timeFormat at 9, dateSeparator at
// 10, timeSeparator at 11).
func superExtendedFormatPayload(dateFormat, timeFormat, dateSep, timeSep byte) []byte {
	p := make([]byte, 16)
	binary.BigEndian.PutUint32(p[0:4], 0)
	binary.BigEndian.PutUint32(p[4:8], 0)
	p[8] = dateFormat
	p[9] = timeFormat
	p[10] = dateSep
	p[11] = timeSep
	binary.BigEndian.PutUint32(p[12:16], 0)
	return p
}

func TestParseSuperExtendedDataFormatReadsDateTimeFormats(t *testing.T) {
	meta := &resultSetMeta{}
	payload := superExtendedFormatPayload(dateFormatDMY, timeFormatEUR, 2, 1)
	if err := parseSuperExtendedDataFormat(payload, meta); err != nil {
		t.Fatalf("parseSuperExtendedDataFormat: %v", err)
	}
	if !meta.hasDataFormat {
		t.Fatal("hasDataFormat not set after parsing 0x3812")
	}
	if meta.DateFormat != dateFormatDMY || meta.TimeFormat != timeFormatEUR {
		t.Errorf("formats = date %d time %d, want DMY and EUR", meta.DateFormat, meta.TimeFormat)
	}
	if meta.DateSeparator != 2 || meta.TimeSeparator != 1 {
		t.Errorf("separators = %d/%d, want 2/1", meta.DateSeparator, meta.TimeSeparator)
	}
}

func TestResultFormatOverridesConnectionForStoredProcedure(t *testing.T) {
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(superExtendedFormatPayload(dateFormatDMY, timeFormatEUR, 0, 0), meta); err != nil {
		t.Fatal(err)
	}
	conn := &Conn{cfg: Config{DateFormat: dateFormatUSA, TimeFormat: timeFormatHMS}, systemInfo: &SystemInfo{ServerVersion: v7r1VRM}}
	dt := meta.dateTimeSettingsFor(conn, true)
	if dt.DateFormat != dateFormatDMY || dt.TimeFormat != timeFormatEUR {
		t.Fatalf("stored procedure settings = %+v, want DMY/EUR from result format", dt)
	}
	// Separators come from the connection, as in JTOpen result decoding.
	if dt.DateSeparator != conn.cfg.DateSeparator || dt.TimeSeparator != conn.cfg.TimeSeparator {
		t.Errorf("separators = %d/%d, want connection values", dt.DateSeparator, dt.TimeSeparator)
	}
	raw := encodeEBCDIC37("22/04/26")
	col := columnMeta{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 8}
	val, err := decodeColumnValueWithSettings(col, raw, dt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, ok := val.(time.Time); !ok || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("decoded DMY date = %#v", val)
	}
}

func TestResultFormatIgnoredOutsideStoredProcedure(t *testing.T) {
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(superExtendedFormatPayload(dateFormatDMY, timeFormatEUR, 0, 0), meta); err != nil {
		t.Fatal(err)
	}
	conn := &Conn{cfg: Config{DateFormat: dateFormatUSA}}
	if dt := meta.dateTimeSettingsFor(conn, false); dt.DateFormat != dateFormatUSA || dt.TimeFormat != timeFormatHMS {
		t.Errorf("non stored procedure settings = %+v, want connection values", dt)
	}
}

func TestResultFormatRequiresKnownVRMAtLeastV6R1(t *testing.T) {
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(superExtendedFormatPayload(dateFormatDMY, timeFormatEUR, 0, 0), meta); err != nil {
		t.Fatal(err)
	}
	cfg := Config{DateFormat: dateFormatUSA, TimeFormat: timeFormatHMS}
	tests := []struct {
		name string
		info *SystemInfo
	}{
		{"unknown VRM", &SystemInfo{ServerVersion: 0}},
		{"no signon info", nil},
		{"V5R4", &SystemInfo{ServerVersion: 0x050400}},
		{"V6R0M9", &SystemInfo{ServerVersion: 0x060009}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &Conn{cfg: cfg, systemInfo: tt.info}
			if dt := meta.dateTimeSettingsFor(conn, true); dt.DateFormat != dateFormatUSA || dt.TimeFormat != timeFormatHMS {
				t.Errorf("settings = %+v, want connection values", dt)
			}
		})
	}
	// V6R1M0 is the minimum and enables the override.
	conn := &Conn{cfg: cfg, systemInfo: &SystemInfo{ServerVersion: storedProcDateTimeMinVRM}}
	if dt := meta.dateTimeSettingsFor(conn, true); dt.DateFormat != dateFormatDMY {
		t.Errorf("V6R1M0 settings = %+v, want DMY", dt)
	}
}

func TestMarkStoredProcResultOnlyForCall(t *testing.T) {
	call := &queryRows{}
	markStoredProcResult(call, sqlStatementTypeCall)
	if !call.storedProcResult {
		t.Error("CALL result set not flagged")
	}
	sel := &queryRows{}
	markStoredProcResult(sel, sqlStatementTypeSelect)
	if sel.storedProcResult {
		t.Error("SELECT result set flagged")
	}
	// Non-query rows and nil must not panic.
	markStoredProcResult(nil, sqlStatementTypeCall)
}

func TestResultFormatAbsentUsesConnection(t *testing.T) {
	conn := &Conn{cfg: Config{DateFormat: dateFormatEUR, TimeFormat: timeFormatUSA}}
	var meta *resultSetMeta
	if dt := meta.dateTimeSettingsFor(conn, true); dt.DateFormat != dateFormatEUR || dt.TimeFormat != timeFormatUSA {
		t.Errorf("nil meta settings = %+v, want connection values", dt)
	}
	// A result set without 0x3812 has zero-valued format fields; they must not apply.
	meta = &resultSetMeta{}
	if dt := meta.dateTimeSettingsFor(conn, true); dt.DateFormat != dateFormatEUR || dt.TimeFormat != timeFormatUSA {
		t.Errorf("absent format settings = %+v, want connection values", dt)
	}
	if dt := meta.dateTimeSettingsFor(nil, true); dt != defaultDateTimeSettings() {
		t.Errorf("nil conn settings = %+v, want defaults", dt)
	}
}

func TestQueryRowsDecodesStoredProcedureDateWithResultFormat(t *testing.T) {
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(superExtendedFormatPayload(dateFormatYMD, 0, 0, 0), meta); err != nil {
		t.Fatal(err)
	}
	meta.columns = []columnMeta{{Type: db2TypeDate, CCSID: 37, Offset: 0, Length: 8}}
	meta.RowSize = 8
	conn := &Conn{cfg: Config{DateFormat: dateFormatISO}, systemInfo: &SystemInfo{ServerVersion: v7r1VRM}}
	raw := encodeEBCDIC37("26/04/22")
	rows := &queryRows{
		conn:             conn,
		ctx:              context.Background(),
		meta:             meta,
		storedProcResult: true,
		block:            &fetchBlock{Data: raw, RowSize: 8},
	}
	dest := make([]driver.Value, 1)
	if err := rows.decodeCurrentRow(dest); err != nil {
		t.Fatalf("decodeCurrentRow: %v", err)
	}
	if got, ok := dest[0].(time.Time); !ok || !got.Equal(time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("decoded YMD date = %#v", dest[0])
	}

	// Same bytes on a non-procedure result set use the connection's ISO format and fail.
	rows.storedProcResult = false
	if err := rows.decodeCurrentRow(dest); err == nil {
		t.Error("expected error decoding YMD text with the connection's ISO format")
	}
}

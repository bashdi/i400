package i400

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// udLLCP wraps payload in a length/code point element, as in the reply stream.
func udLLCP(codePoint uint16, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(6+len(payload)))
	out = binary.BigEndian.AppendUint16(out, codePoint)
	return append(out, payload...)
}

// udExtCol describes one column for a JTOpen DBExtendedColumnDescriptors payload.
type udExtCol struct {
	Updateable, Searchable byte
	BaseColumn, BaseTable  string
	Label, BaseSchema      string
}

// udExtSection builds the variable descriptor records for one column:
// LL(4) CP(2) value, with CCSID(2) ahead of the value for the label (0x3902).
func udExtSection(t *testing.T, c udExtCol) []byte {
	t.Helper()
	var out []byte
	add := func(cp uint16, text string) {
		if text == "" {
			return
		}
		raw := udEBCDIC(t, text)
		if cp == 0x3902 {
			out = binary.BigEndian.AppendUint32(out, uint32(6+2+len(raw)))
			out = binary.BigEndian.AppendUint16(out, cp)
			out = binary.BigEndian.AppendUint16(out, 37)
		} else {
			out = binary.BigEndian.AppendUint32(out, uint32(6+len(raw)))
			out = binary.BigEndian.AppendUint16(out, cp)
		}
		out = append(out, raw...)
	}
	add(0x3900, c.BaseColumn)
	add(0x3901, c.BaseTable)
	add(0x3902, c.Label)
	add(0x3904, c.BaseSchema)
	return out
}

// udExtendedDescriptorsPayload lays out a DBExtendedColumnDescriptors payload as
// JTOpen reads it: count(4), reserved(6), 16 bytes per column, then the variable
// sections. Each section offset counts from the LL of the 0x3811 element, which
// is 6 bytes before the payload (JTOpen uses offset_ - 6 + offset).
func udExtendedDescriptorsPayload(t *testing.T, cols []udExtCol) []byte {
	t.Helper()
	sections := make([][]byte, len(cols))
	for i, c := range cols {
		sections[i] = udExtSection(t, c)
	}
	out := binary.BigEndian.AppendUint32(nil, uint32(len(cols)))
	out = append(out, make([]byte, 6)...)
	pos := 10 + 16*len(cols) // payload-relative start of the first section
	for i, c := range cols {
		out = append(out, c.Updateable, c.Searchable)
		out = binary.BigEndian.AppendUint16(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(pos+6)) // LL-relative offset
		out = binary.BigEndian.AppendUint32(out, uint32(len(sections[i])))
		out = binary.BigEndian.AppendUint32(out, 0)
		pos += len(sections[i])
	}
	for _, s := range sections {
		out = append(out, s...)
	}
	return out
}

func TestUDParseExtendedColumnDescriptorsJTOpenLayout(t *testing.T) {
	payload := udExtendedDescriptorsPayload(t, []udExtCol{
		{Updateable: 1, Searchable: 3, BaseColumn: "EMPNO", BaseTable: "EMP", Label: "EMPLOYEE NO", BaseSchema: "HR"},
		{Updateable: 0, Searchable: 2, BaseColumn: "SALARY", BaseTable: "EMP", Label: "PAY"},
	})
	meta := &resultSetMeta{}
	if err := parseExtendedColumnDescriptors(payload, meta); err != nil {
		t.Fatalf("parseExtendedColumnDescriptors() error = %v", err)
	}
	if len(meta.columns) != 2 {
		t.Fatalf("columns = %d, want 2", len(meta.columns))
	}
	first := meta.columns[0]
	if first.Updateable != 1 || first.Searchable != 3 {
		t.Fatalf("column 0 updateable/searchable = %d/%d, want 1/3", first.Updateable, first.Searchable)
	}
	if first.BaseColumnName != "EMPNO" || first.BaseTableName != "EMP" || first.Label != "EMPLOYEE NO" || first.BaseSchemaName != "HR" {
		t.Fatalf("column 0 names = %q/%q/%q/%q", first.BaseColumnName, first.BaseTableName, first.Label, first.BaseSchemaName)
	}
	second := meta.columns[1]
	if second.Updateable != 0 || second.Searchable != 2 || second.BaseColumnName != "SALARY" || second.Label != "PAY" {
		t.Fatalf("column 1 = %+v", second)
	}
}

func TestUDParseExtendedColumnDescriptorsRejectsTruncated(t *testing.T) {
	payload := udExtendedDescriptorsPayload(t, []udExtCol{{BaseColumn: "A", Label: "B"}})
	if err := parseExtendedColumnDescriptors(payload[:12], &resultSetMeta{}); err == nil {
		t.Fatalf("truncated descriptor table error = nil, want error")
	}
}

// udSuperField describes one field for a DBSuperExtendedDataFormat payload (0x3812).
type udSuperField struct {
	SQLType, Length, Scale, Precision, CCSID int
	ParamType                                byte
	LOBLocator, LOBMaxSize                   uint32
	Attr                                     byte
	Name                                     string
	NameCCSID                                int
}

// udSuperExtendedDataFormatPayload follows JTOpen DBSuperExtendedDataFormat:
// a 16-byte header then 48 bytes per field, with the variable name records
// after the fixed part. Variable offsets are relative to each field's base.
func udSuperExtendedDataFormatPayload(t *testing.T, dateFmt, timeFmt, dateSep, timeSep byte, recordSize uint32, fields []udSuperField) []byte {
	t.Helper()
	n := len(fields)
	var vars [][]byte
	for _, f := range fields {
		var v []byte
		if f.Name != "" {
			name := udEBCDIC(t, f.Name)
			if f.NameCCSID != 37 {
				name = []byte(f.Name)
			}
			v = binary.BigEndian.AppendUint32(v, uint32(8+len(name)))
			v = binary.BigEndian.AppendUint16(v, 0x3840)
			v = binary.BigEndian.AppendUint16(v, uint16(f.NameCCSID))
			v = append(v, name...)
		}
		vars = append(vars, v)
	}
	out := binary.BigEndian.AppendUint32(nil, 0)        // consistency token
	out = binary.BigEndian.AppendUint32(out, uint32(n)) // number of fields
	out = append(out, dateFmt, timeFmt, dateSep, timeSep)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	varStart := 16 + 48*n
	for i, f := range fields {
		base := 16 + 48*i
		fixed := make([]byte, 48)
		binary.BigEndian.PutUint16(fixed[2:], uint16(f.SQLType))
		binary.BigEndian.PutUint32(fixed[4:], uint32(f.Length))
		binary.BigEndian.PutUint16(fixed[8:], uint16(f.Scale))
		binary.BigEndian.PutUint16(fixed[10:], uint16(f.Precision))
		binary.BigEndian.PutUint16(fixed[12:], uint16(f.CCSID))
		fixed[14] = f.ParamType
		binary.BigEndian.PutUint32(fixed[17:], f.LOBLocator)
		fixed[21] = f.Attr
		binary.BigEndian.PutUint32(fixed[26:], f.LOBMaxSize)
		binary.BigEndian.PutUint32(fixed[32:], uint32(varStart-base))
		binary.BigEndian.PutUint32(fixed[36:], uint32(len(vars[i])))
		out = append(out, fixed...)
		varStart += len(vars[i])
	}
	for _, v := range vars {
		out = append(out, v...)
	}
	return out
}

func TestUDParseSuperExtendedDataFormatFields(t *testing.T) {
	payload := udSuperExtendedDataFormatPayload(t, 1, 2, '-', ':', 14, []udSuperField{
		{SQLType: db2TypeInteger, Length: 4, CCSID: 0, Name: "ID", NameCCSID: 37, Attr: 0x80},
		{SQLType: db2TypeVarchar, Length: 12, CCSID: 37, Name: "NAME", NameCCSID: 37},
		{SQLType: db2TypeDecimal, Length: 4, Scale: 2, Precision: 5, CCSID: 37},
	})
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(payload, meta); err != nil {
		t.Fatalf("parseSuperExtendedDataFormat() error = %v", err)
	}
	if !meta.hasDataFormat || meta.DateFormat != 1 || meta.TimeFormat != 2 || meta.DateSeparator != '-' || meta.TimeSeparator != ':' {
		t.Fatalf("format header = %+v", meta)
	}
	if meta.RowSize != 14 {
		t.Fatalf("RowSize = %d, want 14 from the record size", meta.RowSize)
	}
	if len(meta.columns) != 3 {
		t.Fatalf("columns = %d, want 3", len(meta.columns))
	}
	wantOffsets := []int{0, 4, 16}
	for i, col := range meta.columns {
		if col.Offset != wantOffsets[i] {
			t.Fatalf("column %d offset = %d, want %d", i, col.Offset, wantOffsets[i])
		}
	}
	if c := meta.columns[0]; c.Type != db2TypeInteger || c.Length != 4 || c.Name != "ID" || c.NameCCSID != 37 {
		t.Fatalf("column 0 = %+v", c)
	}
	if c := meta.columns[1]; c.Type != db2TypeVarchar || c.Length != 12 || c.CCSID != 37 || c.Name != "NAME" {
		t.Fatalf("column 1 = %+v", c)
	}
	if c := meta.columns[2]; c.Type != db2TypeDecimal || c.Scale != 2 || c.Precision != 5 || c.Name != "" {
		t.Fatalf("column 2 = %+v", c)
	}
}

func TestUDParseSuperExtendedDataFormatRecordSizeFallback(t *testing.T) {
	// With a zero record size the row size is the sum of the field lengths.
	payload := udSuperExtendedDataFormatPayload(t, 0, 0, 0, 0, 0, []udSuperField{
		{SQLType: db2TypeInteger, Length: 4, CCSID: 37},
		{SQLType: db2TypeChar, Length: 6, CCSID: 37},
	})
	meta := &resultSetMeta{}
	if err := parseSuperExtendedDataFormat(payload, meta); err != nil {
		t.Fatalf("parseSuperExtendedDataFormat() error = %v", err)
	}
	if meta.RowSize != 10 {
		t.Fatalf("RowSize = %d, want 10", meta.RowSize)
	}
}

func TestUDParseDescribePayloadSuperExtended(t *testing.T) {
	payload := udSuperExtendedDataFormatPayload(t, 0, 0, 0, 0, 8, []udSuperField{
		{SQLType: db2TypeBigint, Length: 8, CCSID: 37, Name: "BIG", NameCCSID: 37},
	})
	meta, err := parseDescribePayload(udLLCP(0x3812, payload))
	if err != nil {
		t.Fatalf("parseDescribePayload() error = %v", err)
	}
	if len(meta.columns) != 1 || meta.columns[0].Name != "BIG" || meta.RowSize != 8 {
		t.Fatalf("describe meta = %+v", meta)
	}
}

// udSuperParamField describes one field for a DBSuperExtendedDataFormat parameter marker payload.
type udSuperParamField struct {
	SQLType, Length, Scale, Precision, CCSID int
	ParamType                                byte
	LOBLocator, LOBMaxSize                   uint32
}

func udSuperParameterMarkerPayload(recordSize uint32, fields []udSuperParamField) []byte {
	out := binary.BigEndian.AppendUint32(nil, 0)
	out = binary.BigEndian.AppendUint32(out, uint32(len(fields)))
	out = binary.BigEndian.AppendUint32(out, 0)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	for _, f := range fields {
		fixed := make([]byte, 48)
		binary.BigEndian.PutUint16(fixed[2:], uint16(f.SQLType))
		binary.BigEndian.PutUint32(fixed[4:], uint32(f.Length))
		binary.BigEndian.PutUint16(fixed[8:], uint16(f.Scale))
		binary.BigEndian.PutUint16(fixed[10:], uint16(f.Precision))
		binary.BigEndian.PutUint16(fixed[12:], uint16(f.CCSID))
		fixed[14] = f.ParamType
		binary.BigEndian.PutUint32(fixed[17:], f.LOBLocator)
		binary.BigEndian.PutUint32(fixed[26:], f.LOBMaxSize)
		out = append(out, fixed...)
	}
	return out
}

func TestUDParseSuperExtendedParameterMarkerFormat(t *testing.T) {
	payload := udSuperParameterMarkerPayload(20, []udSuperParamField{
		{SQLType: db2TypeInteger, Length: 4, CCSID: 0, ParamType: 0xF1},
		{SQLType: db2TypeBlob, Length: 0, LOBLocator: 9, LOBMaxSize: 4096, ParamType: 0xF0},
	})
	format, err := parseSuperExtendedParameterMarkerFormat(payload)
	if err != nil {
		t.Fatalf("parseSuperExtendedParameterMarkerFormat() error = %v", err)
	}
	if format.RecordSize != 20 || len(format.Fields) != 2 {
		t.Fatalf("format = record %d fields %d, want 20/2", format.RecordSize, len(format.Fields))
	}
	f0 := format.Fields[0]
	if f0.SQLType != db2TypeInteger || f0.Length != 4 || f0.ParameterType != 0xF1 || f0.LOBLocator != 0 {
		t.Fatalf("field 0 = %+v", f0)
	}
	f1 := format.Fields[1]
	if f1.SQLType != db2TypeBlob || f1.LOBLocator != 9 || f1.LOBMaxSize != 4096 || f1.ParameterType != 0xF0 {
		t.Fatalf("field 1 = %+v", f1)
	}

	// Empty payload is no format; zero fields keeps the record size only.
	if got, err := parseSuperExtendedParameterMarkerFormat(nil); err != nil || got != nil {
		t.Fatalf("empty payload = %v, %v; want nil, nil", got, err)
	}
	empty, err := parseSuperExtendedParameterMarkerFormat(udSuperParameterMarkerPayload(7, nil))
	if err != nil || empty.RecordSize != 7 || len(empty.Fields) != 0 {
		t.Fatalf("zero-field format = %+v, %v", empty, err)
	}
	if _, err := parseSuperExtendedParameterMarkerFormat(payload[:16+30]); err == nil {
		t.Fatalf("truncated field table error = nil, want error")
	}
	if _, err := parseSuperExtendedParameterMarkerFormat(payload[:10]); err == nil {
		t.Fatalf("short header error = nil, want error")
	}
}

func TestUDParseParameterMarkerFormatCodePointAndUsesExtended(t *testing.T) {
	super := udLLCP(CodePointSuperExtendedParameterMarker, udSuperParameterMarkerPayload(4, []udSuperParamField{
		{SQLType: db2TypeInteger, Length: 4},
	}))
	format, err := parseParameterMarkerFormatFromPayload(super)
	if err != nil {
		t.Fatalf("parseParameterMarkerFormatFromPayload(super) error = %v", err)
	}
	if format.codePoint != CodePointSuperExtendedParameterMarker || !format.usesExtendedFormat() || format.usesOriginalFormat() {
		t.Fatalf("super format code point = 0x%x, extended = %v", format.codePoint, format.usesExtendedFormat())
	}

	// usesExtendedFormat is false for nil and for the original (0x3805-style) format.
	var nilFormat *parameterMarkerFormat
	if nilFormat.usesExtendedFormat() || nilFormat.usesOriginalFormat() {
		t.Fatalf("nil format reports extended/original")
	}
	original := &parameterMarkerFormat{codePoint: CodePointParameterMarkerFormat}
	if original.usesExtendedFormat() || !original.usesOriginalFormat() {
		t.Fatalf("original format classification wrong")
	}
	extended := &parameterMarkerFormat{codePoint: CodePointExtendedParameterMarker}
	if !extended.usesExtendedFormat() {
		t.Fatalf("extended (0x%x) not classified as extended", CodePointExtendedParameterMarker)
	}
	unknown := &parameterMarkerFormat{codePoint: 0x1234}
	if unknown.usesExtendedFormat() || unknown.usesOriginalFormat() {
		t.Fatalf("unknown code point classified")
	}
}

// udOriginalPayload builds a DBOriginalData (0x3806) payload: the 14-byte header
// (token, row count, column count, indicator size, row size) then the
// indicators for every row and column, then the row data.
func udOriginalPayload(rowCount, columnCount, indicatorSize, rowSize int, indicators []byte, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, 0x01020304)
	out = binary.BigEndian.AppendUint32(out, uint32(rowCount))
	out = binary.BigEndian.AppendUint16(out, uint16(columnCount))
	out = binary.BigEndian.AppendUint16(out, uint16(indicatorSize))
	out = binary.BigEndian.AppendUint16(out, uint16(rowSize))
	out = append(out, indicators...)
	return append(out, data...)
}

func TestUDParseOriginalResultDataPayload(t *testing.T) {
	// Two rows, two columns, 2-byte indicators, 4-byte rows.
	// Row 0: col0 not null, col1 null. Row 1: col0 null, col1 not null.
	indicators := []byte{0x00, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00}
	data := []byte{0, 0, 0, 1, 0, 0, 0, 2}
	block, err := parseOriginalResultDataPayload(udOriginalPayload(2, 2, 2, 4, indicators, data))
	if err != nil {
		t.Fatalf("parseOriginalResultDataPayload() error = %v", err)
	}
	if block.RowCount != 2 || block.ColumnCount != 2 || block.IndicatorSize != 2 || block.RowSize != 4 {
		t.Fatalf("block header = %+v", block)
	}
	wantNulls := []bool{false, true, true, false}
	for i, want := range wantNulls {
		if block.Nulls[i] != want {
			t.Fatalf("null[%d] = %v, want %v", i, block.Nulls[i], want)
		}
	}
	if !bytes.Equal(block.Data, data) {
		t.Fatalf("data = % X, want % X", block.Data, data)
	}

	// A zero-row block has no data and no error.
	empty, err := parseOriginalResultDataPayload(udOriginalPayload(0, 1, 2, 4, nil, nil))
	if err != nil || empty.RowCount != 0 || len(empty.Data) != 0 {
		t.Fatalf("empty block = %+v, %v", empty, err)
	}

	// Truncated data is an error.
	if _, err := parseOriginalResultDataPayload(udOriginalPayload(2, 2, 2, 4, indicators, data[:3])); err == nil {
		t.Fatalf("truncated data error = nil, want error")
	}
}

func TestUDParseFetchPayloadDispatch(t *testing.T) {
	original := udOriginalPayload(1, 1, 2, 4, []byte{0, 0}, []byte{0, 0, 0, 7})
	block, err := parseFetchPayload(udLLCP(0x3806, original))
	if err != nil || block.RowCount != 1 || !bytes.Equal(block.Data, []byte{0, 0, 0, 7}) {
		t.Fatalf("0x3806 fetch = %+v, %v", block, err)
	}

	// Extended result data (0x380E) has a 20-byte header: token, row count,
	// column count, indicator size, reserved, row size.
	extended := binary.BigEndian.AppendUint32(nil, 0)
	extended = binary.BigEndian.AppendUint32(extended, 1)
	extended = binary.BigEndian.AppendUint16(extended, 1)
	extended = binary.BigEndian.AppendUint16(extended, 2)
	extended = binary.BigEndian.AppendUint32(extended, 0)
	extended = binary.BigEndian.AppendUint32(extended, 4)
	extended = append(extended, 0xFF, 0xFF)
	extended = append(extended, 0, 0, 0, 9)
	block, err = parseFetchPayload(udLLCP(0x380E, extended))
	if err != nil || block.RowCount != 1 || !block.Nulls[0] || !bytes.Equal(block.Data, []byte{0, 0, 0, 9}) {
		t.Fatalf("0x380E fetch = %+v, %v", block, err)
	}

	// A reply with no result data element is an empty block.
	none, err := parseFetchPayload(udLLCP(0x3807, []byte{1, 2}))
	if err != nil || none == nil || none.RowCount != 0 {
		t.Fatalf("no result data = %+v, %v", none, err)
	}
}

func TestUDSumColumnLengths(t *testing.T) {
	if got := sumColumnLengths(nil); got != 0 {
		t.Fatalf("sumColumnLengths(nil) = %d, want 0", got)
	}
	cols := []columnMeta{{Length: 4}, {Length: 10}, {Length: 0}, {Length: 8}}
	if got := sumColumnLengths(cols); got != 22 {
		t.Fatalf("sumColumnLengths = %d, want 22", got)
	}
}

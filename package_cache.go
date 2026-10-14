package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"strings"
)

// Client-side package cache, ported from JTOpen's JDPackageManager.cache(),
// isCached(), getCachedStatementIndex() and the cached branch of
// AS400JDBCStatement.commonPrepare.
//
// When packageCache is enabled and CREATE_PACKAGE reports that the package
// already exists, the driver sends RETURN_PACKAGE and keeps the statements the
// package holds. A later prepare of packaged statement text that matches a
// cached statement skips PREPARE_DESCRIBE and binds the statement to the cached
// package statement name instead.

const (
	// orsPackageInfo is ORS_BITMAP_PACKAGE_INFO, which asks for the package info
	// reply on RETURN_PACKAGE.
	orsPackageInfo uint32 = 0x00100000

	// codePointPackageInfo is the package info reply code point (0x380B).
	codePointPackageInfo = 0x380B
	// codePointPackageReturnSize is the return size code point (0x3815) on
	// RETURN_PACKAGE.
	codePointPackageReturnSize = 0x3815

	// packageSQLStateCacheDisabled is JDError.WARN_PACKAGE_CACHE_DISABLED.
	packageSQLStateCacheDisabled = "01H12"

	// Package info layout, counted from the start of the package info payload
	// (JTOpen's DBReplyPackageInfo, offsets after the LL and code point).
	packageInfoHeaderLength = 42
	packageInfoEntryLength  = 64

	// SQLDA layout used for package data formats (JTOpen's DBSQLDADataFormat).
	sqldaHeaderLength = 16
	sqldaFieldLength  = 80
)

// packageCachedStatement is one statement found in a downloaded package.
type packageCachedStatement struct {
	// name is the package statement name, trailing blanks removed.
	name string
	// text is the statement text decoded with the package CCSID. usable is false
	// when the name or the text could not be decoded; such entries never match.
	text   string
	usable bool
	// resultFields is the result data format. It is parsed and checked for LOBs
	// like JTOpen does; column metadata for a query still comes from the open
	// reply, so the driver never depends on this format for decoding rows.
	resultFields []parameterMarkerField
	// paramFormat is the parameter marker format, or nil when the package has none.
	paramFormat *parameterMarkerFormat
}

// packageCache is the statement list of a downloaded package.
type packageCache struct {
	statements []packageCachedStatement
}

// buildReturnPackageRequest builds RETURN_PACKAGE, which names the package and
// its library, sets a return size of zero, and asks for the package info.
func buildReturnPackageRequest(name, library string) ([]byte, error) {
	body := make([]byte, 0, 64)
	nameAttr, err := appendNameAttribute(CodePointPackageName, name)
	if err != nil {
		return nil, err
	}
	libraryAttr, err := appendNameAttribute(CodePointPackageLibrary, library)
	if err != nil {
		return nil, err
	}
	body = append(body, nameAttr...)
	body = append(body, libraryAttr...)
	body = appendIntAttribute(body, codePointPackageReturnSize, 0)

	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionReturnPackage)
	request = appendRequestTemplate(request, orsSendReplyImmediately|orsPackageInfo, 0, 0, 3)
	return append(request, body...), nil
}

// parsePackageInfo reads the package info reply (LLCP 0x380B) out of the reply
// payload. nameCCSID is the job CCSID used for statement and field names.
//
// Layout, relative to the start of the package info (JTOpen DBReplyPackageInfo):
//
//	0    total length       4 bytes
//	4    CCSID              2 bytes (statement text)
//	6    default collection 18 bytes
//	24   statement count    2 bytes
//	26   reserved           16 bytes
//	42   entries, 64 bytes each:
//	       0   statement needs default collection  1 byte (not used)
//	       1   statement type                      2 bytes (not used)
//	       3   statement name                      18 bytes
//	       40  format offset, length               4 + 4 bytes
//	       48  text offset, length                 4 + 4 bytes
//	       56  parameter marker format offset, length 4 + 4 bytes
//
// Offsets in entries count from the start of the LL, which is six bytes before
// the package info, so an offset o refers to info[o-6].
func parsePackageInfo(payload []byte, nameCCSID int) (*packageCache, error) {
	var info []byte
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return nil, err
		}
		if codePoint == codePointPackageInfo {
			info = cpPayload
			break
		}
		remaining = rest
	}
	if info == nil {
		return nil, fmt.Errorf("package info not in reply")
	}
	if len(info) < packageInfoHeaderLength {
		return nil, fmt.Errorf("package info too short: %d", len(info))
	}
	textCCSIDValue := int(int16(binary.BigEndian.Uint16(info[4:6])))
	count := int(binary.BigEndian.Uint16(info[24:26]))
	if len(info) < packageInfoHeaderLength+count*packageInfoEntryLength {
		return nil, fmt.Errorf("package info too short for %d statements: %d", count, len(info))
	}

	cache := &packageCache{statements: make([]packageCachedStatement, 0, count)}
	for i := 0; i < count; i++ {
		start := packageInfoHeaderLength + i*packageInfoEntryLength
		entry := info[start : start+packageInfoEntryLength]

		stmt := packageCachedStatement{}
		name, nameErr := decodeTextByCCSID(entry[3:21], nameCCSID)
		stmt.name = strings.TrimRight(name, " ")

		textData, textInRange := packageRegion(info, entry[48:52], entry[52:56])
		text, textErr := decodeTextByCCSID(textData, textCCSIDValue)
		stmt.text = text
		stmt.usable = nameErr == nil && textErr == nil && textInRange && stmt.name != ""

		if formatData, ok := packageRegionOK(info, entry[40:44], entry[44:48]); ok {
			fields, err := parseSQLDAFormat(formatData, nameCCSID)
			if err != nil {
				return nil, fmt.Errorf("statement %d result format: %w", i, err)
			}
			stmt.resultFields = fields
		}
		if formatData, ok := packageRegionOK(info, entry[56:60], entry[60:64]); ok {
			fields, err := parseSQLDAFormat(formatData, nameCCSID)
			if err != nil {
				return nil, fmt.Errorf("statement %d parameter marker format: %w", i, err)
			}
			stmt.paramFormat = packageParameterFormat(fields)
		}
		cache.statements = append(cache.statements, stmt)
	}
	return cache, nil
}

// packageRegionOK returns the bytes an offset and length pair from an entry
// refer to. A length of 0 or 6 means the item is absent, as in JTOpen.
func packageRegionOK(info []byte, offsetBytes, lengthBytes []byte) ([]byte, bool) {
	length := int(int32(binary.BigEndian.Uint32(lengthBytes)))
	if length == 0 || length == 6 {
		return nil, false
	}
	return packageRegion(info, offsetBytes, lengthBytes)
}

// packageRegion returns the info bytes an offset and length pair from an entry
// refers to. The bool is false when the pair points outside the info.
func packageRegion(info []byte, offsetBytes, lengthBytes []byte) ([]byte, bool) {
	start := int(int32(binary.BigEndian.Uint32(offsetBytes))) - 6
	length := int(int32(binary.BigEndian.Uint32(lengthBytes)))
	if start < 0 || length < 0 || start+length > len(info) {
		return nil, false
	}
	return info[start : start+length], true
}

// sqldaIsVarType reports whether a base SQL type (the high bit cleared) is a
// varying-length type that carries a two-byte length prefix.
func sqldaIsVarType(baseType int) bool {
	switch baseType {
	case 448, 456, 464, 472, 908:
		return true
	}
	return false
}

// parseSQLDAFormat reads a package data format (JTOpen DBSQLDADataFormat): a
// 16-byte header whose field count is at offset 14, followed by 80-byte field
// entries. Field offsets follow DBSQLDADataFormat's getters: type at 0, length
// at 2 (precision is the byte at 2), scale at 3, CCSID at 34, parameter type at
// 48, name length at 64 and name at 66.
func parseSQLDAFormat(data []byte, nameCCSID int) ([]parameterMarkerField, error) {
	if len(data) < sqldaHeaderLength {
		return nil, fmt.Errorf("SQLDA too short: %d", len(data))
	}
	count := int(binary.BigEndian.Uint16(data[14:16]))
	if len(data) < sqldaHeaderLength+count*sqldaFieldLength {
		return nil, fmt.Errorf("SQLDA too short for %d fields: %d", count, len(data))
	}
	fields := make([]parameterMarkerField, count)
	for i := range fields {
		base := sqldaHeaderLength + i*sqldaFieldLength
		entry := data[base : base+sqldaFieldLength]

		sqlType := int(int16(binary.BigEndian.Uint16(entry[0:2])))
		baseType := sqlType & 0xFFFE
		precision := int(int8(entry[2]))
		length := int(int16(binary.BigEndian.Uint16(entry[2:4])))
		switch baseType {
		case 484: // packed decimal
			length = precision/2 + 1
		case 488: // zoned decimal
			length = precision
		case 464, 468, 472: // graphic types count double-byte characters
			length *= 2
		}
		if sqldaIsVarType(baseType) {
			length += 2
			precision += 2
		}

		scale := int(int8(entry[3]))
		if baseType == 496 || baseType == 500 {
			scale = 0
		}

		paramType := 0xF0 // input
		switch entry[48] {
		case 0xD6: // 'O'
			paramType = 0xF1
		case 0xC2: // 'B'
			paramType = 0xF2
		}

		name := ""
		nameLength := int(binary.BigEndian.Uint16(entry[64:66]))
		nameStart := base + 66
		if nameLength > 0 && nameStart+nameLength <= len(data) {
			decoded, err := decodeTextByCCSID(data[nameStart:nameStart+nameLength], nameCCSID)
			if err == nil {
				name = strings.TrimSpace(decoded)
			}
		}

		fields[i] = parameterMarkerField{
			Name:          name,
			SQLType:       sqlType,
			Length:        length,
			Scale:         scale,
			Precision:     precision,
			CCSID:         int(binary.BigEndian.Uint16(entry[34:36])),
			ParameterType: paramType,
			LOBLocator:    -1,
			LOBMaxSize:    -1,
		}
	}
	return fields, nil
}

// packageParameterFormat wraps cached parameter fields in the same format type
// the prepare reply produces. Record size is the sum of field lengths, as in
// JTOpen's getRecordSize.
func packageParameterFormat(fields []parameterMarkerField) *parameterMarkerFormat {
	recordSize := 0
	for _, field := range fields {
		recordSize += field.Length
	}
	return &parameterMarkerFormat{
		codePoint:  CodePointParameterMarkerFormat,
		RecordSize: recordSize,
		Fields:     fields,
	}
}

// packageFieldsHaveLOB reports whether any field is a LOB or a LOB locator. JTOpen
// prepares such statements again instead of using the cached formats.
func packageFieldsHaveLOB(fields []parameterMarkerField) bool {
	for _, field := range fields {
		switch field.SQLType & 0xFFFE {
		case 404, 408, 412, 960, 964, 968:
			return true
		}
	}
	return false
}

// cachedStatementFor returns the cached statement that can stand in for a
// prepare of query. It follows JTOpen's rules: the package cache must be
// loaded, the statement must be packaged, its text must match exactly, and no
// cached format may contain a LOB. The returned value holds a copy of the
// parameter format, so callers may modify it.
func (p *packageState) cachedStatementFor(query string) (packageCachedStatement, bool) {
	if p == nil {
		return packageCachedStatement{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled || p.cache == nil || !packageCriteriaMatches(query, p.selectOK) {
		return packageCachedStatement{}, false
	}
	for i := range p.cache.statements {
		stmt := p.cache.statements[i]
		if !stmt.usable || stmt.text != query {
			continue
		}
		if packageFieldsHaveLOB(stmt.resultFields) {
			return packageCachedStatement{}, false
		}
		if stmt.paramFormat != nil {
			if packageFieldsHaveLOB(stmt.paramFormat.Fields) {
				return packageCachedStatement{}, false
			}
			stmt.paramFormat = clonePackageFormat(stmt.paramFormat)
		}
		return stmt, true
	}
	return packageCachedStatement{}, false
}

// isCachedStatementName reports whether name is a statement in the downloaded
// package. Such statements are shared by every connection statement that
// matches them, so closing one must not close it on the server (JTOpen never
// closes a package statement either).
func (p *packageState) isCachedStatementName(name string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		return false
	}
	for _, stmt := range p.cache.statements {
		if stmt.name == name {
			return true
		}
	}
	return false
}

func clonePackageFormat(format *parameterMarkerFormat) *parameterMarkerFormat {
	copied := *format
	copied.Fields = append([]parameterMarkerField(nil), format.Fields...)
	return &copied
}

// packageCacheEnabled reports whether the packageCache option is on and the
// system has not refused RETURN_PACKAGE.
func (p *packageState) packageCacheEnabled() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cacheOn
}

// cachePackage downloads the package statements with RETURN_PACKAGE. It is
// called from ensurePackage when CREATE_PACKAGE reports that the package already
// exists, as JTOpen's create() does. Failures are reported through the package
// error mode and never leave a partially loaded cache behind.
func (c *Conn) cachePackage(ctx context.Context, p *packageState) error {
	request, err := buildReturnPackageRequest(p.name, p.library)
	if err != nil {
		return err
	}
	envelope, payload, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.RCClass != 0 {
		p.mu.Lock()
		p.cacheOn = false
		p.mu.Unlock()
		return c.packageError(p.errMode, &SQLError{
			State:   packageSQLStateCacheDisabled,
			Message: fmt.Sprintf("SQL package %s could not be read (class %d, code %d); package cache disabled", p.name, envelope.RCClass, envelope.RCCode),
		})
	}
	cache, err := parsePackageInfo(payload, c.packageNameCCSID())
	if err != nil {
		p.mu.Lock()
		p.cacheOn = false
		p.mu.Unlock()
		return c.packageError(p.errMode, &SQLError{
			State:   packageSQLStateCacheDisabled,
			Message: fmt.Sprintf("SQL package %s info could not be read (%v); package cache disabled", p.name, err),
		})
	}
	p.mu.Lock()
	p.cache = cache
	p.mu.Unlock()
	return nil
}

// packageNameCCSID is the job CCSID for names in the package. Zero means the
// driver default (EBCDIC 37).
func (c *Conn) packageNameCCSID() int {
	if c == nil || c.systemInfo == nil {
		return 0
	}
	return c.systemInfo.ServerCCSID
}

// prepareOrReuse prepares query, or reuses a statement from the package cache
// when JTOpen would skip the prepare. The returned name is what open and execute
// requests must use. On a cache hit no PREPARE_DESCRIBE is sent: the statement
// handle is bound to the cached package statement with a Create RPB, which
// JTOpen does not need because it names the statement directly, and the cached
// parameter marker format is returned. On a miss this is prepareAndDescribe
// with the statement name unchanged.
func (c *Conn) prepareOrReuse(ctx context.Context, statementHandle uint16, statementName, query string) (string, *parameterMarkerFormat, error) {
	if err := c.ensurePackage(ctx); err != nil {
		return "", nil, err
	}
	cached, ok := c.pkg.cachedStatementFor(query)
	if !ok {
		_, format, err := c.prepareAndDescribe(ctx, statementHandle, statementName, query)
		if err != nil {
			return "", nil, err
		}
		return statementName, format, nil
	}

	// Record the statement as packaged so open and execute send the package name.
	c.pkg.specForPrepare(statementHandle, query)
	request, err := buildCreateRPBRequest(statementHandle, cached.name, c.pkg.specForLibrary())
	if err != nil {
		return "", nil, err
	}
	if err := c.sendWithoutReply(ctx, request); err != nil {
		return "", nil, err
	}
	return cached.name, cached.paramFormat, nil
}

// sendWithoutReply writes a request the server does not answer, such as the
// Create RPB, under the same locking and deadline rules as doRequest.
func (c *Conn) sendWithoutReply(ctx context.Context, request []byte) error {
	if c == nil {
		return driver.ErrBadConn
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		c.invalidateConnection(err)
		return err
	}
	c.mu.RLock()
	conn := c.netConn
	closed := c.closed
	c.mu.RUnlock()
	if closed || conn == nil {
		return driver.ErrBadConn
	}

	c.opMu.Lock()
	defer c.opMu.Unlock()
	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)
	if _, err := conn.Write(request); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			c.invalidateConnection(ctxErr)
			return ctxErr
		}
		c.invalidateConnection(driver.ErrBadConn)
		return driver.ErrBadConn
	}
	return nil
}

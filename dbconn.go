package i400

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	interfaceName         = "jtopenlite"
	interfaceLevel        = "20230219"
	interfaceType         = "JDBC"
	clientFunctionalLevel = "V7R2M01   "
)

func connectDatabase(ctx context.Context, cfg Config, info *SystemInfo, user, password string) (net.Conn, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	databasePort := cfg.Port
	if databasePort == 0 {
		resolvedPort, err := ResolvePort(ctx, cfg.Host, cfg.DatabaseServiceName())
		if err == nil {
			databasePort = resolvedPort
		} else {
			databasePort = cfg.DefaultPort()
		}
	}
	if databasePort <= 0 {
		return nil, "", fmt.Errorf("%w: invalid database port", ErrConnection)
	}

	conn, err := dialHostServer(ctx, cfg, databasePort)
	if err != nil {
		return nil, "", err
	}

	setConnDeadline(conn, ctx)
	defer clearConnDeadline(conn)
	cancelDeadline := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer cancelDeadline()

	clientSeed := make([]byte, 8)
	binary.BigEndian.PutUint64(clientSeed, uint64(time.Now().UnixMilli()))
	serverSeed, err := databaseExchangeSeeds(conn, clientSeed)
	if err != nil {
		conn.Close()
		return nil, "", err
	}

	if err := databaseStartServer(conn, info, user, password, clientSeed, serverSeed); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			conn.Close()
			return nil, "", ctxErr
		}
		conn.Close()
		return nil, "", err
	}

	jobName, err := databaseSetServerAttributes(conn, cfg, info, true)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			conn.Close()
			return nil, "", ctxErr
		}
		conn.Close()
		return nil, "", err
	}
	if err := databaseAddLibraryList(conn, cfg); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			conn.Close()
			return nil, "", ctxErr
		}
		conn.Close()
		return nil, "", err
	}
	return conn, jobName, nil
}

func databaseExchangeSeeds(conn net.Conn, clientSeed []byte) ([]byte, error) {
	request := make([]byte, 0, 28)
	request = appendU32(request, 28)
	request = append(request, 1)
	request = append(request, 0)
	request = appendU16(request, DatabaseServerID)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	request = appendU16(request, 8)
	request = appendU16(request, RequestExchangeRandomSeeds)
	request = appendBytes(request, clientSeed)
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return nil, err
	}
	rc, payload, err := readSimpleReplyForID(packet, ReplyExchangeRandomSeeds)
	if err != nil {
		return nil, err
	}
	if rc != 0 {
		return nil, fmt.Errorf("database exchange seeds failed with return code 0x%x", uint32(rc))
	}
	if len(payload) < 8 {
		return nil, fmt.Errorf("database exchange seeds reply too short")
	}
	serverSeed := make([]byte, 8)
	copy(serverSeed, payload[:8])
	return serverSeed, nil
}

func databaseStartServer(conn net.Conn, info *SystemInfo, user, password string, clientSeed, serverSeed []byte) error {
	userBytesForEncryption, err := signonUserBytes(user, info.PasswordLevel, true)
	if err != nil {
		return err
	}
	passwordBytes, err := signonPasswordBytes(password, info.PasswordLevel)
	if err != nil {
		return err
	}
	var encryptedPassword []byte
	if info.PasswordLevel >= 2 {
		encryptedPassword, err = encryptPasswordSHA(userBytesForEncryption, passwordBytes, clientSeed, serverSeed)
	} else {
		encryptedPassword, err = encryptPasswordDES(userBytesForEncryption, passwordBytes, clientSeed, serverSeed)
	}
	if err != nil {
		return err
	}
	wireUserBytes, err := signonUserBytes(user, 0, false)
	if err != nil {
		return err
	}

	requestLen := 44 + len(encryptedPassword)
	request := make([]byte, 0, requestLen)
	request = appendU32(request, uint32(requestLen))
	request = append(request, 2)
	request = append(request, 0)
	request = appendU16(request, DatabaseServerID)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	request = appendU16(request, 2)
	request = appendU16(request, RequestStartServer)
	if len(encryptedPassword) == 8 {
		request = append(request, 1)
	} else {
		request = append(request, 3)
	}
	request = append(request, 1)
	request = appendU32(request, uint32(6+len(encryptedPassword)))
	request = appendU16(request, CodePointPassword)
	request = appendBytes(request, encryptedPassword)
	request = appendU32(request, 16)
	request = appendU16(request, CodePointUserID)
	request = appendBytes(request, wireUserBytes)

	if _, err := conn.Write(request); err != nil {
		return err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return err
	}
	rc, payload, err := readSimpleReplyForID(packet, ReplyStartServer)
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("database start server failed with return code 0x%x", uint32(rc))
	}
	for len(payload) >= 6 {
		cp, cpPayload, rest, err := ParseLLCP(payload)
		if err != nil {
			return err
		}
		if cp == CodePointJobName && len(cpPayload) >= 4 {
			jobName, err := DecodeEBCDIC37(cpPayload[4:])
			if err == nil {
				info.JobName = jobName
			}
		}
		payload = rest
	}
	return nil
}

func databaseSetServerAttributes(conn net.Conn, cfg Config, info *SystemInfo, autoCommit bool) (string, error) {
	request, err := buildSetServerAttributesRequest(cfg, autoCommit, cfg.CommitmentControl)
	if err != nil {
		return "", err
	}

	if _, err := conn.Write(request); err != nil {
		return "", err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return "", err
	}
	envelope, payload, err := readReplyEnvelope(packet)
	if err != nil {
		return "", err
	}
	if envelope.hasError() {
		return "", fmt.Errorf("database server attributes failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
	}
	if err := parseServerAttributesPayload(payload, info); err != nil {
		return "", err
	}
	return info.JobName, nil
}

func parseServerAttributesPayload(payload []byte, info *SystemInfo) error {
	if info == nil {
		return nil
	}
	remaining := payload
	for len(remaining) > 0 {
		codePoint, cpPayload, rest, err := ParseLLCP(remaining)
		if err != nil {
			return err
		}
		if codePoint == 0x3804 {
			if err := parseServerAttributesBlock(cpPayload, info); err != nil {
				return err
			}
		}
		remaining = rest
	}
	return nil
}

func parseServerAttributesBlock(data []byte, info *SystemInfo) error {
	if len(data) < 114 {
		return fmt.Errorf("server attributes too short: %d", len(data))
	}

	if functionalLevel, err := DecodeEBCDIC37(data[50:60]); err == nil {
		trimmed := strings.TrimSpace(functionalLevel)
		if len(trimmed) >= 7 {
			if parsed, err := strconv.Atoi(strings.TrimSpace(trimmed[6:])); err == nil {
				info.ServerFunctionalLevel = parsed
			}
		}
	}

	if jobIdentifier, err := DecodeEBCDIC37(data[88:114]); err == nil {
		info.ServerJobIdentifier = jobIdentifier
	}

	return nil
}

func databaseAddLibraryList(conn net.Conn, cfg Config) error {
	request, ok, err := buildAddLibraryListRequest(cfg)
	if err != nil || !ok {
		return err
	}
	if _, err := conn.Write(request); err != nil {
		return err
	}

	packet, err := readPacket(conn)
	if err != nil {
		return err
	}
	envelope, _, err := readReplyEnvelope(packet)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return fmt.Errorf("database add library list failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
	}
	return nil
}

func buildSetServerAttributesRequest(cfg Config, autoCommit bool, commitmentControl int) ([]byte, error) {
	body := make([]byte, 0, 256)
	parmCount := 0

	body = appendShortAttribute(body, CodePointMessageID, 13488)
	parmCount++
	var err error
	body, err = appendEBCDIC37StringAttribute(body, CodePointClientFunctionalLevel, clientFunctionalLevel)
	if err != nil {
		return nil, err
	}
	parmCount++
	body = appendShortAttribute(body, CodePointNamingConvention, namingConventionValue(cfg.Naming))
	parmCount++
	body = appendShortAttribute(body, CodePointCommitmentControlLevel, requestCommitmentControl(autoCommit, commitmentControl))
	parmCount++
	body = appendByteAttribute(body, CodePointTrueAutoCommitIndicator, autoCommitIndicator(autoCommit))
	parmCount++
	body = appendByteAttribute(body, 0x3821, 0xF2)
	parmCount++
	body = appendShortAttribute(body, CodePointDateFormat, uint16(cfg.DateFormat))
	parmCount++
	body = appendShortAttribute(body, CodePointDateSeparator, uint16(cfg.DateSeparator))
	parmCount++
	body = appendShortAttribute(body, CodePointTimeFormat, uint16(cfg.TimeFormat))
	parmCount++
	body = appendShortAttribute(body, CodePointTimeSeparator, uint16(cfg.TimeSeparator))
	parmCount++
	body = appendShortAttribute(body, CodePointDecimalSeparator, uint16(cfg.DecimalSeparator))
	parmCount++
	body = appendIntAttribute(body, 0x3822, 1024*1024)
	parmCount++
	body = appendByteAttribute(body, 0x3805, 0xF0)
	parmCount++
	body = appendShortAttribute(body, 0x3806, 1)
	parmCount++
	body = appendIntAttribute(body, 0x3825, 0xF6000000)
	parmCount++

	body, err = appendEBCDIC37StringAttribute(body, 0x383C, interfaceType)
	if err != nil {
		return nil, err
	}
	parmCount++
	body, err = appendEBCDIC37StringAttribute(body, 0x383D, interfaceName)
	if err != nil {
		return nil, err
	}
	parmCount++
	body, err = appendEBCDIC37StringAttribute(body, 0x383E, interfaceLevel)
	if err != nil {
		return nil, err
	}
	parmCount++

	if schema := defaultSQLLibrary(cfg); schema != "" {
		body, err = appendEBCDIC37StringAttribute(body, CodePointDefaultSQLLibrary, schema)
		if err != nil {
			return nil, err
		}
		parmCount++
	}

	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionSetAttributes)
	request = appendRequestTemplate(request, orsSendReplyImmediately|0x01000000, 0, 0, parmCount)
	return append(request, body...), nil
}

func buildAddLibraryListRequest(cfg Config) ([]byte, bool, error) {
	indicators, libraries := configuredLibraryList(cfg)
	if len(libraries) == 0 {
		return nil, false, nil
	}
	body, err := appendLibraryListAttribute(nil, indicators, libraries)
	if err != nil {
		return nil, false, err
	}
	request := buildHeader(uint32(40+len(body)), 0, NativeDatabaseServerID, 20, FunctionAddLibraryList)
	request = appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 1)
	return append(request, body...), true, nil
}

func buildCancelRequest(statementHandle uint16, jobIdentifier string) ([]byte, error) {
	body := make([]byte, 0, 64)
	jobIdentifierAttr, err := appendEBCDIC37FixedAttribute(body, CodePointJobIdentifier, jobIdentifier, 26)
	if err != nil {
		return nil, err
	}
	body = append(body, jobIdentifierAttr...)
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionCancel)
	request = appendRequestTemplate(request, orsSendReplyImmediately, statementHandle, 0, 1)
	return append(request, body...), nil
}

func appendEBCDIC37StringAttribute(dst []byte, codePoint uint16, value string) ([]byte, error) {
	encoded, err := EncodeEBCDIC37(value)
	if err != nil {
		return nil, err
	}
	dst = appendU32(dst, uint32(10+len(encoded)))
	dst = appendU16(dst, codePoint)
	dst = appendU16(dst, 37)
	dst = appendU16(dst, uint16(len(encoded)))
	return appendBytes(dst, encoded), nil
}

func appendEBCDIC37FixedAttribute(dst []byte, codePoint uint16, value string, size int) ([]byte, error) {
	encoded, err := EncodeEBCDIC37BlankPad(value, size)
	if err != nil {
		return nil, err
	}
	dst = appendU32(dst, uint32(10+len(encoded)))
	dst = appendU16(dst, codePoint)
	dst = appendU16(dst, 37)
	dst = appendU16(dst, uint16(len(encoded)))
	return appendBytes(dst, encoded), nil
}

func appendLibraryListAttribute(dst []byte, indicators []byte, libraries []string) ([]byte, error) {
	payload := make([]byte, 0, 32)
	payload = appendU16(payload, 37)
	payload = appendU16(payload, uint16(len(libraries)))
	for i, library := range libraries {
		indicatorBytes, err := EncodeEBCDIC37(string([]byte{indicators[i]}))
		if err != nil {
			return nil, err
		}
		encodedLibrary, err := EncodeEBCDIC37(library)
		if err != nil {
			return nil, err
		}
		payload = append(payload, indicatorBytes[0])
		payload = appendU16(payload, uint16(len(encodedLibrary)))
		payload = appendBytes(payload, encodedLibrary)
	}
	dst = appendU32(dst, uint32(6+len(payload)))
	dst = appendU16(dst, CodePointListOfLibraries)
	return appendBytes(dst, payload), nil
}

func namingConventionValue(naming NamingConvention) uint16 {
	if naming == NamingSystem {
		return 1
	}
	return 0
}

func requestCommitmentControl(autoCommit bool, commitmentControl int) uint16 {
	if autoCommit {
		return 0
	}
	if commitmentControl < 0 || commitmentControl > 4 {
		return uint16(defaultCommitmentControl)
	}
	return uint16(commitmentControl)
}

func defaultSQLLibrary(cfg Config) string {
	if schema := normalizeServerIdentifier(cfg.Schema); schema != "" {
		return schema
	}
	if cfg.Naming != NamingSQL {
		return ""
	}
	for _, library := range cfg.Libraries {
		normalized := normalizeServerIdentifier(library)
		if normalized == "" || strings.EqualFold(normalized, "*LIBL") {
			continue
		}
		return normalized
	}
	return ""
}

func configuredLibraryList(cfg Config) ([]byte, []string) {
	tokens := make([]string, 0, len(cfg.Libraries)+1)
	for _, library := range cfg.Libraries {
		normalized := normalizeServerIdentifier(library)
		if normalized == "" {
			continue
		}
		tokens = append(tokens, normalized)
	}

	if schema := normalizeServerIdentifier(cfg.Schema); schema != "" && len(schema) <= 10 && !containsNormalizedIdentifier(tokens, schema) {
		tokens = append([]string{schema}, tokens...)
	}

	liblIndex := -1
	frontCount := 0
	for _, token := range tokens {
		if strings.EqualFold(token, "*LIBL") {
			if liblIndex == -1 {
				liblIndex = frontCount
			}
			continue
		}
		frontCount++
	}

	libraries := make([]string, 0, len(tokens))
	indicators := make([]byte, 0, len(tokens))
	nonLiblIndex := 0
	for _, token := range tokens {
		if strings.EqualFold(token, "*LIBL") {
			continue
		}
		indicator := byte('C')
		if liblIndex >= 0 {
			if nonLiblIndex < liblIndex {
				indicator = 'F'
			} else {
				indicator = 'L'
			}
		} else if len(libraries) > 0 {
			indicator = 'L'
		}
		libraries = append(libraries, token)
		indicators = append(indicators, indicator)
		nonLiblIndex++
	}

	if liblIndex > 1 {
		for left, right := 0, liblIndex-1; left < right; left, right = left+1, right-1 {
			libraries[left], libraries[right] = libraries[right], libraries[left]
			indicators[left], indicators[right] = indicators[right], indicators[left]
		}
	}

	return indicators, libraries
}

func containsNormalizedIdentifier(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func normalizeServerIdentifier(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "\"") {
		return trimmed
	}
	return strings.ToUpper(trimmed)
}

func encodeEBCDIC37Fixed(text string, size int) []byte {
	encoded, err := EncodeEBCDIC37(text)
	if err != nil {
		return nil
	}
	result := make([]byte, size)
	for i := range result {
		result[i] = 0x40
	}
	copy(result, encoded)
	return result
}

func (c *Conn) cancelStatement(ctx context.Context, statementHandle uint16) error {
	if c == nil {
		return driver.ErrBadConn
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.systemInfo == nil || c.systemInfo.ServerFunctionalLevel < 5 || strings.TrimSpace(c.systemInfo.ServerJobIdentifier) == "" {
		return unsupported("protocol-native cancel")
	}

	cancelInfo := *c.systemInfo
	cancelConn, _, err := connectDatabase(ctx, c.cfg, &cancelInfo, c.cfg.User, c.cfg.Password)
	if err != nil {
		return err
	}
	defer cancelConn.Close()

	request, err := buildCancelRequest(statementHandle, c.systemInfo.ServerJobIdentifier)
	if err != nil {
		return err
	}

	setConnDeadline(cancelConn, ctx)
	defer clearConnDeadline(cancelConn)
	if _, err := cancelConn.Write(request); err != nil {
		return err
	}

	packet, err := readPacket(cancelConn)
	if err != nil {
		return err
	}
	envelope, _, err := readReplyEnvelope(packet)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return fmt.Errorf("cancel request failed: class=%d code=0x%x", envelope.RCClass, uint32(envelope.RCCode))
	}
	return nil
}

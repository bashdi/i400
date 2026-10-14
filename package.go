package i400

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Extended dynamic support (IBM i SQL packages), ported from JTOpen's
// JDPackageManager, JDSQLStatement.isPackaged and the package-related
// request code in AS400JDBCStatement.
//
// Creating the package, naming it, and referencing it from prepare,
// execute-immediate, open and execute requests are in this file. The client-side
// package cache (JTOpen "package cache") lives in package_cache.go: when the
// package already exists it is downloaded with RETURN_PACKAGE, and packaged
// statements that match a cached statement skip PREPARE_DESCRIBE. "package clear"
// is parsed but has no effect, as in JTOpen.

const (
	defaultPackageLibrary = "QGPL"
	defaultPackageCCSID   = 13488

	packageCriteriaDefault = "default"
	packageCriteriaSelect  = "select"

	packageErrorNone      = "none"
	packageErrorWarning   = "warning"
	packageErrorException = "exception"

	// packageSQLStateDisabled is JDError.WARN_EXTENDED_DYNAMIC_DISABLED.
	packageSQLStateDisabled = "01H11"

	// JTOpen's ORS return codes for CREATE_PACKAGE.
	packageReturnAlreadyExists     = -601
	packageReturnDefaultCollection = -999999

	// CodePointPackageLibrary is the library code point (0x3801) used on
	// CREATE_PACKAGE, the RPB request and nothing else.
	CodePointPackageLibrary = 0x3801
	// codePointPackageAddStatement is 0x3812 on SET SERVER ATTRIBUTES.
	codePointPackageAddStatement = 0x3812
	// codePointPrepareOption is 0x3808 on PREPARE_DESCRIBE and EXECUTE_IMMEDIATE.
	codePointPrepareOption = 0x3808
)

// packageSuffixInvariant is JTOpen's SUFFIX_INVARIANT_. Suffix characters are
// looked up by index, so the table order matters.
const packageSuffixInvariant = "9876543210ZYXWVUTSRQPONMLKJIHGFEDCBA"

// packageSpec describes how one request refers to the package. A nil
// *packageSpec means extended dynamic support is off for the connection and
// no package code points are sent. A non-nil spec with packaged == false still
// sends the empty package name on prepare, which is what JTOpen does when
// packaging is enabled but the statement does not meet the criteria.
type packageSpec struct {
	name     string
	library  string
	packaged bool
}

// packageState is the per-connection extended dynamic state. Methods are safe
// on a nil receiver, which means extended dynamic is disabled.
type packageState struct {
	// createMu serializes CREATE_PACKAGE. mu guards the fields below and is
	// never held across network I/O.
	createMu sync.Mutex
	mu       sync.Mutex
	enabled  bool
	created  bool
	name     string
	library  string
	errMode  string
	selectOK bool // package criteria "select"

	// cacheOn is JTOpen's cache_ flag ("package cache"). It is cleared when the
	// system refuses RETURN_PACKAGE. cache holds the statements of the package
	// once it has been downloaded, and is nil until then.
	cacheOn bool
	cache   *packageCache

	// statementPackaged records, per statement handle, whether the statement
	// was packaged at prepare time. Execute and open requests consult it.
	statementPackaged map[uint16]bool
}

// newPackageState applies the connect-time checks from JDPackageManager's
// constructor. It returns a warning to record on the connection when the
// package name is missing and the package error mode is "warning". It returns
// an error when the mode is "exception". With mode "none" the failure is silent.
func newPackageState(cfg Config) (*packageState, *SQLWarning, error) {
	if !cfg.ExtendedDynamic {
		return nil, nil, nil
	}
	mode := packageErrorMode(cfg)
	name := strings.TrimSpace(cfg.Package)
	if name == "" {
		message := "extended dynamic support disabled: no package name was specified"
		switch mode {
		case packageErrorException:
			return nil, nil, &SQLError{State: packageSQLStateDisabled, Message: message}
		case packageErrorWarning:
			return nil, &SQLWarning{State: packageSQLStateDisabled, Message: message}, nil
		}
		return nil, nil, nil
	}

	library := strings.ToUpper(strings.TrimSpace(cfg.PackageLibrary))
	if library == "" {
		library = defaultPackageLibrary
	}
	commitMode := int(serverCommitmentControl(cfg, true, cfg.CommitmentControl))
	return &packageState{
		enabled:           true,
		cacheOn:           cfg.PackageCache,
		name:              packageNameFor(name, cfg, commitMode),
		library:           library,
		errMode:           mode,
		selectOK:          packageCriteria(cfg) == packageCriteriaSelect,
		statementPackaged: make(map[uint16]bool),
	}, nil, nil
}

func packageErrorMode(cfg Config) string {
	if cfg.PackageError == "" {
		return packageErrorWarning
	}
	return cfg.PackageError
}

func packageCriteria(cfg Config) string {
	if cfg.PackageCriteria == "" {
		return packageCriteriaDefault
	}
	return cfg.PackageCriteria
}

// packageNameFor builds the package name exactly as JTOpen does: the first six
// characters of the user name, upper-cased with blanks turned into underscores,
// followed by the four-character suffix derived from the connection settings.
func packageNameFor(name string, cfg Config, commitMode int) string {
	runes := []rune(name)
	if len(runes) >= 6 {
		runes = runes[:6]
	}
	normalized := strings.ReplaceAll(strings.ToUpper(string(runes)), " ", "_")
	return normalized + packageSuffix(cfg, commitMode)
}

// packageSuffix derives the four suffix characters from the connection
// settings, following JDPackageManager.getSuffix. The first character encodes
// translate hex, which the Go driver does not expose, so it is always the
// "character" value (index 0).
func packageSuffix(cfg Config, commitMode int) string {
	dateSeparator := cfg.DateSeparator
	dateFormat := cfg.DateFormat

	// Commit mode *RR (4) does not fit in two bits, so JTOpen borrows the low
	// bits of the date separator to store it.
	if commitMode == 4 {
		switch dateSeparator {
		case 0, 1, 2:
			commitMode = dateSeparator
			dateSeparator = 6
		case 3, 4:
			commitMode = dateSeparator - 2
			dateSeparator = 7
		}
	}

	naming := 0
	if cfg.Naming == NamingSystem {
		naming = 1
	}

	var suffix [4]byte
	suffix[0] = packageSuffixInvariant[0]
	suffix[1] = packageSuffixInvariant[(commitMode<<3)|dateFormat]
	suffix[2] = packageSuffixInvariant[(cfg.DecimalSeparator<<4)|(naming<<3)|dateSeparator]
	suffix[3] = packageSuffixInvariant[(cfg.TimeFormat<<2)|cfg.TimeSeparator]
	return string(suffix[:])
}

// packageCriteriaMatches reports whether a statement is packaged, following
// JDSQLStatement's isPackaged_ rule:
//
//	(has parameter markers and is not WHERE CURRENT OF)
//	or (INSERT ... SELECT)
//	or (SELECT ... FOR UPDATE)
//	or DECLARE
//	or (criteria "select" and SELECT/WITH/VALUES)
func packageCriteriaMatches(sql string, selectCriteria bool) bool {
	tokens, parameterMarkers := packageTokens(sql)
	if len(tokens) == 0 {
		return false
	}

	first := ""
	restStart := 0
	for i, token := range tokens {
		word := strings.TrimLeft(token, "(")
		if word != "" {
			first = word
			restStart = i + 1
			break
		}
	}
	if first == "" {
		return false
	}

	isSelect := strings.HasPrefix(first, "SELECT") || first == "WITH" || strings.HasPrefix(first, "VALUES")
	isInsert := first == "INSERT"
	isDeclare := first == "DECLARE"

	isSubSelect := false
	isCurrentOf := false
	isForUpdate := false
	rest := tokens[restStart:]
	for i, token := range rest {
		switch token {
		case "SELECT":
			if isInsert {
				isSubSelect = true
			}
		case "CURRENT":
			if i+1 < len(rest) && rest[i+1] == "OF" {
				isCurrentOf = true
			}
		case "FOR":
			if i+1 < len(rest) && rest[i+1] == "UPDATE" {
				isForUpdate = true
			}
		}
	}

	packaged := (parameterMarkers > 0 && !isCurrentOf) ||
		(isInsert && isSubSelect) ||
		(isSelect && isForUpdate) ||
		isDeclare
	if selectCriteria {
		packaged = packaged || isSelect
	}
	return packaged
}

// packageTokens splits SQL text the way JDSQLTokenizer does for the purposes
// of packaging: whitespace separates tokens, a '(' starts its own token,
// comments are dropped, quoted literals become a single placeholder token that
// never matches a keyword, and '?' outside literals and comments is counted as
// a parameter marker. Tokens are upper-cased.
func packageTokens(sql string) ([]string, int) {
	var tokens []string
	markers := 0
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, strings.ToUpper(current.String()))
			current.Reset()
		}
	}

	for i := 0; i < len(sql); {
		ch := sql[i]
		switch {
		case ch == '/' && i+1 < len(sql) && sql[i+1] == '*':
			flush()
			depth := 1
			i += 2
			for i < len(sql) && depth > 0 {
				if i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*' {
					depth++
					i += 2
				} else if i+1 < len(sql) && sql[i] == '*' && sql[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
		case ch == '-' && i+1 < len(sql) && sql[i+1] == '-':
			flush()
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case ch == '\'' || ch == '"':
			flush()
			i++
			for i < len(sql) && sql[i] != ch {
				i++
			}
			i++
			tokens = append(tokens, "'")
		case ch == '(':
			flush()
			tokens = append(tokens, "(")
			i++
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f':
			flush()
			i++
		default:
			if ch == '?' {
				markers++
			}
			current.WriteByte(ch)
			i++
		}
	}
	flush()
	return tokens, markers
}

// specForPrepare returns the spec for a prepare-describe or execute-immediate
// request and records whether the statement is packaged.
func (p *packageState) specForPrepare(handle uint16, sql string) *packageSpec {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled {
		return nil
	}
	packaged := packageCriteriaMatches(sql, p.selectOK)
	p.statementPackaged[handle] = packaged
	return &packageSpec{name: p.name, library: p.library, packaged: packaged}
}

// specForImmediate is specForPrepare for execute-immediate, which has no
// statement handle to record against.
func (p *packageState) specForImmediate(sql string) *packageSpec {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled {
		return nil
	}
	return &packageSpec{name: p.name, library: p.library, packaged: packageCriteriaMatches(sql, p.selectOK)}
}

// specForLibrary returns a spec that only carries the library, used for the
// RPB request, which JTOpen sends whenever packaging is enabled.
func (p *packageState) specForLibrary() *packageSpec {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled {
		return nil
	}
	return &packageSpec{name: p.name, library: p.library}
}

// specForExecute returns the spec for open and execute requests on a prepared
// statement. JTOpen only sends the package name for packaged statements, so
// the result is nil unless the statement was packaged at prepare time.
func (p *packageState) specForExecute(handle uint16) *packageSpec {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled || !p.statementPackaged[handle] {
		return nil
	}
	return &packageSpec{name: p.name, library: p.library, packaged: true}
}

// appendPrepareAttributes adds the prepare option and package name to a
// PREPARE_DESCRIBE or EXECUTE_IMMEDIATE body, as JTOpen's commonPrepare does.
// It returns the body and the updated parameter count.
func (s *packageSpec) appendPrepareAttributes(body []byte, parms int) ([]byte, int) {
	if s == nil {
		return body, parms
	}
	if s.packaged {
		body = appendByteAttribute(body, codePointPrepareOption, 1)
		parms++
		attr, err := appendNameAttribute(CodePointPackageName, s.name)
		if err == nil {
			body = append(body, attr...)
			parms++
		}
		return body, parms
	}
	body = appendByteAttribute(body, codePointPrepareOption, 0)
	parms++
	body = appendRawAttribute(body, CodePointPackageName, nil)
	parms++
	return body, parms
}

// appendExecuteAttributes adds the package name to an OPEN_DESCRIBE or EXECUTE
// body when the statement is packaged.
func (s *packageSpec) appendExecuteAttributes(body []byte, parms int) ([]byte, int) {
	if s == nil || !s.packaged {
		return body, parms
	}
	attr, err := appendNameAttribute(CodePointPackageName, s.name)
	if err != nil {
		return body, parms
	}
	return append(body, attr...), parms + 1
}

// appendLibraryAttributes adds the package library to an RPB body.
func (s *packageSpec) appendLibraryAttributes(body []byte, parms int) ([]byte, int) {
	if s == nil {
		return body, parms
	}
	attr, err := appendNameAttribute(CodePointPackageLibrary, s.library)
	if err != nil {
		return body, parms
	}
	return append(body, attr...), parms + 1
}

// buildCreatePackageRequest builds CREATE_PACKAGE, which carries the package
// name and library and asks for return data only.
func buildCreatePackageRequest(name, library string) ([]byte, error) {
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

	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionCreatePackage)
	request = appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 2)
	return append(request, body...), nil
}

// buildSetPackageAddAttribute returns the SET SERVER ATTRIBUTES "package add"
// attribute value. JTOpen sends it on every connection; the default is allow.
func buildSetPackageAddAttribute(cfg Config) []byte {
	value := uint16(1)
	if cfg.PackageAddDisabled {
		value = 0
	}
	return appendShortAttribute(nil, codePointPackageAddStatement, value)
}

// ensurePackage creates the package on the first statement that needs it,
// which is the point where JTOpen calls JDPackageManager.create(). An existing
// package counts as success. Other failures disable extended dynamic and are
// either returned as an error (package error "exception") or recorded as a
// warning (package error "warning") or dropped ("none").
func (c *Conn) ensurePackage(ctx context.Context) error {
	if c == nil || c.pkg == nil {
		return nil
	}
	p := c.pkg
	p.createMu.Lock()
	defer p.createMu.Unlock()

	p.mu.Lock()
	created, enabled := p.created, p.enabled
	p.mu.Unlock()
	if created {
		return nil
	}
	if !enabled {
		p.mu.Lock()
		p.created = true
		p.mu.Unlock()
		return nil
	}

	request, err := buildCreatePackageRequest(p.name, p.library)
	if err != nil {
		return err
	}
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}

	// As in JTOpen, an exception leaves the package unmarked so the next
	// statement sees a disabled package and simply marks it created. Warnings
	// and silent mode mark it created right away.
	var failure error
	switch {
	case envelope.RCClass == 1 && envelope.RCCode == packageReturnAlreadyExists:
		// The package already exists, which is what we want. As in JTOpen, an
		// existing package is downloaded when the package cache is on.
		if p.packageCacheEnabled() {
			failure = c.cachePackage(ctx, p)
		}
	case envelope.RCCode == packageReturnDefaultCollection:
		// The package default collection differs from this connection's, so
		// using it could mix up data. JTOpen disables packaging here.
		failure = c.packageError(p.errMode, &SQLError{State: packageSQLStateDisabled, Message: "extended dynamic support disabled: the package default collection does not match the connection"})
		p.mu.Lock()
		p.enabled = false
		p.mu.Unlock()
	case envelope.RCClass != 0:
		p.mu.Lock()
		p.enabled = false
		p.mu.Unlock()
		if p.errMode == packageErrorException {
			failure = newProtocolError("create package", envelope)
		} else if p.errMode == packageErrorWarning {
			c.recordWarning(&SQLWarning{
				Code:    envelope.RCCode,
				Message: fmt.Sprintf("SQL package %s could not be created (class %d, code %d); extended dynamic support disabled", p.name, envelope.RCClass, envelope.RCCode),
			})
		}
	}
	if failure != nil {
		return failure
	}
	p.mu.Lock()
	p.created = true
	p.mu.Unlock()
	return nil
}

// packageError applies the package error mode to a disabling condition that
// has an SQL state. Warnings are recorded on the connection and exceptions
// are returned.
func (c *Conn) packageError(mode string, sqlErr *SQLError) error {
	switch mode {
	case packageErrorException:
		return sqlErr
	case packageErrorWarning:
		c.recordWarning(&SQLWarning{State: sqlErr.State, Message: sqlErr.Message})
	}
	return nil
}

// parsePackageOptions reads the extended dynamic DSN options. JTOpen spellings
// with spaces (for example "package library") are accepted through optionValue.
func parsePackageOptions(config *Config) error {
	q := config.RawQuery
	config.ExtendedDynamic = false
	config.PackageLibrary = defaultPackageLibrary
	config.PackageCriteria = packageCriteriaDefault
	config.PackageError = packageErrorWarning
	config.PackageCCSID = defaultPackageCCSID

	if value, ok := optionValue(q, "extendedDynamic"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid extendedDynamic value %q", ErrInvalidDSN, value)
		}
		config.ExtendedDynamic = enabled
	}
	if value, ok := optionValue(q, "package"); ok {
		config.Package = value
	}
	if value, ok := optionValue(q, "packageLibrary"); ok {
		config.PackageLibrary = strings.ToUpper(value)
	}
	if value, ok := optionValue(q, "packageCriteria"); ok {
		switch strings.ToLower(value) {
		case packageCriteriaDefault:
			config.PackageCriteria = packageCriteriaDefault
		case packageCriteriaSelect:
			config.PackageCriteria = packageCriteriaSelect
		default:
			return fmt.Errorf("%w: invalid packageCriteria value %q", ErrInvalidDSN, value)
		}
	}
	if value, ok := optionValue(q, "packageAdd"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid packageAdd value %q", ErrInvalidDSN, value)
		}
		config.PackageAddDisabled = !enabled
	}
	if value, ok := optionValue(q, "packageError"); ok {
		switch strings.ToLower(value) {
		case packageErrorNone, packageErrorWarning, packageErrorException:
			config.PackageError = strings.ToLower(value)
		default:
			return fmt.Errorf("%w: invalid packageError value %q", ErrInvalidDSN, value)
		}
	}
	if value, ok := optionValue(q, "packageCCSID"); ok {
		if strings.EqualFold(value, "system") {
			config.PackageCCSIDSystem = true
		} else {
			ccsid, err := strconv.Atoi(value)
			if err != nil || ccsid <= 0 {
				return fmt.Errorf("%w: invalid packageCCSID value %q", ErrInvalidDSN, value)
			}
			config.PackageCCSID = ccsid
		}
	}
	if value, ok := optionValue(q, "packageCache"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid packageCache value %q", ErrInvalidDSN, value)
		}
		config.PackageCache = enabled
	}
	if value, ok := optionValue(q, "packageClear"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%w: invalid packageClear value %q", ErrInvalidDSN, value)
		}
		config.PackageClear = enabled
	}
	return nil
}

// validatePackageConfig checks the package fields of a Config built in code.
// Zero values mean the JTOpen default.
func validatePackageConfig(c Config) error {
	switch c.PackageError {
	case "", packageErrorNone, packageErrorWarning, packageErrorException:
	default:
		return fmt.Errorf("%w: invalid packageError %q", ErrInvalidDSN, c.PackageError)
	}
	switch c.PackageCriteria {
	case "", packageCriteriaDefault, packageCriteriaSelect:
	default:
		return fmt.Errorf("%w: invalid packageCriteria %q", ErrInvalidDSN, c.PackageCriteria)
	}
	if c.PackageCCSID < 0 {
		return fmt.Errorf("%w: invalid packageCCSID %d", ErrInvalidDSN, c.PackageCCSID)
	}
	return nil
}

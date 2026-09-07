package i400

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"sync"
)

type Tx struct {
	conn *Conn
	ctx  context.Context
	mu   sync.Mutex
	done bool
}

var _ driver.Tx = (*Tx)(nil)

func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !c.IsValid() {
		return nil, driver.ErrBadConn
	}

	c.mu.RLock()
	active := !c.autoCommit
	c.mu.RUnlock()
	if active {
		return nil, fmt.Errorf("transaction already active")
	}

	mode, err := transactionIsolationMode(opts.Isolation)
	commitmentControl := c.cfg.CommitmentControl
	if err != nil {
		return nil, err
	}
	if mode != "" {
		if err := c.setTransactionIsolationLevel(ctx, mode); err != nil {
			return nil, err
		}
		commitmentControl = transactionCommitmentControl(opts.Isolation)
	}
	c.setCurrentCommitmentControl(commitmentControl)

	if err := c.setAutoCommitState(ctx, false); err != nil {
		return nil, err
	}

	return &Tx{conn: c, ctx: ctx}, nil
}

func (c *Conn) setTransactionIsolationLevel(ctx context.Context, mode string) error {
	if mode == "" {
		return nil
	}
	_, err := c.execContext(ctx, "SET TRANSACTION ISOLATION LEVEL "+mode, nil)
	return err
}

func transactionIsolationMode(level driver.IsolationLevel) (string, error) {
	switch sql.IsolationLevel(level) {
	case sql.LevelDefault:
		return "", nil
	case sql.LevelReadUncommitted:
		return "CHG", nil
	case sql.LevelReadCommitted:
		return "CS", nil
	case sql.LevelRepeatableRead:
		return "ALL", nil
	case sql.LevelSerializable:
		return "RR", nil
	case sql.LevelWriteCommitted, sql.LevelSnapshot, sql.LevelLinearizable:
		return "", fmt.Errorf("%w: unsupported isolation level %d", ErrUnsupported, level)
	default:
		return "", fmt.Errorf("%w: unsupported isolation level %d", ErrUnsupported, level)
	}
}

func (c *Conn) setAutoCommitState(ctx context.Context, enabled bool) error {
	if c == nil {
		return driver.ErrBadConn
	}
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.RLock()
	current := c.autoCommit
	c.mu.RUnlock()
	if current == enabled {
		return nil
	}

	request := buildAutoCommitRequest(enabled, commitmentControlForAutoCommit(enabled, c.currentCommitmentMode()))
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("set auto commit", envelope)
	}

	c.mu.Lock()
	c.autoCommit = enabled
	c.mu.Unlock()
	return nil
}

func (c *Conn) commitTransaction(ctx context.Context) error {
	request := buildCommitRequest()
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("commit", envelope)
	}
	return nil
}

func (c *Conn) rollbackTransaction(ctx context.Context) error {
	request := buildRollbackRequest()
	envelope, _, err := c.doRequest(ctx, request)
	if err != nil {
		return err
	}
	if envelope.hasError() {
		return newProtocolError("rollback", envelope)
	}
	return nil
}

func (t *Tx) Commit() error {
	return t.finish(true)
}

func (t *Tx) Rollback() error {
	return t.finish(false)
}

func (t *Tx) finish(commit bool) error {
	if t == nil {
		return sql.ErrTxDone
	}

	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return sql.ErrTxDone
	}
	t.done = true
	conn := t.conn
	ctx := t.ctx
	t.conn = nil
	t.mu.Unlock()

	if conn == nil {
		return sql.ErrTxDone
	}
	if !conn.IsValid() {
		return driver.ErrBadConn
	}

	var err error
	if commit {
		err = conn.commitTransaction(ctx)
	} else {
		err = conn.rollbackTransaction(ctx)
	}
	if err != nil {
		return err
	}
	if err := conn.setAutoCommitState(ctx, true); err != nil {
		return err
	}
	conn.setCurrentCommitmentControl(conn.cfg.CommitmentControl)
	return nil
}

func buildCommitRequest() []byte {
	request := buildHeader(40, 0, DatabaseServerID, 20, FunctionCommit)
	return appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 0)
}

func buildRollbackRequest() []byte {
	request := buildHeader(40, 0, DatabaseServerID, 20, FunctionRollback)
	return appendRequestTemplate(request, orsSendReplyImmediately, 0, 0, 0)
}

func buildAutoCommitRequest(enabled bool, commitmentControl int) []byte {
	body := make([]byte, 0, 16)
	body = appendByteAttribute(body, CodePointTrueAutoCommitIndicator, autoCommitIndicator(enabled))
	body = appendShortAttribute(body, CodePointCommitmentControlLevel, uint16(requestCommitmentControl(enabled, commitmentControl)))
	request := buildHeader(uint32(40+len(body)), 0, DatabaseServerID, 20, FunctionSetAttributes)
	request = appendRequestTemplate(request, orsSendReplyImmediately|0x01000000, 0, 0, 2)
	return append(request, body...)
}

func buildEndJobRequest() []byte {
	request := buildHeader(40, 0, DatabaseServerID, 0, FunctionEndJob)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	request = appendU32(request, 0)
	return request
}

func sendEndJobRequest(conn net.Conn) error {
	if conn == nil {
		return nil
	}
	_, err := conn.Write(buildEndJobRequest())
	return err
}

func autoCommitIndicator(enabled bool) byte {
	if enabled {
		return 0xE8
	}
	return 0xD5
}

func transactionCommitmentControl(level driver.IsolationLevel) int {
	switch sql.IsolationLevel(level) {
	case sql.LevelReadUncommitted:
		return 2
	case sql.LevelReadCommitted:
		return 1
	case sql.LevelRepeatableRead:
		return 3
	case sql.LevelSerializable:
		return 4
	default:
		return defaultCommitmentControl
	}
}

func commitmentControlForAutoCommit(enabled bool, commitmentControl int) int {
	if enabled {
		return 0
	}
	if commitmentControl < 0 || commitmentControl > 4 {
		return defaultCommitmentControl
	}
	return commitmentControl
}

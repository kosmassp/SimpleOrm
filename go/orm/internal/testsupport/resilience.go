package testsupport

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// ErrFlakyOpen is the open failure a ResilienceDialect raises for its first FailOpens connection attempts — the one
// failure it calls transient.
var ErrFlakyOpen = errors.New("testsupport: the server is briefly unreachable")

// ResilienceDialect decorates the SQLite dialect for the resilience tests (Level 4, ADR-0047): it counts the open
// attempts (CreateConnection calls and the connections its pools dial), fails the first FailOpens dials with Failure
// (ErrFlakyOpen by default), calls exactly the failures matching ErrFlakyOpen transient, and records, for every
// statement any of its connections is asked to run, the SQL and the deadline of the context it ran under. The
// connections are the real modernc ones on a real temp-file database; only the dial is wrapped.
type ResilienceDialect struct {
	orm.Dialect
	// FailOpens is how many dials fail before one succeeds.
	FailOpens int
	// Failure is the error a failing dial returns; nil means ErrFlakyOpen.
	Failure error

	mu         sync.Mutex
	creates    int
	dials      int
	statements []ObservedStatement
}

// ObservedStatement is one statement a connection was asked to run, with the deadline of its context (zero when none).
type ObservedStatement struct {
	SQL      string
	Deadline time.Time
	// Remaining is the time left before the deadline when the statement started (zero when no deadline).
	Remaining time.Duration
}

// NewResilienceDialect wraps a fresh SQLite dialect.
func NewResilienceDialect() *ResilienceDialect { return &ResilienceDialect{Dialect: sqlite.New()} }

// IsTransient calls ErrFlakyOpen (and whatever wraps it) transient, and nothing else.
func (d *ResilienceDialect) IsTransient(err error) bool { return errors.Is(err, ErrFlakyOpen) }

// CreateConnection is the SQLite pool's driver behind a dial the decorator counts and can fail.
func (d *ResilienceDialect) CreateConnection(connectionString string) (*sql.DB, error) {
	d.mu.Lock()
	d.creates++
	d.mu.Unlock()
	inner, err := d.Dialect.CreateConnection(connectionString)
	if err != nil {
		return nil, err
	}
	registered := inner.Driver()
	_ = inner.Close() // nothing was dialed: only its driver is kept
	return sql.OpenDB(&resilienceConnector{dialect: d, driver: registered, dsn: sqlite.DataSourceName(connectionString)}), nil
}

// Creates is how many pools the dialect was asked for (one per open attempt of a session).
func (d *ResilienceDialect) Creates() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.creates
}

// Dials is how many connections its pools tried to open.
func (d *ResilienceDialect) Dials() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// Statements returns the statements observed since the last call and forgets them.
func (d *ResilienceDialect) Statements() []ObservedStatement {
	d.mu.Lock()
	defer d.mu.Unlock()
	statements := d.statements
	d.statements = nil
	return statements
}

func (d *ResilienceDialect) observe(ctx context.Context, sqlText string) {
	statement := ObservedStatement{SQL: sqlText}
	if deadline, ok := ctx.Deadline(); ok {
		statement.Deadline = deadline
		statement.Remaining = time.Until(deadline)
	}
	d.mu.Lock()
	d.statements = append(d.statements, statement)
	d.mu.Unlock()
}

type resilienceConnector struct {
	dialect *ResilienceDialect
	driver  driver.Driver
	dsn     string
}

func (c *resilienceConnector) Connect(context.Context) (driver.Conn, error) {
	c.dialect.mu.Lock()
	c.dialect.dials++
	failing := c.dialect.dials <= c.dialect.FailOpens
	failure := c.dialect.Failure
	c.dialect.mu.Unlock()
	if failing {
		if failure == nil {
			failure = ErrFlakyOpen
		}
		return nil, failure
	}
	inner, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &observingConn{inner: inner, dialect: c.dialect}, nil
}

func (c *resilienceConnector) Driver() driver.Driver { return c.driver }

// observingConn delegates to the modernc connection and records each statement's context deadline.
type observingConn struct {
	inner   driver.Conn
	dialect *ResilienceDialect
}

var (
	_ driver.QueryerContext     = (*observingConn)(nil)
	_ driver.ExecerContext      = (*observingConn)(nil)
	_ driver.ConnPrepareContext = (*observingConn)(nil)
	_ driver.ConnBeginTx        = (*observingConn)(nil)
	_ driver.Pinger             = (*observingConn)(nil)
)

func (c *observingConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *observingConn) Close() error { return c.inner.Close() }

// CheckNamedValue keeps the driver's own argument conversions.
func (c *observingConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.inner.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c *observingConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *observingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.inner.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *observingConn) Ping(ctx context.Context) error {
	if pinger, ok := c.inner.(driver.Pinger); ok {
		return pinger.Ping(ctx)
	}
	return nil
}

func (c *observingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.dialect.observe(ctx, query)
	stmt, err := c.inner.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &observingStmt{inner: stmt, query: query, dialect: c.dialect}, nil
}

func (c *observingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.dialect.observe(ctx, query)
	return c.inner.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *observingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.dialect.observe(ctx, query)
	return c.inner.(driver.ExecerContext).ExecContext(ctx, query, args)
}

// observingStmt records each execution of a prepared statement.
type observingStmt struct {
	inner   driver.Stmt
	query   string
	dialect *ResilienceDialect
}

var (
	_ driver.StmtExecContext  = (*observingStmt)(nil)
	_ driver.StmtQueryContext = (*observingStmt)(nil)
)

func (s *observingStmt) Close() error  { return s.inner.Close() }
func (s *observingStmt) NumInput() int { return s.inner.NumInput() }

func (s *observingStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, errors.New("testsupport: Stmt.Exec is deprecated; use ExecContext")
}

func (s *observingStmt) Query(args []driver.Value) (driver.Rows, error) {
	return nil, errors.New("testsupport: Stmt.Query is deprecated; use QueryContext")
}

func (s *observingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.dialect.observe(ctx, s.query)
	return s.inner.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (s *observingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.dialect.observe(ctx, s.query)
	return s.inner.(driver.StmtQueryContext).QueryContext(ctx, args)
}

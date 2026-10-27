package orm

import (
	"context"
	"database/sql"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The session's one statement funnel (Level 4, ADR-0047, spec/session.md "Resilience": "each implementation routes
// command creation through one place that applies the option, so no path can miss it"). Every statement the session
// sends goes through db.exec() — a statementExecutor over the current transaction or the pinned connection — or, for a
// dialect's one-round-trip message, through db.messageContext; both derive the statement's context with
// core.CommandContext and the session's CommandTimeout. database/sql binds a result set to the context it was opened
// under (the rows close when that context ends), so a query's deadline is released when its rows are closed, and the
// rows are read under the same deadline (CODING-STANDARD §10). With the option unset nothing is derived: the caller's
// context reaches the driver untouched, the Level 0–3 path exactly.

// statementExecutor runs statements on the session's current executor, each under the command timeout. It is the only
// statement surface the session's code sees (db.exec()); the raw executor is handed out only with a timed context
// around it (db.messageContext).
type statementExecutor struct {
	inner   core.SQLExecutor
	timeout time.Duration
}

// ExecContext runs a statement that returns no rows.
func (e statementExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, cancel := core.CommandContext(ctx, e.timeout)
	defer cancel()
	return e.inner.ExecContext(ctx, query, args...)
}

// QueryContext opens a result set; its deadline lasts until the rows are closed.
func (e statementExecutor) QueryContext(ctx context.Context, query string, args ...any) (*resultRows, error) {
	ctx, cancel := core.CommandContext(ctx, e.timeout)
	rows, err := e.inner.QueryContext(ctx, query, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	return &resultRows{Rows: rows, release: cancel}, nil
}

// PrepareContext prepares a statement (one round trip, under the timeout); each execution of it runs under the
// timeout again.
func (e statementExecutor) PrepareContext(ctx context.Context, query string) (*preparedStatement, error) {
	prepareCtx, cancel := core.CommandContext(ctx, e.timeout)
	defer cancel()
	stmt, err := e.inner.PrepareContext(prepareCtx, query)
	if err != nil {
		return nil, err
	}
	return &preparedStatement{stmt: stmt, timeout: e.timeout}, nil
}

// preparedStatement is a prepared command whose every execution carries the command timeout.
type preparedStatement struct {
	stmt    *sql.Stmt
	timeout time.Duration
}

func (s *preparedStatement) ExecContext(ctx context.Context, args ...any) (sql.Result, error) {
	ctx, cancel := core.CommandContext(ctx, s.timeout)
	defer cancel()
	return s.stmt.ExecContext(ctx, args...)
}

func (s *preparedStatement) QueryContext(ctx context.Context, args ...any) (*resultRows, error) {
	ctx, cancel := core.CommandContext(ctx, s.timeout)
	rows, err := s.stmt.QueryContext(ctx, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	return &resultRows{Rows: rows, release: cancel}, nil
}

func (s *preparedStatement) Close() error { return s.stmt.Close() }

// resultRows is a result set the session opened under the command timeout: every *sql.Rows method, and a Close that
// also releases the statement's deadline. Every reader defers Close.
type resultRows struct {
	*sql.Rows
	release context.CancelFunc
}

// Close closes the rows, then releases the statement's context.
func (r *resultRows) Close() error {
	err := r.Rows.Close()
	r.release()
	return err
}

// messageContext is the context one planned message runs under when a dialect sends it as one round trip
// (core.StatementBatcher.SendMessage): a multi-statement message carries the same timeout as one statement does
// (spec/session.md "Resilience"). The realization reads every result before it returns, so the caller defers the
// cancel around the call.
func (db *Db) messageContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return core.CommandContext(ctx, db.timeout)
}

// rawExecutor is the current transaction or the pinned connection, for the dialect's one-round-trip realization only:
// it runs under messageContext.
func (db *Db) rawExecutor() core.SQLExecutor {
	if db.tx != nil {
		return db.tx
	}
	return db.connection
}

// toolingContext is ctx carrying the session's statement log and command timeout, for the tooling code the session
// calls (SchemaGuard's migration-history check and introspection, which run through the migrations package).
func (db *Db) toolingContext(ctx context.Context) context.Context {
	return core.WithCommandTimeout(core.WithStatementLog(ctx, db.log), db.timeout)
}

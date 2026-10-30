package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	// The one import of the driver (CODING-STANDARD §1): pgx's connection configuration and its database/sql adapter.
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// openPool opens the driver-level pool for a native PostgreSQL connection
// string (a URL or key/value DSN): pgx's connector wrapped by the
// placeholder-rewriting driver below. Nothing connects until first use. It is
// not registered with database/sql.Register — sql.OpenDB takes the connector
// directly, so the package keeps no init and no global state.
func openPool(connectionString string, cachedPlan CachedPlanPolicy) (*sql.DB, error) {
	config, err := pgx.ParseConfig(connectionString)
	if err != nil {
		return nil, err
	}
	// A statement whose context ends — the session's CommandTimeout (ADR-0047) or the caller's cancellation — is
	// cancelled on the server (a CancelRequest), and the connection waits for the server's answer (57014) instead of
	// severing the socket at once, pgx's default: the session owns one pinned connection, and "the session's next
	// statement runs" (spec/session.md "Resilience"). The socket deadline stays as the fallback for a server that does
	// not answer the cancel within cancelGrace.
	config.BuildContextWatcherHandler = func(pgConn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: pgConn, DeadlineDelay: cancelGrace}
	}
	return sql.OpenDB(&connector{inner: stdlib.GetConnector(*config), cachedPlan: cachedPlan}), nil
}

// cancelGrace is how long a connection whose statement was cancelled waits for the server's answer before pgx severs
// the socket.
const cancelGrace = 5 * time.Second

// connector wraps pgx's driver.Connector so every connection it makes is a [conn].
type connector struct {
	inner      driver.Connector
	cachedPlan CachedPlanPolicy
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	inner, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	pgxConn, ok := inner.(*stdlib.Conn)
	if !ok {
		_ = inner.Close()
		return nil, fmt.Errorf("postgres: pgx returned a %T connection, expected *stdlib.Conn", inner)
	}
	return &conn{inner: pgxConn, cachedPlan: c.cachedPlan}, nil
}

func (c *connector) Driver() driver.Driver { return c.inner.Driver() }

// rewritten is a statement after the `@name` → `$n` rewrite: names[i] is the
// lower-cased placeholder bound as $(i+1). A statement with no placeholders
// keeps its text and has no names.
type rewritten struct {
	sql   string
	names []string
}

// rewritePlaceholders replaces every real `@name` occurrence (not inside a
// literal or comment) with `$n`, numbering names by first appearance and
// treating names case-insensitively, as the binder does.
func rewritePlaceholders(sqlText string) rewritten {
	// One scan (core.PlaceholderSpans, ADR-0048): the spans arrive in text order, and a name's ordinal is its first
	// appearance case-insensitively — the numbering the per-name scans produced, without a scan per name.
	spans := core.PlaceholderSpans(sqlText)
	if len(spans) == 0 {
		return rewritten{sql: sqlText}
	}

	var names []string
	ordinal := map[string]int{}
	var b strings.Builder
	b.Grow(len(sqlText))
	last := 0
	for _, span := range spans {
		key := strings.ToLower(span.Name)
		n, seen := ordinal[key]
		if !seen {
			names = append(names, key)
			n = len(names)
			ordinal[key] = n
		}
		b.WriteString(sqlText[last:span.Index])
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
		last = span.Index + span.Length
	}
	b.WriteString(sqlText[last:])
	return rewritten{sql: b.String(), names: names}
}

// arrange puts the named values in `$n` order. A statement without
// placeholders gets its arguments as they are; with placeholders, every one
// needs a named value and every named value a placeholder — the strictness of
// §7.13, kept at the driver as well.
func (r rewritten) arrange(args []driver.NamedValue) ([]driver.NamedValue, error) {
	if len(r.names) == 0 {
		return args, nil
	}
	byName := make(map[string]driver.Value, len(args))
	for _, arg := range args {
		if arg.Name == "" {
			return nil, fmt.Errorf("postgres: positional argument %d beside @name placeholders; bind every value with sql.Named", arg.Ordinal)
		}
		byName[strings.ToLower(arg.Name)] = arg.Value
	}
	arranged := make([]driver.NamedValue, len(r.names))
	for i, name := range r.names {
		value, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("postgres: placeholder @%s has no bound argument", name)
		}
		delete(byName, name)
		arranged[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	if len(byName) > 0 {
		extra := make([]string, 0, len(byName))
		for name := range byName {
			extra = append(extra, "@"+name)
		}
		sort.Strings(extra)
		return nil, fmt.Errorf("postgres: argument(s) %s have no placeholder in the statement", strings.Join(extra, ", "))
	}
	return arranged, nil
}

// conn is one pgx connection behind the rewriting layer. Everything but the
// placeholder rewrite and the cached-plan retry is pgx's own behaviour, delegated.
type conn struct {
	inner      *stdlib.Conn
	cachedPlan CachedPlanPolicy
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)
)

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	r := rewritePlaceholders(query)
	prepared, err := c.inner.PrepareContext(ctx, r.sql)
	if err != nil {
		return nil, err
	}
	return &stmt{inner: prepared.(*stdlib.Stmt), names: r.names}, nil
}

func (c *conn) Close() error { return c.inner.Close() }

func (c *conn) Begin() (driver.Tx, error) { return c.inner.Begin() }

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.inner.BeginTx(ctx, opts)
}

func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	r := rewritePlaceholders(query)
	arranged, err := r.arrange(args)
	if err != nil {
		return nil, err
	}
	result, err := c.inner.ExecContext(ctx, r.sql, arranged)
	if c.retriesCachedPlan(err) {
		result, err = c.inner.ExecContext(ctx, r.sql, arranged)
	}
	if err != nil {
		return nil, c.surfaceCachedPlan(query, err)
	}
	return result, nil
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r := rewritePlaceholders(query)
	arranged, err := r.arrange(args)
	if err != nil {
		return nil, err
	}
	rows, err := c.inner.QueryContext(ctx, r.sql, arranged)
	if c.retriesCachedPlan(err) {
		rows, err = c.inner.QueryContext(ctx, r.sql, arranged)
	}
	if err != nil {
		return nil, c.surfaceCachedPlan(query, err)
	}
	return rows, nil
}

// isCachedPlanError reports PostgreSQL refusing a cached prepared statement whose result type
// changed under it: SQLSTATE 0A000, "cached plan must not change result type".
func isCachedPlanError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "0A000" &&
		strings.HasPrefix(pgErr.Message, "cached plan must not change result type")
}

// retriesCachedPlan reports whether err is the cached-plan error, the policy is
// CachedPlanRetryOnce, and no transaction is open on the connection — then the statement ran in
// its own implicit transaction and nothing of it happened, and pgx has already invalidated its
// cache entry (it does so on every error: Conn.exec's deferred Invalidate, Rows.Close), so one more
// run re-prepares it. Inside a transaction (TxStatus 'T' or the aborted 'E') PostgreSQL has aborted
// the transaction, and the error surfaces as TX-002. Statements the caller prepared explicitly
// (stmt) are never retried: their server-side statement is what changed.
func (c *conn) retriesCachedPlan(err error) bool {
	return c.cachedPlan == CachedPlanRetryOnce && isCachedPlanError(err) &&
		c.inner.Conn().PgConn().TxStatus() == 'I'
}

// surfaceCachedPlan turns a cached-plan error that was not (or could not be) retried into TX-002
// (spec/errors.md), naming the statement and saying what fixes it, with pgx's error kept as the
// cause; every other error passes through unchanged. The rerun the message asks for works because
// pgx dropped the stale statement from this connection's cache on the error — the library holds
// no statement cache of its own.
func (c *conn) surfaceCachedPlan(query string, err error) error {
	if !isCachedPlanError(err) {
		return err
	}
	remedy := "rerun the statement (the driver dropped its stale prepared copy, so the rerun re-prepares it against the new table shape) or open a new session"
	if c.inner.Conn().PgConn().TxStatus() != 'I' {
		remedy = "PostgreSQL has aborted the current transaction: roll it back and rerun the whole transaction (the driver dropped its stale prepared copy, so the rerun re-prepares it against the new table shape), or open a new session"
	}
	return &core.Error{
		Code:    "TX-002",
		Target:  query,
		Message: "the schema changed under a statement this connection had prepared (SQLSTATE 0A000, cached plan must not change result type); " + remedy,
		Cause:   err,
	}
}

func (c *conn) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }

func (c *conn) ResetSession(ctx context.Context) error { return c.inner.ResetSession(ctx) }

// CheckNamedValue accepts every value as it is: pgx encodes typed slices, time.Time, strings, and []byte natively.
func (c *conn) CheckNamedValue(value *driver.NamedValue) error { return c.inner.CheckNamedValue(value) }

// stmt is a prepared statement whose placeholders were rewritten at prepare time.
type stmt struct {
	inner *stdlib.Stmt
	names []string
}

func (s *stmt) Close() error { return s.inner.Close() }

func (s *stmt) NumInput() int {
	if len(s.names) > 0 {
		return len(s.names)
	}
	return s.inner.NumInput()
}

func (s *stmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("postgres: Stmt.Exec is deprecated; use ExecContext")
}

func (s *stmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, fmt.Errorf("postgres: Stmt.Query is deprecated; use QueryContext")
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	arranged, err := rewritten{names: s.names}.arrange(args)
	if err != nil {
		return nil, err
	}
	return s.inner.ExecContext(ctx, arranged)
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	arranged, err := rewritten{names: s.names}.arrange(args)
	if err != nil {
		return nil, err
	}
	return s.inner.QueryContext(ctx, arranged)
}

// IsTransient answers whether a failed open may succeed on another attempt (ADR-0047, spec/dialects.md "Transient
// open failures"): a *pgconn.PgError anywhere in the cause chain whose SQLSTATE is class 08 (every connection
// exception), 53300 (too_many_connections), or 57P03 (cannot_connect_now), else a socket, I/O, or timeout failure of
// the Go runtime (core.IsTransientTransport) — pgx reports an unreachable server as a *pgconn.ConnectError wrapping
// the dial's *net.OpError. Not transient: 28xxx (authentication), 3D000 (no such database). An engine answer settles
// it: a PgError with another SQLSTATE is what the server will say again, whatever transport failure the chain also
// carries (pgx joins the failures of every address it tried; CODING-STANDARD §10). A pure function of the failure;
// never asked about a statement.
func (*Dialect) IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		code := pgErr.Code
		return strings.HasPrefix(code, "08") || code == "53300" || code == "57P03"
	}
	return core.IsTransientTransport(err)
}

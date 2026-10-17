package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"strings"

	// The one import of the driver (CODING-STANDARD §1): pgx's connection configuration and its database/sql adapter.
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	return sql.OpenDB(&connector{inner: stdlib.GetConnector(*config), cachedPlan: cachedPlan}), nil
}

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
	distinct := core.FindPlaceholders(sqlText)
	if len(distinct) == 0 {
		return rewritten{sql: sqlText}
	}

	var names []string
	ordinal := map[string]int{}
	for _, name := range distinct {
		key := strings.ToLower(name)
		if _, seen := ordinal[key]; !seen {
			names = append(names, key)
			ordinal[key] = len(names)
		}
	}

	type occurrence struct {
		index, length, ordinal int
	}
	var occurrences []occurrence
	for _, name := range names {
		for _, span := range core.PlaceholderOccurrences(sqlText, name) {
			occurrences = append(occurrences, occurrence{span.Index, span.Length, ordinal[name]})
		}
	}
	sort.Slice(occurrences, func(i, j int) bool { return occurrences[i].index < occurrences[j].index })

	var b strings.Builder
	last := 0
	for _, o := range occurrences {
		b.WriteString(sqlText[last:o.index])
		b.WriteByte('$')
		b.WriteString(fmt.Sprint(o.ordinal))
		last = o.index + o.length
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
		return c.inner.ExecContext(ctx, r.sql, arranged)
	}
	return result, err
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	r := rewritePlaceholders(query)
	arranged, err := r.arrange(args)
	if err != nil {
		return nil, err
	}
	rows, err := c.inner.QueryContext(ctx, r.sql, arranged)
	if c.retriesCachedPlan(err) {
		return c.inner.QueryContext(ctx, r.sql, arranged)
	}
	return rows, err
}

// retriesCachedPlan reports whether err is PostgreSQL refusing a cached prepared statement whose
// result type changed under it (SQLSTATE 0A000, "cached plan must not change result type") and the
// policy is CachedPlanRetryOnce and no transaction is open on the connection — then the statement
// ran in its own implicit transaction and nothing of it happened, and pgx has already invalidated
// its cache entry (it does so on every error), so one more run re-prepares it. Inside a
// transaction (TxStatus 'T' or the aborted 'E') PostgreSQL has aborted the transaction: the error
// surfaces and the caller must roll back. Statements the caller prepared explicitly (stmt) are
// never retried: their server-side statement is what changed.
func (c *conn) retriesCachedPlan(err error) bool {
	if err == nil || c.cachedPlan != CachedPlanRetryOnce {
		return false
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "0A000" || !strings.HasPrefix(pgErr.Message, "cached plan must not change result type") {
		return false
	}
	return c.inner.Conn().PgConn().TxStatus() == 'I'
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

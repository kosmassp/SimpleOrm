// Package postgres is the PostgreSQL dialect of the Go port (ADR-0025,
// spec/dialects.md) — the C# SimpleOrm.Postgres assembly. It is backed by
// github.com/jackc/pgx/v5, the Go analog of Npgsql, referenced only from
// driver.go.
//
// # Placeholders
//
// The whole port binds `sql.Named("name", v)` arguments against `@name`
// placeholders (CODING-STANDARD §5). pgx's database/sql adapter accepts only
// `$1…$n` and rejects named arguments, and the Dialect seam has no member for
// this (and must not gain one), so the dialect realizes it inside this package:
// CreateConnection returns a pool over a thin driver (driver.go) that wraps
// pgx's connector and, for every query, exec, and prepare, rewrites the `@name`
// placeholders to `$n` positions — numbered by first appearance, a name used
// twice sharing one position — and reorders the named values to match. The
// scan is the port's one placeholder scanner (core.FindPlaceholders), so
// lookalikes inside string literals and comments are left alone. Statements
// with no `@name` placeholder, or called with no named arguments, pass through
// untouched, so hand-written `$1` SQL and the migration runner's DDL work as
// they are.
//
// # Describing statements
//
// DescribeStatement (core.StatementDescriber) prepares the rewritten statement
// as the unnamed server-side statement — parse and describe, never execute —
// and resolves the result columns' type names, base tables, and column names
// through one catalog query (format_type, pg_class, pg_attribute).
//
// # Temporals
//
// pgx scans timestamptz and timestamp both into time.Time, so the UTC marker of
// spec/dialects.md "Temporals" lives in the column's type, which is what
// IsMarkerlessTemporal (core.TemporalMarkers) judges: the result mapper refuses
// VAL-020 for a value read from a `timestamp without time zone` column into a
// datetime property with no registered handler.
//
// # Cached plans
//
// pgx prepares and caches every statement that has arguments, per connection,
// and a session pins one connection for its life. When the table under a
// cached statement changes (a column's type, say), PostgreSQL refuses the next
// run with SQLSTATE 0A000 "cached plan must not change result type"; pgx
// invalidates the entry on any error but never retries. The default policy,
// CachedPlanRetryOnce, runs the statement once more when no transaction is
// open on the connection; inside one PostgreSQL has aborted the transaction,
// so the error surfaces and the caller rolls back. CachedPlanSurface
// (New(WithCachedPlanPolicy(CachedPlanSurface))) never retries. A driver
// matter of this package, not a seam member (spec/dialects.md Clarifications,
// ADR-0040 add.1).
package postgres

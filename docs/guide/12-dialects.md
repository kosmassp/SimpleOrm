# Dialects

A dialect provides the connection, quotes identifiers, renders generated SQL,
declares its capabilities, and supplies the migration run lock. Your code is
the same on every dialect; what differs is under the hood, and this page lists
it. The seam member by member, the three realizations, and the live-testing
contract are in [`spec/dialects.md`](../../spec/dialects.md).

## Choosing one

```csharp
await using var db = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect() }, ct);

await using var db = await Db.OpenAsync(
    "Server=.;Database=app;Integrated Security=true",
    new DbOptions { Dialect = new SqlServerDialect() }, ct);

await using var db = await Db.OpenAsync(
    "Host=localhost;Username=app;Password=...;Database=app",
    new DbOptions { Dialect = new PostgresDialect() }, ct);
```

Each dialect takes its driver's native connection string. The CLI selects by
name, `--dialect sqlite|sqlserver|postgres`, default `sqlite`.

In PHP, one namespace `SimpleOrm\Dialect` over PDO; each takes its PDO DSN verbatim:

```php
new DbOptions(new SqliteDialect());      // 'app.db', 'sqlite:app.db', or 'Data Source=app.db'
new DbOptions(new SqlServerDialect());   // 'sqlsrv:Server=host;Database=app;Encrypt=no' or an 'odbc:...' DSN (pdo_sqlsrv / pdo_odbc)
new DbOptions(new PostgresDialect());    // 'pgsql:host=localhost;port=5432;dbname=app;user=app;password=...' (pdo_pgsql)
```

In Go, one package per dialect, each importing exactly one driver:

```go
orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New()})
orm.Open(ctx, "sqlserver://user:pass@host:1433?database=app", orm.Options{Dialect: sqlserver.New()}) // or an ADO-style server=...;database=... string
orm.Open(ctx, "postgres://user:pass@localhost:5432/app", orm.Options{Dialect: postgres.New()})       // or a key/value DSN
```

## SQLite (the reference)

- The reference rendering: identifiers unquoted, `limit`/`offset`, `RETURNING`
  for generated keys (SQLite 3.35+; the bundled native library is 3.46+).
- Generated tables are STRICT (3.37+), which is what makes declared types and
  `VAL-011` real on SQLite. Keep hand-written tables STRICT too.
- Dates are ISO-8601 UTC TEXT with a trailing `Z`; decimals are TEXT; GUIDs
  are TEXT; booleans are INTEGER 0/1.
- No array parameters: `IN (@Ids)` expands to generated placeholders.
- No materialized views, no procedures: those declarations are dormant.
- The migration run lock is one `BEGIN IMMEDIATE` transaction.
- Every tooling command works: `snapshot`, `shadow`, `diff`, `migrate --force`.
- C# `Microsoft.Data.Sqlite`; PHP `pdo_sqlite`; Go `modernc.org/sqlite` (pure
  Go, opened with `_txlock=immediate` so transactions are `BEGIN IMMEDIATE` too).

## SQL Server

- Identifiers are always bracketed (legacy schemas are full of reserved words).
- Generated keys come back via `scope_identity()` (trigger-safe); paging renders
  `offset ... rows fetch next ... rows only`, and an unordered page gains
  `order by (select null)`; composite subquery membership rewrites as a
  correlated EXISTS (no row-value `IN` in T-SQL).
- **Dates are `datetimeoffset`.** It carries the UTC marker and reads back as
  `DateTime` with `Kind == Utc`. A legacy `datetime`, `datetime2`, or
  `smalldatetime` column is markerless and reads back refusing with `VAL-020`;
  if you know such a column's kind, register an `ITypeHandler<DateTime>` that
  declares it.
- Generated DDL uses `nvarchar(max)` for strings (`nvarchar(450)` for indexed
  columns and keys) and `decimal(38,9)`; declare real precision and lengths on
  the model with the storage facets, `[Column(Length = 50)]` and
  `[Column(Precision = 18, Scale = 2)]` (ADR-0043), which snapshots, `diff`,
  and force sync read. Composite `nvarchar(450)` keys can exceed the 900-byte
  index limit.
- **The affected-row rule.** Generated UPDATE and DELETE renderings become
  `set nocount on; <statement>; select @@rowcount; set nocount off` and the
  session judges that scalar, so a trigger's rows can never let a stale write
  pass as a success (`ReportsAffectedRowsBySelect`). Registry commands keep the
  driver's count. See [CRUD](04-crud-and-concurrency.md#the-sql-server-affected-row-rule).
- Triggers render as one statement-level trigger over `inserted`/`deleted`
  with `set nocount on`; `Before` has no form there (`DDL-005`); `InsteadOf`
  on a table is allowed.
- The migration run lock is one transaction holding `sp_getapplock` with a
  60s timeout.
- **The process must not run in globalization-invariant mode.**
  `Microsoft.Data.SqlClient` refuses every connection with "Globalization
  Invariant Mode is not supported"; set `InvariantGlobalization` to false in
  any project using this dialect (the CLI and the tests do).
- `Encrypt` and `TrustServerCertificate`: `Microsoft.Data.SqlClient` defaults
  to `Encrypt=Mandatory` since 4.0, which refuses a self-signed certificate.
  The test fixture uses `Encrypt=false` for an in-process LocalDB; for a LAN
  server with a self-signed certificate add `TrustServerCertificate=true`, or
  install a trusted certificate.
- C# `Microsoft.Data.SqlClient` (5.1.x on `netstandard2.0`, 6.x on `net10.0`);
  PHP `pdo_sqlsrv` or `pdo_odbc`; Go `github.com/microsoft/go-mssqldb`.

## PostgreSQL

- Identifiers are always double-quoted; generated keys use
  `generated by default as identity ... returning`; storage types are the
  `format_type` spellings (`numeric` unconstrained, `text` for strings).
- **Array parameters, realized.** Write the SQL natively, `where id = any(@Ids)`,
  and a collection-typed args property binds as one typed array parameter. No
  placeholder expansion, no parameter ceilings; an empty list is a typed empty
  array and matches no rows.
- **Native temporals.** `timestamp with time zone` is the UTC-marked storage;
  values bind natively as UTC (`BindsTemporalsNatively`: PostgreSQL refuses
  `timestamptz >= text`) and read back `DateTime` with `Kind == Utc`. A legacy
  `timestamp without time zone` refuses with `VAL-020` unless a handler declares
  its kind.
- **Materialized views work**: `[MaterializedView]` entities create and read
  like any relation; refresh scheduling stays yours.
- Triggers render as a PL/pgSQL function `<name>_fn` plus the trigger;
  `InsteadOf` is allowed on views only.
- The migration run lock is one transaction holding `pg_advisory_xact_lock`
  under a 60s `lock_timeout`; DDL is transactional, so a failed run applies nothing.
- C# `Npgsql` (4.1.x on `netstandard2.0` with legacy-temporal caveats, 9.x on
  `net10.0`; prefer `net10.0`). Npgsql does not auto-prepare by default, so
  the cached-plan rule below does not apply there.
- PHP `pdo_pgsql`: a collection binds as a PostgreSQL array literal under an
  untyped parameter; the SQL stays `= any(:ids)`.
- Go `github.com/jackc/pgx/v5` through `database/sql`: a package-local
  connector rewrites `@name` to `$n`; a slice binds as a typed slice.

### The pgx cached plan and `TX-002` (Go)

pgx caches prepared statements per connection. When a table under a cached
statement changes shape, PostgreSQL answers SQLSTATE `0A000` ("cached plan
must not change result type"). The Go dialect retries the statement once when
no transaction is active (the failed statement ran in its own implicit
transaction, so nothing happened); inside a transaction PostgreSQL has already
aborted it and the error surfaces as `TX-002`, which names the statement,
keeps pgx's error as its wrapped cause, and says to roll back and rerun the
transaction or open a new session. The policy is configurable:

```go
postgres.New(postgres.WithCachedPlanPolicy(postgres.CachedPlanRetryOnce | postgres.CachedPlanSurface))
```

C# and PHP are unaffected: Npgsql does not auto-prepare by default, and PDO
prepares each statement per call.

## PHP driver notes

- `SqlServerDialect` takes a `sqlsrv:` or `odbc:` DSN verbatim and never
  emulates prepares (emulation would interpolate string literals without the
  `N` prefix and mangle non-ASCII text on the way to `nvarchar`).
- Through `pdo_odbc` with ODBC Driver 17, parameter text is converted on the
  client: temporals must be sent as `yyyy-mm-dd hh:mm:ss.fffffff +hh:mm` (a `T`
  or `Z` is SQLSTATE 22018), and `varbinary` arrives as hexadecimal text. The
  dialect declares these conventions (`PdoConventions`: `TemporalText::Odbc`,
  `BinaryText::Hex`) and the converter honors them; nothing changes in your code.
- Binary values bind as `PDO::PARAM_LOB`, chosen by the metadata token: an args
  property for a bytes column declares `#[Column(type: ColumnType::Bytes)]`.
- `composer test` stays `pdo_sqlite`-only; `composer test-live` loads
  `pdo_pgsql` and `pdo_odbc` for the live legs.

## Go driver notes

- `modernc.org/sqlite` is pure Go, no cgo.
- `go-mssqldb` returns `decimal`/`numeric`/`money` as ASCII bytes (accepted by
  the converter), `datetime2` as a `time.Time` without a marker (refused
  `VAL-020` by the column's declared type), and announces an untyped nil as
  `nvarchar(1)`, which is why the binder types every NULL from the metadata
  token. `uniqueidentifier` reads in RFC 4122 byte order (`GuidConversion`).
- pgx caches statements (above) and speaks only `$n` parameters (rewritten by
  the dialect's connector).
- Run the tests with `-p 2` on a machine where the SQLite file locks contend.

## Statement batching

`Batching = Auto` (ADR-0044; [Tracking and the unit of work](07-tracking-and-unit-of-work.md#batching))
plans the same messages on every dialect; the driver decides how a message
travels. `db.BatchingInEffect` tells you which realization you got.

| | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| C# (`net10.0`) | one by one, one prepared command per statement text | `SqlBatch`: one TDS message, one RPC per statement | `NpgsqlBatch`: pipelined, one `Sync` |
| C# (`netstandard2.0`) | one by one | one by one | one by one |
| Go | one by one | one multi-statement text, placeholders renamed apart | pgx `SendBatch` |
| PHP | one by one | one multi-statement text with `nextRowset()` | one by one (`pdo_pgsql` cannot) |

- **SQLite** has no network round trip to save; `Auto` reuses prepared
  commands within the call instead, which measures faster than a new command
  per row.
- **SQL Server** caps one message at 2098 bound parameters (`sp_executesql`
  takes 2100, two of them its own). Every generated update and delete reads
  its count with `select @@rowcount`, so after a registry command in a message
  the next such statement starts a new one. The SQL Server batching legs are
  written and skip until an engine is reachable.
- **PostgreSQL** in C# sends a registry command of several statements on its
  own (Npgsql refuses several statements in one batch command); the plan is
  unchanged.

## Typed nulls

A NULL parameter carries the SQL type its property's metadata token names,
never an untyped null, in every implementation: `Binary` for bytes, `String`
for a temporal bound as text, the numeric type otherwise. This exists because
SQL Server refuses to convert an untyped (character) NULL into `varbinary`.

## What each dialect does not support

| | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| Array parameters | expansion instead | expansion instead | native |
| Materialized views | no (`DDL-002`) | declared, dormant | yes |
| Procedures | no | declared, dormant | declared, dormant |
| Row-value `IN` | yes | EXISTS rewrite | yes |
| `Before` triggers | yes | no (`DDL-005`) | yes |
| `InsteadOf` on a table | no (`DDL-005`) | yes | no (`DDL-005`) |
| `snapshot`/`shadow`/`diff`/force sync | yes | yes (ADR-0043; `shadow --db` is the admin connection; pinned, live run pending an engine) | yes (ADR-0043, live) |
| Derived `migrate down` | yes | yes (ADR-0043) | yes (ADR-0043) |
| Statement batching that saves a round trip (`BatchingInEffect`) | no (no round trip to save; prepared-command reuse) | yes (C# `net10.0`, Go, PHP; live run pending an engine) | yes (C# `net10.0`, Go; not PHP) |
| `REFRESH MATERIALIZED VIEW`, `jsonb`, array columns, batched reads, multi-row inserts | no | no | not yet |

## Pitfalls

- `VAL-020` reading a `datetime2` or `timestamp without time zone` column:
  the storage has no marker. Migrate to `datetimeoffset`/`timestamptz`, or
  register a handler that declares the kind.
- "Globalization Invariant Mode is not supported" on SQL Server: turn
  `InvariantGlobalization` off.
- A certificate error opening SQL Server: `Encrypt=false` for LocalDB, or
  `TrustServerCertificate=true` for a self-signed LAN server.
- `bigint = any(text[])` on PostgreSQL: an empty collection still binds a
  typed array, and the element type comes from the args property's type, so
  declare it as the column's type.
- `TX-002` in Go after a migration ran while a session stayed open: roll back
  and rerun the transaction, or open a new session.
- `MIG-014` on `migrate down`, `diff`, or `shadow --from`: a snapshot stamped
  for another dialect was offered — a tree and its snapshots belong to one
  dialect; rerun `snapshot`/`shadow` with the right `--dialect`.
- Registry SQL is dialect-specific by nature (`IN (@Ids)` versus `= any(@Ids)`);
  criteria queries and generated CRUD are not.

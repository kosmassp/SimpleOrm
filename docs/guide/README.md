# SimpleOrm developer guide

SimpleOrm is a SQL-first micro-ORM. The database is the source of truth: your
classes conform to the schema, and schema changes are explicit migration files.
Real SQL is first-class: hand-written queries map through the same pipeline as
generated ones, and there is no second-class query language. There are no hidden
queries: every round trip is a visible call in your code, and nothing loads on
property access. It is strict by default: a column with no property, a property
with no column, an unused parameter, or a date without a UTC marker is an error,
never a silent null. Every error carries a stable code (`MAP-001`, `CRUD-010`,
...) plus the query, column, property, or parameter involved and what was expected.

This guide is for application developers using the library. The language-neutral
rules live in [`spec/`](../../spec/README.md); the project history lives in
[`docs/decisions.md`](../decisions.md). Neither is required reading to use the
library, but the guide links into the spec wherever an exact rule matters.

## Three implementations, three dialects

| Implementation | Where | Open a session |
|---|---|---|
| C# (the reference) | `dotnet/src/SimpleOrm` + one dialect package | `await Db.OpenAsync(cs, new DbOptions { Dialect = new SqliteDialect() }, ct)` |
| PHP | `php/src` (`simpleorm/simpleorm`) | `Db::open($dsn, new DbOptions(new SqliteDialect()))` |
| Go | `go/orm` | `orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New()})` |

| Dialect | C# package | PHP | Go |
|---|---|---|---|
| SQLite (reference) | `SimpleOrm.Sqlite` | `SimpleOrm\Dialect\SqliteDialect` (`pdo_sqlite`) | `orm/sqlite` (`modernc.org/sqlite`) |
| SQL Server | `SimpleOrm.SqlServer` | `SqlServerDialect` (`pdo_sqlsrv` or `pdo_odbc`) | `orm/sqlserver` (`go-mssqldb`) |
| PostgreSQL | `SimpleOrm.Postgres` | `PostgresDialect` (`pdo_pgsql`) | `orm/postgres` (`pgx`) |

All three implementations pass the same conformance files; every page shows the
C# form first and then the PHP and Go equivalents where the shape differs.

## Start here

1. [Getting started](01-getting-started.md): reference the library, open a session, map one entity, run one query, insert one row.
2. [Mapping entities](02-mapping-entities.md): what `[Column]`, `[Key]`, `[Generated]`, and `[Version]` mean, and how types and dates convert.
3. [Queries and commands](03-queries-and-commands.md): the registry, parameters, `IN` lists, and strictness.
4. [CRUD and concurrency](04-crud-and-concurrency.md): generated writes and the version column.
5. [Schema guard](11-schema-guard.md): run it at startup and in a test, and read its report.
6. [Migrations](09-migrations.md): the everyday loop with `diff`, `migrate`, and `snapshot`.

Then pick what your application needs: criteria queries, relationships,
tracking and the unit of work, hooks, triggers, another dialect, or the
[statement log](16-observability.md) when you want to see the SQL going out.

## Table of contents

| Page | What it covers |
|---|---|
| [01 Getting started](01-getting-started.md) | Installing by project reference, opening a session, the first entity, query, and insert, the temp-file SQLite test pattern |
| [02 Mapping entities](02-mapping-entities.md) | Opt-in `[Column]` mapping, keys, versions, naming, nullability, the type table, dates, JSON columns, owned types, views and statements, the manual builder, base classes |
| [03 Queries and commands](03-queries-and-commands.md) | `Query.Inline`/`Query.Embedded`, args records, `@name` parameters, `IN` lists, the read and execute methods, nested JSON results, strictness errors |
| [04 CRUD and concurrency](04-crud-and-concurrency.md) | `InsertAsync`, `GetAsync`, `UpdateAsync`, `UpdateOnlyAsync`, `DeleteAsync`, the version column, every `CRUD-` code |
| [05 Criteria queries](05-criteria-queries.md) | `db.Query<T>()` with `Criteria.*`, ordering and paging, null semantics, dynamic composition, why there is no LINQ |
| [06 Relationships and loading](06-relationships-and-loading.md) | Declaring the four cardinalities, `LoadAsync`/`LoadEachAsync`, `Include(...).Fetch(...)`, choosing a fetch mode |
| [07 Tracking and unit of work](07-tracking-and-unit-of-work.md) | `TrackingMode.Tracked`, the identity map, dirty updates, `Persist`/`Remove`/`FlushAsync`, cascades, detach and merge |
| [08 Hooks](08-hooks.md) | `[CreatedAt]`, `[UpdatedAt]`, `[SoftDelete]`, `DbOptions.Clock` |
| [09 Migrations](09-migrations.md) | Migrations as code, the version table, checksums, derived rollbacks, `diff`, `snapshot`, `shadow`, the CLI |
| [10 Triggers](10-triggers.md) | Trigger declaration classes, the neutral body tokens, per-dialect rendering, trigger migrations |
| [11 Schema guard](11-schema-guard.md) | `SchemaGuard.ValidateAsync`, what it checks, the `-- notnull:` hint, reading the report |
| [12 Dialects](12-dialects.md) | SQLite, SQL Server, and PostgreSQL: connection strings, what differs, what each lacks |
| [13 Errors](13-errors.md) | How errors are shaped, the prefixes, the common codes and their fixes |
| [14 Testing your app](14-testing-your-app.md) | Temp-file databases per language, SchemaGuard in a test, live legs, a fixed clock |
| [15 PHP and Go](15-php-and-go.md) | The port-specific shapes in one place |
| [16 Observability](16-observability.md) | The statement log: see every statement as it is sent, forward it to your logger, parameter values off by default, the CLI's `--log` |

## Conventions in this guide

- Code comes from the sample projects (`dotnet/samples/SimpleOrm.Sample`,
  `php/samples/SimpleOrm.Sample`, `go/orm/sample`) and the test suites. The
  fixture entities are `User`, `Role`, `UserRole`, `UserProfile` with its owned
  `Address`, `Transaction`, `TransactionDetail`, `RoleChange`, the view
  `UserTransactionTotal`, and the statement `DailySales`.
- `ct` is a `CancellationToken`; `ctx` is a `context.Context`.
- Error codes are written in backticks. The full registry is
  [`spec/errors.md`](../../spec/errors.md).
- Nothing is published to NuGet, Packagist, or as a tagged Go module at the
  time of writing; every implementation is consumed by project reference.

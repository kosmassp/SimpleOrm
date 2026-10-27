# ORM Project Brief — .NET reference implementation, SQLite first

> **Project name: SimpleOrm.** SDK: .NET 10 (10.0.400). Reference database: **SQLite** (ADR-0003; PostgreSQL was the original reference and returns as a Level 4 dialect). Claude Code reads this file at the start of every session, so these rules stay in force without re-pasting.

---

## 1. Vision

A SQL-first ORM that grows in levels: a mapper (Level 0), a micro-ORM with migrations and schema rules (Level 1), relationships and a query model (Level 2), full session semantics comparable to Hibernate but smaller (Level 3), and enterprise hardening (Level 4).

C# on SQLite is the **reference implementation**. The long-term plan is a language-neutral **spec** plus a **conformance suite**, with ports to other languages (Go, Java, PHP) and dialects (PostgreSQL, MySQL, SQL Server, Oracle) that pass the same conformance files. Early decisions are made so that later levels and ports don't require rewrites.

**Levels 0–3 are complete in C#, PHP, and Go** (Level 2 exit ADR-0031, PHP Level 2 ADR-0033, Level 3 ADR-0034 → ADR-0039), with the SQLite, SQL Server, and PostgreSQL dialects in all three (ADR-0024, ADR-0025, ADR-0040). **Level 4 items done in all three:** triggers in the mapping (ADR-0042), migration tooling beyond SQLite (ADR-0043), statement batching (ADR-0044), observability — the statement log (ADR-0045). **The programme now is uniformity across languages** (ADR-0032): the purpose is how the same spec is implemented in many language types; Fidelis is a consumer, not the target. "Current state" below says what is pending and what is deprioritized; the history is in `docs/decisions.md`. Do not start a milestone unprompted. If I casually ask for something listed under "Deferred," tell me which level it belongs to and why it's deferred before doing anything.

## Current state (2026-10-27)

One state, replacing the per-milestone session notes (ADR-0046); every fact has its history under the ADR named.

**What exists.**

- **Levels 0–3 in three implementations**, sharing no code, only `spec/` and `conformance/`: C# under `dotnet/` (the reference), PHP under `php/` (ADR-0026; Level 2 ADR-0033), Go under `go/` (ADR-0027; Level 2 ADR-0031). Level 3 in all three: identity map and change tracking (ADR-0035), unit of work with declared cascades (ADR-0036), detach/merge and the declared hooks (ADR-0037), draft migrations (ADR-0038); inheritance mapping is ruled out (ADR-0041; `MAP-028` built, ADR-0041 add.1); the exit was a spec-only Python port design whose 32 gaps became spec text (ADR-0039).
- **Three dialects in every implementation**: SQLite (the reference, ADR-0003), SQL Server (ADR-0024), PostgreSQL (ADR-0025); the ports' SQL Server and PostgreSQL by ADR-0040. Both live-verified in all three (ADR-0040 add.2, 2026-10-19, SQL Server 2016 on the owner's LAN); the four driver items of ADR-0040 add.1 are closed, the last by ruling — SQL Server's generated writes report their own row count (`ReportsAffectedRowsBySelect`, ADR-0040 add.3). Go drivers: `modernc.org/sqlite`, `github.com/microsoft/go-mssqldb` v1.11.2, `github.com/jackc/pgx/v5` v5.11.0; PHP: `pdo_sqlite`, `pdo_pgsql`, `pdo_sqlsrv` or `pdo_odbc` with ODBC Driver 17.
- **Level 4, done in all three:** (1) triggers in the mapping (ADR-0042); (2) migration tooling — `snapshot`, `shadow`, `diff`/`--amend`, derived rollbacks, `migrate --force` — on every dialect (ADR-0043); (3) statement batching, `Batching = Off | Auto` (ADR-0044); (4) the statement log, `DbOptions.Log` (ADR-0045). **Done in C# only, the PHP and Go ports next:** (5) resilience — `CommandTimeout`, `OpenRetries` + `OpenRetryDelay`, the seam member `IsTransient` (ADR-0047). §3 has each one's shape.
- **Last test counts** (ADR-0047; PostgreSQL 16.4 live, SQLite as always, SQL Server skip-gated): C# 581 passed / 22 skipped, both TFMs built; PHP 1102 tests / 33 skipped; Go every package ok, `gofmt` and `go vet` clean. Run: `cd dotnet && dotnet test SimpleOrm.sln`; `cd php && composer install && composer test` (`composer test-live` for the live legs); `cd go && go vet ./... && go test -p 2 ./...`.

**PENDING — the SQL Server live verifications**, waiting for the owner's SQL Server (off while the owner is offsite): ADR-0040 add.3 (the trigger test of each leg — generated writes judging their own row count under an `AFTER` trigger without `SET NOCOUNT ON`), ADR-0042 (the trigger live tests), ADR-0043 (the `migrations-cases/sqlserver/` cases, the shadow round trip, the force-sync plan, the drop-column batch), ADR-0044 (the batching legs: C# `LiveBatchingTests+OnSqlServer`, PHP `SqlServerLiveTest`'s batching scenarios, Go `sqlserver/live_batch_test.go`), ADR-0045's statement-log lines for SQL Server's multi-statement message, which ride along with those legs (ADR-0045 itself records nothing pending; the lines are pinned offline), and ADR-0047's `waitfor delay` command-timeout leg (`ResilienceTests`). With `SIMPLEORM_SQLSERVER` set to each implementation's admin connection string (the forms in ADR-0040 add.2; credentials are never in the repo), from the repository root:

```
cd dotnet && dotnet test SimpleOrm.sln --filter "FullyQualifiedName~SqlServer"
cd php && composer test-live -- --filter SqlServer
go test -C go -count=1 -p 2 ./orm/sqlserver/ -run Live
go test -C go -count=1 -p 2 ./orm/sqlserver/ ./orm/internal/conformance/ -run "Live|SqlServer"
```

(The last line is ADR-0043's addition and covers the one before it.) Each run must show the trigger tests passing, not skipped, and the rest of the leg green; a failure is a finding against that ADR, not against its ruling.

**Deprioritized, deferred, ruled out.**

- The new-language ports Rust → Python → TypeScript (ADR-0032): deprioritized (ADR-0040 add.1) — Rust only when the owner starts working in Rust.
- MySQL and Oracle: off the table (ADR-0040 add.1).
- Inheritance mapping: ruled out permanently (ADR-0041). Lazy proxies: never (§2).
- Batched reads (3b) and multi-row inserts for keys the database does not generate: deferred by the owner (ADR-0044).
- Observability beyond the log: the observer/event design (message indices, timing, tagged SQL comments, a slow threshold) was declined as too complicated (ADR-0045); tracing and timing stay candidates.

**Next:** the PHP and Go ports of ADR-0047 (resilience) by the port method; then the queued performance pass; the remaining Level 4 candidates (CI conformance matrix, packaging, tracing/timing — §3, §11) wait for the owner.

## 2. Principles that don't change at any level

- **The database is the source of truth.** The object model conforms to the schema; migrations are explicit files, never implicit side effects.
- **Real SQL is always first-class.** Whatever query front-ends exist later, hand-written SQL maps to the same entities and graphs through the same pipeline. Never an HQL-style second-class dialect.
- **No hidden queries.** Every database round trip is visible in user code. No lazy proxies (Level 2 decides whether an explicit, opt-in form ever exists).
- **No SQL built from user data by string concatenation.** Ever. Any such code path is a bug.
- **Errors name things.** Every exception carries a stable error code plus the query file, column, property, or parameter involved, and what was expected.
- **Async only.** Every I/O method is async and takes a `CancellationToken`. This is the spec-level contract that ports and future dialects implement, even though the SQLite provider is synchronous underneath (ADR-0003).
- **Strict by default.** Silent nulls, silently ignored columns, and silently ignored parameters are bugs, not conveniences.

## 3. Deferred — not before the stated level (this is "not yet," not "never")

| Feature | Level |
|---|---|
| Relationships (one-to-one, one-to-many, many-to-one, many-to-many, owned types), graph reshaping | 2 — done: all four cardinalities with composite-key FK lists (ADR-0005, ADR-0019 + add.1); nothing loads implicitly — default unloaded, eager on request, never on access (ADR-0021); reshaping from joined rows is the `Join` fetch mode (ADR-0022 add.1); owned types (ADR-0030); polymorphic and "through" ruled out permanently |
| Query AST + query front-ends (fluent builder, LINQ provider) | 2 — the AST and its dialect renderer done (ADR-0020); its criteria core is Level 1 (ADR-0012); the lambda front-end is per-language sugar outside the Level 2 exit (ADR-0029); LINQ stays out |
| Dynamic SQL composition (optional filters, sorting from UI) | covered at Level 1 by the ADR-0012 criteria core |
| Explicit/batch/eager loading | 2 — done (ADR-0021, ADR-0022) |
| Identity map, change tracking, unit of work, cascades, merge/detach | 3 — done (ADR-0035–0037) |
| Inheritance mapping, event hooks (audit, soft delete) | 3 — hooks done (ADR-0037); inheritance **ruled out permanently** (ADR-0041): concrete classes only, a base class shares members and is never an entity (`MAP-028` on a read through it, ADR-0041 add.1); variants via owned types or read models (README "Variants") |
| Draft migrations generated from the metadata diff | 3 — done (ADR-0038: `[RenamedFrom]` + `diff --draft`) |
| Additional dialects — SQL Server first, PostgreSQL second (ADR-0023); MySQL/Oracle off the table (ADR-0040 add.1) | 4 (seam exists from Level 1) — SQL Server (ADR-0024) and PostgreSQL (ADR-0025) done, pulled forward, in all three implementations (ADR-0040) and live-verified (ADR-0040 add.2); SQL Server's generated writes report their own row count (ADR-0040 add.3); the migration tooling works on every dialect (ADR-0043) |
| Read/write splitting and multi-database routing, incl. mixed engines (session-level, never per-entity — see decision log 2026-08-28) | 4 |
| Triggers in the mapping | 4 — done (ADR-0042): a trigger is its own declaration class with one neutral body (`{new: cols}`, `{old: cols}`, `{now}`), linked from the entity (`[Trigger(typeof(X))]`); the dialect renders the rest (`DDL-005` where an engine lacks the form); migrations own it as `Trigger/<Class>/`; `conformance/trigger-sql/` |
| Statement batching | 4 — done (ADR-0044): `Batching = Off \| Auto` (default `Off`) + `MaxStatementsPerMessage`; `Auto` sends what one call already holds (a flush, an explicit `db.Batch()`) in as few messages as the plan allows — no timer, nothing deferred; `conformance/batch-cases/`; **batched reads (3b)** and **multi-row inserts** (keys the database does not generate only) deferred |
| Observability — the statement log | 4 — done (ADR-0045): `DbOptions.Log`, one line `<name>: <sql> [@a, @b]` per statement as it is sent, `LogParameterValues` off by default, `DbLog.Console`, CLI `--log`; nothing about the outcome; the observer/event design was declined |
| Resilience — the command timeout and the connection-open retry | 4 — done in C# (ADR-0047; PHP and Go follow by the port method): `DbOptions.CommandTimeout` (unset = the driver's default; set, every command and batch the session creates carries it in whole seconds, rounded up; CLI `--command-timeout`), `OpenRetries` (default 0) + `OpenRetryDelay` (default 1 s, fixed) retrying only the open, only when the dialect's `IsTransient` says so — never a statement, no backoff, no error code, both failures are the driver's; the runner's history read no longer mistakes a failure for an empty history |
| Caching, analyzers, source generators, tracing/metrics | 4 |

## 4. Stack and targets

### Targeting policy

- **Core library:** multi-target `netstandard2.0;net10.0`. `netstandard2.0` is the compatibility floor (it is the only .NET Standard version worth targeting: 2.1 excludes .NET Framework and .NET Standard is frozen). `net10.0` (current LTS) is where modern APIs and performance work live.
- **Core depends only on `System.Data.Common`** (`DbConnection`, `DbCommand`, `DbDataReader`, `DbTransaction`). Zero provider packages. This is what makes the core usable anywhere and makes dialects pluggable.
- **Dialect packages** reference the provider and multi-target too. `SimpleOrm.Sqlite` references `Microsoft.Data.Sqlite`; it still ships a `netstandard2.0` target, so one unconditional `PackageReference` serves both TFMs (switch to conditional references if a future major drops it). The bundled native SQLite (SQLitePCLraw `e_sqlite3`) is ≥ 3.46, so `RETURNING` (3.35+) and STRICT tables (3.37+) are available. `SimpleOrm.SqlServer` references `Microsoft.Data.SqlClient` **conditionally per TFM** (ADR-0024: 5.1.6 on `netstandard2.0`, 6.x on `net10.0` — SqlClient dropped netstandard after 5.x). `SimpleOrm.Postgres` references `Npgsql` the same way (ADR-0025: 4.1.14 on `netstandard2.0`, 9.x on `net10.0`; the 4.1 leg has legacy-temporal caveats — prefer `net10.0` for Postgres).
- C# `LangVersion` latest on both targets. Nullable reference types enabled everywhere. `TreatWarningsAsErrors`.
- CI must build the `netstandard2.0` target; tests run on `net10.0`. A `net48` test leg (Windows) is a later addition; until it exists, .NET Framework runtime bugs are best-effort.

### netstandard2.0 rules (only the core and dialect packages; tests are net10.0)

- `PolySharp` (private, build-only) for language polyfills: `IsExternalInit` (records/`init`), `required`, nullable attributes, `CallerArgumentExpression`.
- `Microsoft.Bcl.AsyncInterfaces` for `IAsyncDisposable`, `IAsyncEnumerable<T>`, `ValueTask`.
- `System.Text.Json` package (works on netstandard2.0) for JSON columns.
- `DateOnly`/`TimeOnly` handlers are compiled only for `net10.0` (`#if NET`). On netstandard2.0, `date`/`time` map to `DateTime`/`TimeSpan` and the docs say so.
- `DbBatch` and other net6+ APIs are gated behind `#if NET`; the netstandard2.0 path must remain correct, just slower.
- Nullability introspection: do **not** rely on `NullabilityInfoContext` (net6+). Implement one small reader of the compiler's `NullableAttribute` / `NullableContextAttribute` metadata (well-documented algorithm, ~100 lines) and use it on both targets, so the validation rules behave identically everywhere.
- Column metadata: use `DbDataReader.GetColumnSchema()` (`IDbColumnSchemaGenerator`) where available, with `GetSchemaTable()` as the fallback; the dialect decides which to trust for nullability.

### Other dependencies

- Tests: xUnit against real SQLite database files — each fixture creates a temp-file database and deletes it afterwards; no server, no Docker, nothing to configure (ADR-0003). No mocked connections anywhere.
- Level 1 milestone 8 only: BenchmarkDotNet, with Dapper as a benchmark baseline.
- Ask before adding any dependency not listed here.

## 5. Repository layout (monorepo from day one)

```
spec/                         language-neutral spec, written as each level stabilizes
  metadata-model.md           EntityMap: what it contains, JSON export format
  mapping-rules.md            naming conventions, construction, conversions, strictness
  migrations.md               file format, version table, checksums, locking, up/down semantics
  validation-rules.md         every rule with its error code
  errors.md                   error code registry (MAP-, PRM-, MIG-, VAL-, CRUD-, QRY-, TX-, REL-, DDL-, UOW-)
  query-ast.md                Level 2
  dialects.md                 the seam member by member; the SQLite, SQL Server, PostgreSQL realizations; the live-testing contract (ADR-0040)
conformance/                  the executable definition of the library (see §9)
  fixtures/                   seed data
  entities/                   expected EntityMap JSON for the fixture entities
  cases/                      query/command/error cases as JSON
  migrations-cases/           runner scenarios as JSON (incl. derived downs, MIG-012 guard)
  diff-cases/                 generator diff scenarios as JSON (shape vs shape, ADR-0017)
  amend-cases/                diff --amend scenarios: a migrations tree as data in, files deleted/written out (ADR-0017 add.3)
  snapshot-cases/             exact snapshot exports for fixture entities (ADR-0017)
  load-cases/                 relationship loading replayed explicitly and in every fetch mode (ADR-0021/0022)
  tracking-cases/             identity map + change tracking step scripts per tracking mode (ADR-0034 M1)
  flush-cases/                unit-of-work step scripts: graph in, ordered statement sequence out (ADR-0034 M2)
  trigger-sql/                a neutral trigger declaration in, the exact create/drop statements per dialect out (ADR-0042)
dotnet/
  SimpleOrm.sln
  src/SimpleOrm/              core: metadata, mapping, parameters, session, rules, migration runner
  src/SimpleOrm.Sqlite/       SQLite dialect (Microsoft.Data.Sqlite)
  src/SimpleOrm.SqlServer/    SQL Server dialect (Microsoft.Data.SqlClient, ADR-0024)
  src/SimpleOrm.Postgres/     PostgreSQL dialect (Npgsql, ADR-0025)
  src/SimpleOrm.Cli/          migrate / status / validate / baseline / export-metadata
  samples/SimpleOrm.Sample/   sample entity models (User, Role, UserRole, Transaction,
                              TransactionDetail) — the fixture entities for tests and conformance
  tests/SimpleOrm.Tests/      integration tests + the conformance runner
docs/decisions.md             ADR-style log; append whenever a decision below changes
docs/guide/                   the developer guide — one page per topic, C# first with the PHP and Go shapes; snippets from the samples
```

```
php/                          the PHP port, Levels 0–3 (ADR-0026, Level 2 by ADR-0033, Level 3 by ADR-0035–0039): src/ mirrors the C# areas;
                              src/Dialect holds SqliteDialect, SqlServerDialect, PostgresDialect behind the Dialects registry (ADR-0040);
                              tests/ holds the fixture entities (tests/Sample), one conformance runner per folder, and the skip-gated
                              live legs (tests/Dialect/*LiveTest.php, Support/LiveServer);
                              CODING-STANDARD.md is its uniformity contract, §10 the only list of PHP divergences;
                              samples/SimpleOrm.Sample is the standalone consumer project mirroring dotnet/samples
                              (own composer.json via a path repository; models, V0001–V0009 migrations + snapshots,
                              repositories, registry, bin/demo.php) — driven by tests/Samples like the C# sample
go/                           the Go port, Levels 0–3 (ADR-0027, Level 2 by ADR-0031, Level 3 by ADR-0035–0039): one module; orm/ is the public package (session,
                              registry, SchemaGuard, aliases of the internal/core contracts), orm/sqlite the dialect
                              + shadow, orm/sqlserver (go-mssqldb) and orm/postgres (pgx) the Level 4 dialects (ADR-0040),
                              orm/migrations the migration model and tools, orm/cli the command as a
                              library, orm/internal/* the areas, orm/sample the fixture entities + migrations tree,
                              orm/internal/conformance one runner per folder; CODING-STANDARD.md §10 the only list
                              of Go divergences
```

Future ports live beside `dotnet/` (`rust/`) and consume `spec/` and `conformance/` unchanged.

## 6. Target public API (Level 1) — refine it, don't expand it

```csharp
await using var db = await Db.OpenAsync(dataSourceOrConnectionString, options, ct);

var orders = await db.QueryAsync(Queries.GetOrdersByCustomer, new GetOrdersByCustomerArgs(CustomerId: 42), ct);
var order  = await db.QuerySingleAsync(Queries.GetOrderById, new GetOrderByIdArgs(Id: 7), ct);            // 0 or >1 rows throws
var maybe  = await db.QuerySingleOrDefaultAsync(Queries.GetOrderById, new GetOrderByIdArgs(Id: 7), ct);
await foreach (var row in db.StreamAsync(Queries.AllOrders, EmptyArgs.Value, ct)) { ... }                 // IAsyncEnumerable<T>

await using var tx = await db.BeginAsync(ct);
var affected = await db.ExecuteAsync(Commands.MarkShipped, new MarkShippedArgs(Id: 7), ct);
await tx.CommitAsync(ct);

// CRUD by key
await db.InsertAsync(order, ct);               // generated key written back onto the entity (ADR-0014)
await db.UpdateAsync(order, ct);               // full row by key; optimistic concurrency if a version column is mapped
await db.UpdateOnlyAsync(order, [nameof(Order.Status)], ct);   // only the listed properties (ADR-0028); same key/version rules
await db.DeleteAsync<Order>(id, ct);           // by key (CRUD-001 if missing); pass the entity for the version-checked form

// Read by key (ADR-0006, implemented early by ADR-0012) — strict variant throws with a code; composite keys pass a tuple
var order = await db.GetAsync<Order>(7, ct);
var maybe = await db.GetOrDefaultAsync<Order>(7, ct);
var link  = await db.GetAsync<UserRole>((userId, roleId), ct);

// Criteria queries (ADR-0012): the AST as data; Where args are implicitly ANDed
var recent = await db.Query<Order>()
    .Where(Criteria.Or(Criteria.Eq("Status", "Pending"), Criteria.In("Id", ids)),
           Criteria.Ge("CreatedAtUtc", since))
    .OrderBy("CreatedAtUtc", SortOrder.Desc).Limit(20)
    .ToListAsync(ct);

// Rules — once at startup; throws with a complete report
await SchemaGuard.ValidateAsync(db, typeof(Queries).Assembly, ct);
```

Queries and commands are declared once in a registry that binds a SQL file to its argument and result types:

```csharp
public static class Queries
{
    public static readonly Query<GetOrdersByCustomerArgs, Order> GetOrdersByCustomer
        = Query.Inline("select ... from orders where customer_id = @CustomerId");
}
public sealed record GetOrdersByCustomerArgs(int CustomerId);
```

The registry is what the validator enumerates. (The Level 4 source-generator idea — producing the registry from `.sql` files — now applies only to teams using the optional `Query.Embedded` form, ADR-0009.)

## 7. Level 1 decisions — resolved; reopen only with an entry in docs/decisions.md

### Metadata (the load-bearing decision)

1. **`EntityMap` is the single source of truth about a type**: CLR type, relation source — table, view, materialized view, statement, or procedure (ADR-0008) — schema-qualified where named, key columns and key strategy, version column, declared indexes (ADR-0007; tables and materialized views only), and one entry per mapped property: property name, column name, CLR type, provider type name, nullability, generated flag, custom handler. Every other subsystem — mapping, CRUD generation, validation, migrations (Level 3), the query model (Level 2) — reads `EntityMap` and nothing else. No subsystem reads attributes directly.
2. **Loaders produce `EntityMap`.** Three, with precedence explicit → attribute → convention:
   - Attributes: `[Table]`, `[Column]` (with the optional storage facets `Length` / `Precision` + `Scale`, ADR-0043 — dialect-neutral sizes each dialect renders its own way, `MAP-032` when misplaced), `[Key]`, `[Generated]`, `[Version]`, `[Ignore]`, `[EnumAsInt]`, `[Owned]` (class + navigation, ADR-0030 — a value object flattened into the owner's columns under a prefix; the `EntityMap` stays flat, property paths are dotted); declaration-only relationship metadata `[ForeignKey]`, `[ManyToOne]` (ADR-0005), `[OneToOne]`, `[OneToMany]`, `[ManyToMany]` (ADR-0019 + add.1, L2 M1; FK lists for composite keys); declaration-only DDL metadata `[Index]` (class-level, repeatable, ADR-0007 — consumed by generated DDL, snapshots, and the diff generator, ADR-0011/0017); relation sources `[View]`, `[MaterializedView]` (separate from `[View]` because it may carry `[Index]`), `[Statement]`, `[Procedure]` (ADR-0008 + addenda; every non-table source carries its defining SQL in the attribute — statements and procedures also their (name, typeof) parameter pairs; materialized views and procedures don't exist on SQLite — dormant until Level 4) — a class carries exactly one of `[Table]`/`[View]`/`[MaterializedView]`/`[Statement]`/`[Procedure]`
   - Manual: a fluent `EntityMapBuilder<T>` for types you can't or won't annotate
   - Conventions: `snake_case` ↔ `PascalCase` by default; pluggable `INamingConvention`
3. **`EntityMap` exports to JSON** (`export-metadata` CLI command and an API), in the format defined in `spec/metadata-model.md`. This export is a conformance artifact: every port must produce identical JSON from its own annotations.
4. **Entity identity** is defined here: key extraction from an instance, key equality, and composite key support. Level 2 graph reshaping and Level 3 identity map both build on this.

### Mapping

5. SQL is declared **inline** in the registry via `Query.Inline(...)`, next to its args and result types (ADR-0009 — same philosophy as `[Statement]`'s inline SQL, ADR-0008 addendum 2). `Query.Embedded("path.sql")` (embedded resources under `Sql/`) remains supported for teams that prefer SQL files; the validator checks both forms identically. The samples use inline only.
6. Attribute mapping is **opt-in** (ADR-0004): a property is mapped iff it carries `[Column]`. Bare `[Column]` derives the column name from the property name via the naming convention; `[Column("name")]` binds explicitly; a public settable property carrying none of `[Column]`, `[Ignore]`, or a relationship attribute is a loader error. A `[ManyToOne]` navigation property is transient — never a column, never written, never populated by the library at Level 1 (ADR-0005) — and must not expose a public setter: the library is its only writer, so it can never disagree with its FK property; a public setter is a loader error (ADR-0005 addendum 2; same rule for `[OneToMany]` when Level 2 adds it). Types with no mapping attributes go through the convention loader, which maps every public property by convention. SQL aliasing preferred over attributes for per-query mismatches.
7. Strict: a result column with no property, or a required property with no column, throws (`MAP-001`, `MAP-002`). Never a silent null or default.
8. Construction: constructor with matching parameter names first (records), then settable properties. Ambiguity is an error (`MAP-003`).
9. Type conversion is a fixed table plus a registry:
   - Fixed: `int`, `long`, `short`, `decimal`, `double`, `float`, `bool`, `string`, `Guid`, `byte[]`, nullable variants; enums as `TEXT` matched by name case-insensitively (`[EnumAsInt]` opt-in); `DateOnly`/`TimeOnly` on `net10.0` only (stored as ISO-8601 `TEXT`)
   - Dates: SQLite has no date/time types; the convention is ISO-8601 UTC `TEXT` with a trailing `Z` ↔ `DateTime` with `Kind = Utc` only; a stored value without UTC marking is a lint error (`VAL-020`, exact rule finalized in milestone 4); `DateTimeOffset` allowed
   - JSON columns (`TEXT` holding JSON) ↔ any type via System.Text.Json through the `ITypeHandler<T>` registry
   - Anything else: an `ITypeHandler<T>` registered on `DbOptions`. No reflection-based guessing.
10. Nested results at Level 1 are produced by the database with `json_group_array(json_object(...))` in a subquery and deserialized by the JSON handler. The SQL pattern is documented in the README. (Level 2 adds graph reshaping from joined rows; the JSON path stays supported.)
11. Raw SQL results and (future) generated queries both go through one row-mapping pipeline built on `DbDataReader`. Mapper delegates are built per (query, result type) with expression trees and cached; reflection only at build time.

### Parameters

12. `@name` placeholders bound from the public properties of the args record. SQLite has no array parameters, so `IN` lists are expanded safely at Level 1: a collection-typed property expands `IN (@ids)` to generated placeholders (`@ids_0..@ids_N`), always parameterized, never concatenated. The dialect declares `SupportsArrayParameters` — realized by ADR-0025: on Postgres the collection binds as **one typed-array parameter** with the SQL untouched (`WHERE id = ANY(@ids)`, written dialect-natively in the registry); an empty collection binds a typed empty array, so empty still matches no rows on every dialect.
13. A parameter in the SQL with no property (`PRM-001`), or a property never used by the SQL (`PRM-002`), is an error.

### Writes

14. CRUD is generated from `EntityMap`, never from attributes directly. Explicit column lists always. Generated keys via the dialect (`RETURNING`; SQLite supports it since 3.35). Key strategies: database-generated (`INTEGER PRIMARY KEY`), client-generated GUID, natural/composite. (No sequence strategy at Level 1 — SQLite has no sequences; it returns with a dialect that has them.) The FK property is what is written; if a `[ManyToOne]` navigation is non-null and its object's key disagrees with the FK property, `Insert`/`Update` throw a consistency error instead of writing (ADR-0005 addendum). Read-by-key is generated the same way: `GetAsync<T>` (missing row throws) / `GetOrDefaultAsync<T>` (null); composite keys pass a tuple validated at runtime against the `EntityMap` key — arity, order, and types, each mismatch a named error (ADR-0006). CRUD writes exist only for table-backed entities; view-, materialized-view-, statement-, and procedure-backed entities are read-only and refuse writes with a named error (ADR-0008). `InsertAsync` and metadata-generated DDL (`CreateTableAsync`/`CreateViewAsync`, IF NOT EXISTS dev/test utility — versioned migrations stay the schema-evolution path) were pulled forward by ADR-0011, `GetAsync` by ADR-0012; Update/Delete + concurrency completed in milestone 7 (ADR-0014: insert returns `Task`, the key lands on the entity; `DeleteAsync` takes a key for the plain form or the entity for the version-checked form).
15. `Update` writes every mapped non-key column by key. **Update by column list** (ADR-0028): `UpdateOnlyAsync(entity, [property names], ct)` — a *separately named* operation (PHP `updateOnly`, Go `orm.UpdateOnly`; never an overload, the ports have no overloading) writes only the listed properties with the same key/version rules; unmapped name `CRUD-005`, key/version/generated `CRUD-006`, empty or repeated `CRUD-007`. Concurrency stays row-level. Dirty tracking stays Level 3.
16. Optimistic concurrency when a version column is mapped: `... SET version = version + 1 WHERE <key> = @key AND version = @version`; zero rows affected throws `ConcurrencyException` (`CRUD-010`). Same on `Delete`.

### Session

17. `Db` owns one `DbConnection` (obtained from the dialect) and is `IAsyncDisposable`. `BeginAsync` returns a transaction scope; no ambient or static transactions. Every command runs on the session's connection and current transaction, if any. Savepoints are Level 4. Resilience (ADR-0047): every command the session creates goes through one funnel that applies `DbOptions.CommandTimeout` when set, and `OpenAsync` retries only the connection open, `OpenRetries` times with a fixed `OpenRetryDelay`, when the dialect calls the failure transient.

### Rules (SchemaGuard)

18. Runs at startup via `ValidateAsync` and in a test that calls the same code. Build-time checking is Level 4.
19. For every registered query and command, obtain the statement description **without executing it** — prepare the statement and read the column schema (`CommandBehavior.SchemaOnly` + `GetColumnSchema()`; the dialect implements this) — and check:
    - SQL parses when prepared (`VAL-001`)
    - parameters match the args type both ways (`PRM-001`, `PRM-002`)
    - result columns match the result type exactly (`MAP-001`, `MAP-002`)
    - a nullable column maps to a nullable property (`VAL-010`); SQLite reports nullability and declared types only for table-backed columns, so expression columns require the property to be nullable unless the SQL contains `-- notnull: <col>`
    - column declared types are compatible with the fixed table or a registered handler (`VAL-011`); because SQLite only enforces declared types on STRICT tables, **all fixture and migration tables are STRICT** (3.37+) — this is what keeps "strict by default" real on SQLite
    - lint: no `SELECT *` (`VAL-021`); no non-UTC date storage (`VAL-020`)
    - no pending migrations (`MIG-030`)
20. Collect every violation and throw one `SchemaValidationException` with a complete, file-by-file report. Never stop at the first error.
21. Fail fast in all environments. No warn-only mode at Level 1.

### Migrations

22. Migrations are **code, per-object** (ADR-0013; owner: never external .sql files). Root versions `V0001.cs` directly under `Migrations/` compose per-object steps (`Migrations/Table/User/V0002_AddDisplayName.cs`, `View/...`) in explicit order; the root is the recorded unit. Table actions execute **rename → add → remove → raw SQL** regardless of declaration order; every action takes optional per-action `Pre`/`Post` data hooks (inline SQL). Column specs are literal (frozen); metadata-rendered DDL is legal only for an object's initial create. `SqlVersion` is the data-driven form (conformance, future generators). Malformed names `MIG-001`; duplicate versions/objects `MIG-002`; step/root version mismatch `MIG-003`; un-composed steps `MIG-004`. **Down DDL is never written — the runner derives it** (ADR-0018, superseding authoring-time generation): at `migrate down` the rollback deduces from the versioned snapshots (`V000N.schema.json` — format v2 per ADR-0017: dialect **storage types** spelled as the dialect's introspection reports them, name-sorted columns/indexes, `key`/`generated` flags, version + generation time; **format v3 per ADR-0043: a `dialect` stamp on every non-SQLite document, `MIG-014` when another dialect reads it**; view snapshots are dialect-neutral — `create [materialized] view <name> as <body>`, prefix and quoting stripped; written by `simpleorm snapshot` from metadata or `simpleorm shadow` from replayed history — tables introspected, views and triggers from the replayed step — which must agree byte-for-byte modulo `generatedAt`; **embedded as assembly resources** so rollbacks work deployed). Every derived statement renders through the seam; the indexes a version added drop before the columns they cover. The step's typed `RenameColumn` actions invert data-preservingly (snapshots can't tell a rename from drop+add); the rest is the snapshot diff; a NOT NULL restore lands nullable with a notice; a type/nullability change is not derivable. `Down()` is the manual override and always wins; step-level `PreDown`/`PostDown` hooks carry the data work (a data-only step derives an empty rollback). No snapshot and no override refuses `migrate down` (`MIG-020`); a failing statement reports its SQL (`MIG-021`).
23. Runner: table `schema_version(version INTEGER, object TEXT, description TEXT, checksum TEXT, applied_at TEXT, execution_ms INTEGER, primary key (version, object)) STRICT` (`applied_at` ISO-8601 UTC). Checksum = SHA-256 of the step's rendered Up SQL; drift on an applied step is `MIG-010`, history unknown to code is `MIG-011`; the whole plan validates before anything executes. The dialect provides the **run lock** — on SQLite the entire run is one `BEGIN IMMEDIATE` transaction (exclusive + atomic: a failed run applies nothing); on SQL Server one transaction holding `sp_getapplock` (ADR-0024); on Postgres one transaction holding `pg_advisory_xact_lock` with a 60s `lock_timeout` (ADR-0025 — DDL is transactional there too, so the anticipated per-migration transactions proved unnecessary). `baseline` records versions without running them. Multi-database apps use one migrations namespace (folder) per database. The diff **generator** exists (ADR-0017): `simpleorm diff` (metadata vs latest snapshot, no database; tables diff by columns, views/MVs by normalized DDL; emits literal-SQL steps with generated Downs; renames declared via `--rename`, never inferred; removals gate on `--allow-remove` `DDL-003`; type/nullability changes are `DDL-004`, hand-written; **`--amend`** regenerates the newest version in place against the snapshot history below it — hand-written versions only with `--force` (raw SQL/hooks/data aren't computable from a diff; the run names them to re-add) — ADR-0017 add.3: git is human protocol, the databases' `MIG-010` checksums are the boundary; in both modes an object with migration history but no snapshot refuses rather than being created anew), `simpleorm shadow` (rebuild snapshots by replay on every dialect — the throwaway database comes from the dialect's shadow host, a temp file on SQLite, `simpleorm_shadow_<hex>` on the admin connection `--db` names elsewhere; `--from V000N [--to V000M]` trusts N — baseline from committed snapshots rendered through the dialect, no verification below N, regenerate only the slice), and `migrate --force [--allow-delete]` (post-migration live-schema sync: additive applies, deletions gated `DDL-003`, inexpressible reported `DDL-004`). Indexes match **structurally** (unique + ordered columns/direction), never by name (ADR-0017 add.2) — one existing under another name counts as implemented, since indexes get added directly to the database in urgencies. Snapshots cover every kind: tables by columns, view/MV/procedure by DDL (`{object, kind, asOfVersion, generatedAt, ddl}`, ADR-0017 add.1; capability-gated like their migrations). A view step's `ExpectDefinition` guard (generated steps always carry it) refuses to apply over a definition changed outside the code (`MIG-012`) — views get hotfixed in urgencies; only `--force` recreates over the drift, with notice; the comparison is normalized text where the engine stores the definition verbatim (SQLite, SQL Server) and a probe-view round trip where it reformats it (PostgreSQL, `ViewDefinitionIsVerbatim` false — ADR-0043). There is never automatic sync against shared databases.
24. The application never applies migrations at startup; it only checks (rule `MIG-030`). The CLI (`--assembly`, `--db`, `--namespace`) applies: `migrate [--force [--allow-delete]]`, `migrate down --to [--snapshots <dir>] [--force]`, `status`, `baseline`, `export-metadata`, `validate`, `snapshot --out`, `diff --out --namespace [--name] [--rename]* [--allow-remove] [--draft] [--amend [--force] [--db]]`, `shadow --out [--from] [--to]` (every dialect; on sqlserver/postgres `--db` is the admin connection).

### Dialect seam (`IDialect`, minimal and capability-based)

25. Members at Level 1, and no more: create connection; quote identifier; parameter prefix; render `RETURNING`/generated-key retrieval; render limit/offset; render CREATE TABLE/INDEX/VIEW and the generated INSERT from `EntityMap` (ADR-0011, ADR-0008 add.3); capability flags — array parameters, transactional DDL, materialized views, procedures; migration run-lock acquire/release; describe-statement implementation; declared-type → CLR type compatibility table; storage type per mapped property (ADR-0017 — what CREATE TABLE emits, used by snapshots/diff/sync); view-definition query (ADR-0017 add.1 — backs the `MIG-012` guard and shadow view snapshots). Add members only when a second dialect needs them — SQL Server (ADR-0024) added: identifier quoting realized (`QuoteIdentifier`, identity on SQLite), `PagingRequiresOrderBy`, `SupportsRowValueIn` (false → the renderer's EXISTS rewrite), `VersionTableSql`, and the migration-action renderings (`RenameTableSql`, `RenameColumnSql`, `AddColumnSql`, `DropColumnSql`, `DropTableSql`, `DropIndexSql`) — SQLite's implementations return the pre-ADR strings byte-for-byte, so checksums and conformance pins are untouched. Postgres (ADR-0025) added `BindsTemporalsNatively` (temporals bind as UTC CLR values, not ISO strings — `timestamptz >= text` refuses; SQLite/SQL Server false). The SQL Server trigger ruling (ADR-0040 add.3) added `ReportsAffectedRowsBySelect` (true only on SQL Server: the generated UPDATE/DELETE renderings end in `select @@rowcount` and the session judges that scalar; registry commands keep the driver's count; `conformance/crud-sql/` pins every generated write per dialect). Triggers in the mapping (ADR-0042) added `CreateTriggerSql(relation, onView, trigger)` (the create statements from the neutral declaration; `DDL-005` for a form the dialect lacks), `DropTriggerSql(relation, name)`, and `TriggerDefinitionSql` (`@relation` = the trigger name, one `definition` row per statement the dialect created, each equal to the matching create statement once normalized — PostgreSQL reconstructs both from the catalog); `conformance/trigger-sql/` pins every rendering. Migration tooling beyond SQLite (ADR-0043) added `Name`, `StorageType(map, property)` (replacing the property-only form: the indexable length for key *and* indexed columns, the facets, introspection's spelling — SQL Server `decimal(38,9)`), `CreateTableSql(schema)` and `CreateIndexSql(table, index)` from a snapshot, `CreateViewSql(view, body, materialized)` and `DropViewSql(view, materialized)`, `ViewDefinitionIsVerbatim`, a fifth `generated` column in `ColumnsInfoSql`, and SQL Server's `DropColumnSql` as the default-constraint-dropping batch; beside the seam, the tooling-only `IShadowHost` (provision/drop the throwaway database, optional trigger read-back — none on SQL Server). Statement batching (ADR-0044) added `SupportsStatementBatching` (the dialect's driver carries several statements in one message with a result per statement — SQL Server and PostgreSQL on `net10.0`) and `MaxParametersPerMessage` (SQL Server 2098, the plan's rule 3). Resilience (ADR-0047) added `IsTransient(exception)` (asked only about a failed connection open: SQLite never; SQL Server the error numbers of `SqlServerDialect.TransientErrorNumbers`; PostgreSQL SQLSTATE class 08, 53300, 57P03; both also a socket/IO/timeout cause). Do not speculate about Oracle.

## 7b. Level 2 milestones (ADR-0019) — one at a time; stop and report; do not start the next unprompted

1. **Relationship metadata.** *(Done, incl. add.1.)* All four classic cardinalities declared: `[OneToOne]`/`[OneToMany]` (FK on the target, by property), `[ManyToMany(typeof(Link))]` (link declared, never inferred; resolved via its `[ForeignKey]`s), beside Level 1's `[ManyToOne]`. **FK declarations are lists** — one entry per key part, in key order (composite keys, ADR-0019 add.1) — counts validated against declared key arity. Navigations: no public setter (`MAP-011`), wrong shape `MAP-020`, target FK issues `MAP-021`, link issues `MAP-022`. Declaration-only; **nothing loads implicitly — default is unloaded, eager loads with the query when requested, never on access**. Polymorphic and "through" relations are ruled out permanently. Owned types deferred until loading exists.
2. **Query AST + renderer + null semantics.** *(Done, ADR-0020 + add.1.)* `SelectAst` as data; the dialect renders (`IDialect.SelectSql`, `AnsiSelectRenderer` as the reference rendering); strict null semantics (`Eq/Ne(null)` → `IS [NOT] NULL`, other null comparisons and null-in-IN are `QRY-007`); empty composites render identity truth-values; negative paging is `QRY-008`; property-aware binding (`[EnumAsInt]` applies to criteria); ambiguous property names refuse; operator tokens pinned. `spec/query-ast.md` + `conformance/ast/` (15 cases); Level 1 spec debt closed (`spec/session.md`, `spec/crud.md`). Fixed in passing: IN-expansion inside literals/comments, `SupportsArrayParameters` realized, `UpdateSql` generated-column exclusion, integer-only generated keys.
3. **Explicit + batch loading.** *(Done, ADR-0021 + add.1/2.)* `LoadAsync(entity, nameof(T.Nav), ct)` + `LoadEachAsync(entities, …)` — one criteria query per navigation per call (many-to-many: two — link then targets), chunked at 500 owners, ordered by target key **value-wise**, composite keys via OR-of-ANDed tuples, structural §7.4 identity (never string tokens); owners sharing a many-to-one target share the instance. Nothing loads implicitly; **unloaded collection access on a database-read entity throws `REL-004`** (owner ruling; guard replaced by loading; user-constructed entities keep their initializers; singular navigations stay null — no proxies — and a **dead link loads as null**). `REL-001` unknown navigation, `REL-002` one-to-one matched >1 row, `REL-003` load-time shape disagreement. `spec/loading.md` + `conformance/load-cases/`.
4. **Eager loading.** *(Done, ADR-0022 + add.1 — configurable per owner ruling.)* `Include(navigations…).Fetch(FetchMode)`: **MultiQuery** (default; root + one batched query per navigation), **SubSelect** (`IN (select …)` over the root query — pages correctly, never chunks), **Join** (one SELECT with LEFT JOINs; `SelectAst` gained joins/projection, rows partition into segment readers reusing the one mapping pipeline §7.11; refuses paging `REL-005` and >1 collection `REL-006` — never in-memory paging or silent Cartesian products). All modes load identical graphs — pinned by `"viaQuery"` load-cases replaying every mode. JSON nesting stays the single-round-trip option for arbitrary shapes.
5. **Fluent front-end.** *(Moved out of Level 2 — ADR-0029.)* Typed lambda → AST stays approved as per-language sugar (ADR-0021 add.2), built on demand on an owner sample; not an exit criterion. LINQ provider stays out.
5b. **Owned types.** *(Done — ADR-0030, all three implementations.)* `[Owned]` on a class (never an entity) + `[Owned]` on the navigation; members flatten into the owner's columns under `<navigation>_` (overridable), the `EntityMap` stays **flat** (dotted property paths `Address.City`), only the mapper regroups; a nullable navigation forces member nullability and an all-NULL row reads as null; `MAP-024` for invalid declarations; no nesting, no collections, attribute loader only. PHP `#[Owned]`, Go `orm.OwnedType` marker + `orm:"owned"`. Fixture `UserProfile.Address` (sample V0010).
6. **Level 2 exit.** *(Done — ADR-0031.)* The ADR-0029 list closed: join/projection vocabulary + `conformance/ast/level2/`; export FK references by column, `MAP-017`/`MAP-011` rewordings; owned types (ADR-0030); spec sweep; benchmark refresh (ADR-0029 add.1). **Proof: the Go port extended to Level 2 with the agents forbidden to read `dotnet/` or `php/`** — 21 spec silences surfaced and answered in `spec/query-ast.md`/`spec/loading.md`, no rule changed; two reference behaviours turned into `QRY-006`. Go `orm.Load`/`LoadEach`, `Include(...).Fetch(orm.FetchMultiQuery|FetchSubSelect|FetchJoin)`; `REL-004` unreachable there — nil vs empty slice is the guard.

## 7c. Level 3 milestones (ADR-0034) — one at a time; stop and report; do not start the next unprompted

Each milestone: the C# reference first with its spec section and conformance folder, then PHP and Go by the ADR-0031 method (agents forbidden to read another implementation; gap answers are written into the spec sections they amend, ADR-0046), then a uniformity review. Level 3 opens the `UOW-` error prefix.

1. **Identity map + snapshot change tracking.** *(Done — ADR-0035, all three implementations.)* `DbOptions.Tracking = TrackingMode.Tracked` (default **untracked**: Level 0–2 untouched); one instance per (type, key) per session on the §7.4 identity, consulted by every entity-materializing read path (link rows and join segments included); snapshots in **database-bound form**; the tracked instance wins on a re-read, `RefreshAsync` is the explicit overwrite; `IsTracked`/`ChangesOf`/`IsDirty` (`UOW-001`); `Update` on a tracked instance writes only the dirty columns via the ADR-0028 path and nothing when clean, `UpdateOnly` moves the snapshot for the written columns only, `Delete` forgets the key; every write and the refresh pass the identity gate (`UOW-002`); a full-row `Update` checks `CRUD-004` first. `spec/session.md` "Tracking", `conformance/tracking-cases/`. Go: the pointer is the tracked shape. PHP: `Session\DbTracking`.
2. **Unit of work.** *(Done — ADR-0036, all three implementations.)* Tracked sessions only (`UOW-003`): `Persist`/`Remove` schedule (planned, then committed — a refusal schedules nothing), `FlushAsync` writes inserts in dependency order (navigations and cascades as instance edges; types by relationship metadata over the reachable closure, cycles sharing a rank; then schedule order) with generated keys flowing into dependents, then every dirty tracked instance, then deletes children-first — in the session transaction or its own — and returns a `FlushSummary`; a failed flush undoes its in-memory effects and keeps the schedule; `UOW-004` cycle, `UOW-005` remove cascading over a never-loaded collection, `UOW-006` conflicting schedule. Cascades **declared** (`[Cascade(Persist|Remove)]` on many-to-one/one-to-one/one-to-many; `MAP-025` elsewhere; exported only when declared; fixture `Transaction.Details`). Every statement is the existing generated CRUD; batching is Level 4's `Batching = Auto` (ADR-0044): the same statements in fewer messages. `spec/unit-of-work.md`, `conformance/flush-cases/`.
3. **Detach/merge + hooks.** *(Done — ADR-0037, all three implementations.)* `Detach` forgets an instance and cancels its scheduled work (`UOW-001` otherwise); `MergeAsync` copies a detached copy's values (version included) onto the session's instance for its key — the tracked one or the row read now — and returns it; `CRUD-001`/`CRUD-002`; no deep graph merge. Hooks as **declared metadata, never callbacks**, exported only when declared, `MAP-026` for every misplaced one, valid on tables only: `[CreatedAt]` fills with `DbOptions.Clock` (UTC, `VAL-020` otherwise) on insert when unset; `[UpdatedAt]` stamps every write that changes a row (joins column-list and dirty SETs, never a clean update); `[SoftDelete]` makes every delete the flag-setting update (entity form version-checked, by-key `CRUD-012` on a versioned entity) — reads never filter on it. A failed write leaves the instance as it was. Fixture: `BaseModel` audit hooks on every table, `Role.IsDeleted` via migration V0011; runners fix the clock at 2026-08-29T00:00:00Z. `spec/hooks.md`, `spec/unit-of-work.md` "Detach and merge", `conformance/hook-cases/`, `merge-cases/`.
4. **Inheritance mapping** — *(Skipped — ADR-0038; ruled out permanently — ADR-0041.)* Single-table, table-per-type, and polymorphic table-per-concrete are out: a navigation to a hierarchy root is the polymorphic association ADR-0019 forbids, two target languages have no subclassing, and owned variants or read models cover the need. What stays is what exists: a base class shares members (columns, key, hooks), identity comes only from the concrete class, and a read through a base class throws `MAP-028` (done — ADR-0041 add.1: the loader rule, the no-inheritance attribute reads, the scan rule in SchemaGuard and the CLI, in C# and PHP; Go refuses the one method-promotion analog and records the rest as unreachable).
5. **Draft migrations.** *(Done — ADR-0038, all three implementations.)* `[RenamedFrom("old")]` on a `[Column]` property is a declared rename the generator reads like `--rename` but kept with the model — permanent and harmless once applied (declares nothing when the snapshot has the new column, has neither, or the model still maps the old one); exported as `"renamedFrom"`; `MAP-027` wherever misplaced (incl. duplicates, owned members, non-table sources). `diff --draft` stamps `[Draft]` on the root; `migrate`/`baseline` refuse a pending draft (`MIG-013`, the whole plan, before anything executes), `status` reports `Draft`, a pending draft is pending for `MIG-030`, `--amend` keeps the marker, `shadow` replays it (`MigrationRunner.ReplayDrafts`, tooling-only). Fixture: `Role.Name` declares `name` (V0004's rename). `spec/migrations.md` "Draft migrations"; `diff-cases/renamed_from_*`, `migrations-cases/draft_refuses_to_apply`.
6. **Level 3 exit.** *(Done — ADR-0039.)* A spec-only audit (a Python port design written from `spec/` + `conformance/` alone) surfaced 32 gaps, all answered in the spec — among them a real divergence (type rank: discovery order in C#, depth in both ports — depth is the rule) and the step-script form, the fixture names, and the recorder contract now stated normatively in `spec/session.md`; five conformance additions; the tracked benchmark (`TrackingBenchmarks`: warm read 1.09×, dirty update at parity, clean update no statement, the sweep ~100 µs per 1000 instances) with the untracked path unchanged; **the reference does not batch** (`Microsoft.Data.Sqlite` cannot; Level 4 for dialects that can — done by ADR-0044).

Not in Level 3: lazy proxies (permanent), caching/observability, savepoints, read/write splitting (Level 4).

## 8. Level 1 milestones — one at a time; stop and report; do not start the next unprompted

1. **Skeleton.** Monorepo layout, multi-targeted core + SQLite dialect, CI build of both targets, temp-file SQLite test fixture, `docs/decisions.md`, empty `spec/` and `conformance/` with READMEs. Done when `dotnet build` succeeds for both TFMs and one trivial integration test passes. *(Done — originally built against PostgreSQL, reworked to SQLite in ADR-0003.)*
2. **Metadata model.** `EntityMap`, the three loaders, precedence rules, entity identity, JSON export, `spec/metadata-model.md`, and the first `conformance/entities/*.json`.
3. **Session + Query/Execute + parameters.** `Db.OpenAsync`, `QueryAsync` family, `StreamAsync`, `ExecuteAsync`, parameter binding including IN-list expansion, transactions. Tests for each against real SQLite databases.
4. **Strict mapping + types.** Constructor mapping, conversion table, handler registry, JSON nesting handler, every error case tested with its error code. `spec/mapping-rules.md`, `spec/errors.md`, first `conformance/cases/*.json`, and the conformance runner test.
5. **Migrations + CLI.** Decisions 22–24. Tests for checksum drift, locking, baseline, down without a down file, failure mid-run. `spec/migrations.md`, `conformance/migrations-cases/`.
6. **SchemaGuard.** Decisions 18–21. Every rule has a failing fixture and the report names the file and reason. `spec/validation-rules.md`.
7. **CRUD + concurrency.** Decisions 14–16, key strategies, conformance cases.
8. **Performance pass.** Compiled mappers verified, `#if NET` fast paths (e.g. `DbBatch`), BenchmarkDotNet against Dapper and raw `Microsoft.Data.Sqlite` reader code. Target: within 10% of Dapper on `net10.0`.

**Level 1 exit criteria:** all eight milestones done; `spec/` covers everything Level 1 does; the conformance suite passes; a second developer could reimplement Level 1 in another language from `spec/` + `conformance/` alone.

## 9. Conformance suite — the mechanism that makes ports possible

The suite is the executable definition of the library. Every implementation runs the same files. Formats are JSON so that any language can load them.

- `entities/`: for each fixture entity, the expected `EntityMap` JSON. Each implementation defines the entity natively (C# attributes, Go struct tags, Java annotations, PHP attributes) and must export identical metadata.
- `cases/`: `{ "name", "query": "path or inline SQL", "params": {...}, "expect": { "rows": [...] } | { "error": "MAP-001" } }`. Expected values use a small documented JSON encoding for dates, decimals, GUIDs, and byte arrays.
- `migrations-cases/`: a folder of migration files plus `{ "preState", "command", "expectStatus" | "error" }` — extended by ADR-0017/18 with snapshots, typed renames, the view guard, raw-SQL hotfix steps, `force`, and deep column/DDL expects.
- `diff-cases/`: generator scenarios — current shape + latest snapshot + declared renames in, the exact change out (no database). `snapshot-cases/`: a fixture entity + version + pinned time in, the exact exported snapshot document out. `amend-cases/` (ADR-0017 add.3): a migrations tree as data (files with/without the generator's header, snapshots at versions) + the `diff` command in, the exit code and the files deleted/written/kept out — against a fixed fixture each implementation defines natively.
- Level 2 adds `ast/`: a query AST as JSON with the expected SQL per dialect.
- Error codes are the cross-language contract for failures; messages may differ per language, codes may not.
- Rule: every milestone adds conformance files. A feature without a conformance case is not done.

## 10. Architecture reservations — decided now so Levels 2–3 don't require a rewrite

1. Explicit `EntityMap`, populated only by loaders; nothing else reads attributes.
2. `IDialect` minimal and capability-flag based.
3. Entity identity defined at Level 1.
4. No string-based query builder at Level 1. The query model at Level 2 is an **AST rendered by the dialect**; every front-end (fluent, LINQ) produces the AST and never emits SQL text itself.
5. Raw SQL and generated queries share one `DbDataReader` mapping pipeline.
6. Async and cancellation everywhere from the first commit.
7. Conformance files and spec documents grow with every milestone, not at the end.

## 11. Level 2–4 outline (for orientation only; do not implement)

- **Level 2:** relationship metadata in `EntityMap`; explicit → batch → eager loading; graph reshaping from joined rows with per-result identity; the query AST and its dialect renderer; explicit null-semantics rules; a fluent front-end first, LINQ later; dynamic composition through the AST.
- **Level 3:** session-wide identity map; snapshot-based change tracking; unit of work with FK-ordered flush, batching, cascades; simple detach/merge rules; event hooks; inheritance mapping (ruled out, ADR-0041); draft migrations from the metadata diff with `[RenamedFrom]` declarations.
- **Level 4:** additional dialects with the full conformance matrix in CI; statement batching (done — ADR-0044: configurable `Off | Auto`, one call's statements in fewer messages, no timer; batched reads deferred); triggers in the mapping (done — ADR-0042: a declaration class with one neutral body, a migration object kind, declaration-shaped snapshots, the hand-change guard; the write strategy stays ADR-0040 add.3's); source-generated registry and mappers; statement caching; observability — the statement log (done — ADR-0045: one line per statement as it is sent, forwardable, values off by default); tagged SQL comments, tracing, and timing stay candidates; resilience; N+1 and slow-query diagnostics; analyzers; API stability and semantic versioning.

## 12. Multi-language plan

- Ports share **no code**; they share `spec/` and `conformance/`. C# is the reference; a port is correct when it passes the same conformance files unchanged.
- Do not port before Level 1 exit criteria are met. The first port covers Levels 0–1 only.
- **Port order (ADR-0023, extended by ADR-0032): PHP first** (done to Level 2 — ADR-0026/0033), **Go** (done to Level 2 — ADR-0027/0031), then — **only after the existing three implementations are finished through the scheduled levels (ADR-0034: Level 3 first)** — **Rust → Python → TypeScript** (deprioritized by ADR-0040 add.1: Rust only when the owner starts working in Rust), each at Levels 0–2 in one go — the spec-neutrality stress tests (traits/derive macros, dataclasses, decorators instead of attributes; no exceptions, a synchronous standard driver, no static types, ownership of loaded graphs): their purpose is to find places where the spec is secretly shaped by one language family, and the port changing the spec is the point. Port agents may not read another implementation (ADR-0031's rule); every unanswered question goes into the port's gap list, whose answers are written into the spec sections they amend (ADR-0046). A porting guide follows the sixth implementation.
- Keep the C# public API a thin layer over spec concepts (`EntityMap`, registry, session, rules, runner) so each concept has an obvious counterpart in another language.
- Realistic expectation: Levels 0–2 port with high fidelity because they are data plus rules plus an AST. Level 3 shares behavioral spec and conformance cases, but each language's session API will diverge. Level 4 is per-ecosystem.

## 13. Working agreements

- Small, focused commits; one milestone per PR-sized change. Run the full test suite and build both TFMs before saying anything is done.
- When a decision above turns out to be wrong in practice, say so, propose the change, and record it in `docs/decisions.md`. Never deviate silently.
- Keep the public API minimal. No abstractions "for the future" beyond §10.
- Every new error gets a code in `spec/errors.md` before it gets a message.
- Update the README at the end of every milestone with the SQL patterns a user needs (nesting, arrays, concurrency, migrations workflow).
- If I ask for something in §3 (Deferred), tell me its level and why before doing anything.

## 14. Local environment notes

- Windows 11; SDKs 6.0/8.0/9.0/10.0 installed. Build with .NET 10.
- No Docker, no database server, nothing to start: tests create a temp-file SQLite database per fixture and delete it afterwards. The native SQLite library ships with `Microsoft.Data.Sqlite` (SQLitePCLraw `e_sqlite3` bundle). CI needs only the .NET SDK.
- Live legs: PostgreSQL 16.4 (Laragon's; start it with `pg_ctl start` when it is down) answers on localhost:5432 (`SIMPLEORM_POSTGRES` or the default); a SQL Server 2016 on the owner's LAN is reached through `SIMPLEORM_SQLSERVER` (never committed — the owner gives the address and credentials per session; LocalDB on this machine is broken). Go test runs need `-p 2` here.

# ORM Project Brief — .NET reference implementation, SQLite first

> **Project name: SimpleOrm.** SDK: .NET 10 (10.0.400). Reference database: **SQLite** (ADR-0003; PostgreSQL was the original reference and returns as a Level 4 dialect). Claude Code reads this file at the start of every session, so these rules stay in force without re-pasting.

---

## 1. Vision

A SQL-first ORM that grows in levels: a mapper (Level 0), a micro-ORM with migrations and schema rules (Level 1), relationships and a query model (Level 2), full session semantics comparable to Hibernate but smaller (Level 3), and enterprise hardening (Level 4).

C# on SQLite is the **reference implementation**. The long-term plan is a language-neutral **spec** plus a **conformance suite**, with ports to other languages (Go, Java, PHP) and dialects (PostgreSQL, MySQL, SQL Server, Oracle) that pass the same conformance files. Early decisions are made so that later levels and ports don't require rewrites.

**Current target: Level 1.** Do not build Level 2+ features unprompted. If I casually ask for something listed under "Deferred," tell me which level it belongs to and why it's deferred before doing anything.

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
| Relationships (one-to-many, many-to-one, many-to-many, owned types), graph reshaping | 2 — but declaration-only `[ForeignKey]`/`[ManyToOne]` attributes exist from Level 1 (ADR-0005); loading/reshaping stay Level 2 |
| Query AST + query front-ends (fluent builder, LINQ provider) | 2 — but the criteria core (the AST: `Criteria` factories + `db.Query<T>()` with Where/OrderBy/Limit) was pulled to Level 1 by ADR-0012; lambda/LINQ front-ends stay Level 2 |
| Dynamic SQL composition (optional filters, sorting from UI) | covered at Level 1 by the ADR-0012 criteria core |
| Explicit/batch/eager loading | 2 |
| Identity map, change tracking, unit of work, cascades, merge/detach | 3 |
| Inheritance mapping, event hooks (audit, soft delete) | 3 |
| Draft migrations generated from the metadata diff | 3 |
| Additional dialects (PostgreSQL, MySQL, SQL Server, Oracle) | 4 (seam exists from Level 1) |
| Read/write splitting and multi-database routing, incl. mixed engines (session-level, never per-entity — see decision log 2026-08-28) | 4 |
| Caching, observability, resilience, analyzers, source generators | 4 |

## 4. Stack and targets

### Targeting policy

- **Core library:** multi-target `netstandard2.0;net10.0`. `netstandard2.0` is the compatibility floor (it is the only .NET Standard version worth targeting: 2.1 excludes .NET Framework and .NET Standard is frozen). `net10.0` (current LTS) is where modern APIs and performance work live.
- **Core depends only on `System.Data.Common`** (`DbConnection`, `DbCommand`, `DbDataReader`, `DbTransaction`). Zero provider packages. This is what makes the core usable anywhere and makes dialects pluggable.
- **Dialect packages** reference the provider and multi-target too. `SimpleOrm.Sqlite` references `Microsoft.Data.Sqlite`; it still ships a `netstandard2.0` target, so one unconditional `PackageReference` serves both TFMs (switch to conditional references if a future major drops it). The bundled native SQLite (SQLitePCLraw `e_sqlite3`) is ≥ 3.46, so `RETURNING` (3.35+) and STRICT tables (3.37+) are available.
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
  errors.md                   error code registry (MAP-, PRM-, MIG-, VAL-, CRUD-, QRY-, TX-)
  query-ast.md                Level 2
conformance/                  the executable definition of the library (see §9)
  schema/sqlite/              migrations for the fixture database
  fixtures/                   seed data
  entities/                   expected EntityMap JSON for the fixture entities
  cases/                      query/command/error cases as JSON
  migrations-cases/           runner scenarios as JSON
dotnet/
  SimpleOrm.sln
  src/SimpleOrm/              core: metadata, mapping, parameters, session, rules, migration runner
  src/SimpleOrm.Sqlite/       SQLite dialect (Microsoft.Data.Sqlite)
  src/SimpleOrm.Cli/          migrate / status / validate / baseline / export-metadata
  samples/SimpleOrm.Sample/   sample entity models (User, Role, UserRole, Transaction,
                              TransactionDetail) — the fixture entities for tests and conformance
  tests/SimpleOrm.Tests/      integration tests + the conformance runner
docs/decisions.md             ADR-style log; append whenever a decision below changes
```

Future ports live beside `dotnet/` (`go/`, `java/`, `php/`) and consume `spec/` and `conformance/` unchanged.

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
var id = await db.InsertAsync(order, ct);      // RETURNING the generated key
await db.UpdateAsync(order, ct);               // full row by key; optimistic concurrency if a version column is mapped
await db.DeleteAsync<Order>(id, ct);

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
   - Attributes: `[Table]`, `[Column]`, `[Key]`, `[Generated]`, `[Version]`, `[Ignore]`, `[EnumAsInt]`; declaration-only relationship metadata `[ForeignKey]`, `[ManyToOne]` (ADR-0005); declaration-only DDL metadata `[Index]` (class-level, repeatable, ADR-0007 — consumed by Level 3 draft migrations); relation sources `[View]`, `[MaterializedView]` (separate from `[View]` because it may carry `[Index]`), `[Statement]`, `[Procedure]` (ADR-0008 + addenda; every non-table source carries its defining SQL in the attribute — statements and procedures also their (name, typeof) parameter pairs; materialized views and procedures don't exist on SQLite — dormant until Level 4) — a class carries exactly one of `[Table]`/`[View]`/`[MaterializedView]`/`[Statement]`/`[Procedure]`
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

12. `@name` placeholders bound from the public properties of the args record. SQLite has no array parameters, so `IN` lists are expanded safely at Level 1: a collection-typed property expands `IN (@ids)` to generated placeholders (`@ids_0..@ids_N`), always parameterized, never concatenated. The dialect declares `SupportsArrayParameters`; a future Postgres dialect uses `WHERE id = ANY(@ids)` instead of expansion.
13. A parameter in the SQL with no property (`PRM-001`), or a property never used by the SQL (`PRM-002`), is an error.

### Writes

14. CRUD is generated from `EntityMap`, never from attributes directly. Explicit column lists always. Generated keys via the dialect (`RETURNING`; SQLite supports it since 3.35). Key strategies: database-generated (`INTEGER PRIMARY KEY`), client-generated GUID, natural/composite. (No sequence strategy at Level 1 — SQLite has no sequences; it returns with a dialect that has them.) The FK property is what is written; if a `[ManyToOne]` navigation is non-null and its object's key disagrees with the FK property, `Insert`/`Update` throw a consistency error instead of writing (ADR-0005 addendum). Read-by-key is generated the same way: `GetAsync<T>` (missing row throws) / `GetOrDefaultAsync<T>` (null); composite keys pass a tuple validated at runtime against the `EntityMap` key — arity, order, and types, each mismatch a named error (ADR-0006). CRUD writes exist only for table-backed entities; view-, materialized-view-, statement-, and procedure-backed entities are read-only and refuse writes with a named error (ADR-0008). `InsertAsync` and metadata-generated DDL (`CreateTableAsync`/`CreateViewAsync`, IF NOT EXISTS dev/test utility — versioned migrations stay the schema-evolution path) were pulled forward to milestone 3 by ADR-0011; Update/Delete/Get remain milestone 7.
15. `Update` writes every mapped non-key column by key. Partial updates are hand SQL at Level 1.
16. Optimistic concurrency when a version column is mapped: `... SET version = version + 1 WHERE <key> = @key AND version = @version`; zero rows affected throws `ConcurrencyException` (`CRUD-010`). Same on `Delete`.

### Session

17. `Db` owns one `DbConnection` (obtained from the dialect) and is `IAsyncDisposable`. `BeginAsync` returns a transaction scope; no ambient or static transactions. Every command runs on the session's connection and current transaction, if any. Savepoints are Level 4.

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

22. Migrations are **code, per-object** (ADR-0013; owner: never external .sql files). Root versions `V0001.cs` directly under `Migrations/` compose per-object steps (`Migrations/Table/User/V0002_AddDisplayName.cs`, `View/...`) in explicit order; the root is the recorded unit. Table actions execute **rename → add → remove → raw SQL** regardless of declaration order; every action takes optional per-action `Pre`/`Post` data hooks (inline SQL). Column specs are literal (frozen); metadata-rendered DDL is legal only for an object's initial create. `SqlVersion` is the data-driven form (conformance, future generators). Malformed names `MIG-001`; duplicate versions/objects `MIG-002`; step/root version mismatch `MIG-003`; un-composed steps `MIG-004`. A step without down statements refuses `migrate down` (`MIG-020`).
23. Runner: table `schema_version(version INTEGER, object TEXT, description TEXT, checksum TEXT, applied_at TEXT, execution_ms INTEGER, primary key (version, object)) STRICT` (`applied_at` ISO-8601 UTC). Checksum = SHA-256 of the step's rendered Up SQL; drift on an applied step is `MIG-010`, history unknown to code is `MIG-011`; the whole plan validates before anything executes. The dialect provides the **run lock** — on SQLite the entire run is one `BEGIN IMMEDIATE` transaction (exclusive + atomic: a failed run applies nothing); a future Postgres dialect evolves this to `pg_advisory_lock` + per-migration transactions (`SupportsTransactionalDdl`). `baseline` records versions without running them. Multi-database apps use one migrations namespace (folder) per database. The diff **generator** (authoring aid, Django-style) arrives after milestone 6's introspection; there is never automatic sync against shared databases.
24. The application never applies migrations at startup; it only checks (rule `MIG-030`). The CLI (`--assembly`, `--db`, `--namespace`) applies: `migrate`, `migrate down --to`, `status`, `baseline`, `export-metadata`; `validate` lands with milestone 6.

### Dialect seam (`IDialect`, minimal and capability-based)

25. Members at Level 1, and no more: create connection; quote identifier; parameter prefix; render `RETURNING`/generated-key retrieval; render limit/offset; render CREATE TABLE/INDEX/VIEW and the generated INSERT from `EntityMap` (ADR-0011, ADR-0008 add.3); capability flags — array parameters, transactional DDL, materialized views, procedures; migration run-lock acquire/release; describe-statement implementation; declared-type → CLR type compatibility table. Add members only when a second dialect needs them. Do not speculate about Oracle.

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
- `migrations-cases/`: a folder of migration files plus `{ "preState", "command", "expectStatus" | "error" }`.
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
- **Level 3:** session-wide identity map; snapshot-based change tracking; unit of work with FK-ordered flush, batching, cascades; simple detach/merge rules; event hooks; optional inheritance mapping; draft migrations from the metadata diff with `[RenamedFrom]` declarations.
- **Level 4:** additional dialects with the full conformance matrix in CI; source-generated registry and mappers; statement caching; observability (tagged SQL comments, tracing, redacted logging); resilience; N+1 and slow-query diagnostics; analyzers; API stability and semantic versioning.

## 12. Multi-language plan

- Ports share **no code**; they share `spec/` and `conformance/`. C# is the reference; a port is correct when it passes the same conformance files unchanged.
- Do not port before Level 1 exit criteria are met. The first port covers Levels 0–1 only.
- **Port to Go first**, precisely because it is the most different (struct tags instead of attributes, `database/sql` instead of ADO.NET, no exceptions, no async/await, no inheritance). Its purpose is to find places where the spec is secretly C#-shaped. Expect the port to change the spec; that is the point. Java second (closest, validates ergonomics). PHP last (request-scoped lifecycle changes session semantics).
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

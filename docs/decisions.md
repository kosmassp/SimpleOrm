# Decisions

ADR-style log. Append an entry whenever a decision in `CLAUDE.md` §7 changes or a new
load-bearing decision is made. Never deviate silently.

---

## ADR-0001 — Project name and SDK (2026-08-26)

**Decision.** The project is named **SimpleOrm**: solution `SimpleOrm.sln`, packages
`SimpleOrm`, `SimpleOrm.Postgres`, `SimpleOrm.Cli`. The .NET 10 SDK (10.0.400) was
installed and is the build SDK; the core and dialect multi-target
`netstandard2.0;net10.0` as the brief specifies.

**Status.** Accepted.

## ADR-0002 — Local PostgreSQL instead of Testcontainers (2026-08-26)

> Superseded by ADR-0003: the reference database is now SQLite and no server is
> needed at all. Kept for history; the "no mocked connections" principle carries over.

**Context.** The brief specifies Testcontainers.PostgreSql (real Postgres in Docker)
for integration tests. The development machine has no Docker and the owner decided not
to install it ("no need to have docker").

**Decision.** Integration tests run against a real PostgreSQL server reached through
the `ORM_TEST_CONNECTION` environment variable, defaulting to the machine's local
Laragon PostgreSQL 16.4 (`localhost:5432`, user `postgres`, trust auth). The test
fixture creates the `simpleorm_test` database if it is missing. CI provides a
`postgres:16` service container and sets the same variable. The principle that matters
— **no mocked connections anywhere, every test talks to real Postgres** — is preserved;
only the provisioning mechanism changed.

**Consequences.** Tests are not hermetic on the dev machine (state lives in a shared
local server), so every test must create and drop its own schema objects or use unique
names. If Docker becomes available later, swapping the fixture back to Testcontainers
is a small, isolated change (one fixture class).

**Status.** Accepted.

## ADR-0003 — SQLite replaces PostgreSQL as the reference database (2026-08-27)

**Context.** The owner decided a running PostgreSQL server is more infrastructure than
this stage of the project warrants ("postgresql is overkill, let's use sqlite for
now"). The tradeoffs were reviewed explicitly before deciding: SQLite is dynamically
typed, has no array parameters, no advisory locks, no date/time types, and no
server-side statement description, all of which shaped Level 1 decisions.

**Decision.** The C# reference implementation targets **SQLite** (via
`Microsoft.Data.Sqlite`) instead of PostgreSQL. The dialect package is
`SimpleOrm.Sqlite`; PostgreSQL moves to the Level 4 "additional dialects" list
(the existing `IDialect` seam is unchanged). CLAUDE.md §7 decisions were reworked
accordingly:

- **IN lists** (§7.12): SQLite has no array parameters, so safe IN-list expansion
  (generated `@p0..@pN` placeholders, always parameterized) is now a **Level 1**
  feature instead of Level 4. The `SupportsArrayParameters` capability flag stays so
  a future Postgres dialect can use `= ANY(@ids)`.
- **Dates** (§7.9): no native date types; the convention is ISO-8601 UTC `TEXT`
  (trailing `Z`), `DateTime.Kind = Utc` only. The `timestamp without time zone` lint
  (`VAL-020`) becomes a lint on non-UTC / non-ISO storage patterns, finalized in
  milestone 4.
- **SchemaGuard** (§7.19): no server describe; SQL is validated by *preparing* the
  statement without executing it. Column type/nullability checks work only for
  table-backed columns; expression columns require nullable properties unless
  annotated. Fixture and migration tables use SQLite **STRICT tables** (3.37+) so
  declared types are actually enforced, preserving "strict by default" as far as
  SQLite allows.
- **Migration locking** (§7.23): advisory locks are replaced by an exclusive write
  transaction (`BEGIN IMMEDIATE`) held for the run; the dialect abstracts this as a
  "run lock". SQLite has transactional DDL, so per-migration transactions stay.
- **Generated keys** (§7.14): `RETURNING` is supported (SQLite ≥ 3.35, bundled via
  SQLitePCLraw). The `sequence` key strategy is dropped from Level 1 (SQLite has no
  sequences); database-generated means `INTEGER PRIMARY KEY`.
- **Async** (§2): the public API stays async-only with `CancellationToken` (it is the
  spec-level contract ports implement), acknowledging that the SQLite provider is
  synchronous underneath.

**Consequences.** Tests need no server or Docker at all: each fixture is a temp-file
SQLite database, deleted afterwards; `ORM_TEST_CONNECTION` is gone and CI needs no
service container. The Laragon PostgreSQL server started for ADR-0002 was stopped.
Validation guarantees are inherently weaker than Postgres could give; STRICT tables
recover most of it. If a stronger reference is wanted later, Postgres returns as a
Level 4 dialect through the same seam. Supersedes ADR-0002.

**Status.** Accepted.

## ADR-0004 — Attribute mapping is opt-in via [Column] (2026-08-27)

**Context.** Two possible attribute-mapping models: opt-out (every public property is
mapped by convention; `[Ignore]` excludes) or opt-in (only annotated properties are
mapped). The original brief implied opt-out. The owner prefers opt-in: what is mapped
is visible in the model, property by property.

**Decision.** In the attribute loader, a property is mapped **iff it carries
`[Column]`**. The attribute takes an optional name: `[Column("created_at")]` binds
explicitly; bare `[Column]` derives the column name from the property name through
the active naming convention (default snake_case: `UserId` → `user_id`; SQLite
identifiers are case-insensitive, so single-word names also match verbatim).

To preserve "nothing is silent" (CLAUDE.md §2), absence is not allowed to mean
anything: a public settable property with **neither `[Column]` nor `[Ignore]` is a
loader error** (code registered in `spec/errors.md` when the loader lands in
milestone 2). `[Ignore]` therefore stays: it is how a property declares "not a
column" explicitly. The convention loader (for types with no mapping attributes at
all) still maps every public property by convention, and the manual
`EntityMapBuilder<T>` is unchanged — loader precedence explicit → attribute →
convention stays as in §7.2.

**Consequences.** Annotated models are more verbose (every mapped property carries
`[Column]`) but self-documenting, and a forgotten annotation fails fast instead of
silently dropping a column. The sample models are updated accordingly and CLAUDE.md
§7.6 is reworded.

**Status.** Accepted.

## Design seed — read/write splitting and multi-database routing (2026-08-28)

**Context.** The owner proposed a `[Connection]` attribute (per-entity connection
string, read vs. write connections, entities split across databases, potentially
mixed engines such as SQLite + Postgres).

**Decision.** Scheduled for **Level 4**, and when it lands it will be
**session-level, never per-entity**, because:

- `EntityMap` is a conformance artifact describing data shape; a connection name is
  deployment configuration and would make the export environment-dependent.
- `Db` owns one connection and its transaction (§7.17); per-entity connections would
  silently break transaction atomicity. Cross-database work stays two visible
  sessions, two transactions.
- Mixed engines already fall out of the dialect seam: one `Db` per
  (dialect, connection string); which database an entity uses is which session it is
  handed to. Wrong wiring is caught at startup by SchemaGuard, which validates each
  registry against the real schema of the session it is given — so organize queries
  in one registry class per database.

Open question for Level 4: whether the *registry* (not the entity) gets a logical
data-source name for routing/validation ergonomics, and how read-replica routing
expresses read-your-writes consistency explicitly.

**Status.** Deferred to Level 4 (row added to CLAUDE.md §3).

## ADR-0005 — Relationship declaration attributes at Level 1 (2026-08-28)

**Context.** The owner wants models to express "this FK column references that
entity" and to hold a model-typed property (`Transaction.User`, not just `UserId`).
Relationship *behavior* — loading, graph reshaping — is Level 2 (§3), and that was
restated before deciding. The owner also asked what to call the not-a-column marker
(NonReal / Transient / Volatile).

**Decision.** The *declaration layer* is pulled forward to Level 1; behavior stays
Level 2:

- `[ForeignKey(typeof(User))]` on a mapped column property declares that the column
  references another entity's primary key. Metadata only; valid without a navigation
  property.
- `[ManyToOne(nameof(UserId))]` declares a navigation property and names its FK
  property explicitly (naming, not type-matching, keeps two FKs to the same entity
  unambiguous — e.g. `SenderId`/`ReceiverId` both referencing `User`).
- No separate `[Transient]` marker: a `[ManyToOne]` property is inherently transient
  (never a column, never written by CRUD), so the relationship attribute carries the
  meaning; `[Ignore]` remains for unrelated non-column properties. The ADR-0004 rule
  extends to: a public settable property must carry `[Column]`, `[Ignore]`, or a
  relationship attribute — anything else is a loader error.
- **At Level 1 the library never populates a navigation property** (no hidden
  queries, §2). User code assigns it after an explicit query; the property should be
  nullable. Level 2 explicit/eager loading attaches to these same declarations.
- Collection sides (`[OneToMany]`, many-to-many) are NOT pulled forward; they arrive
  with Level 2 loading, which is what makes them meaningful.

**Consequences.** Samples can model the full graph now (sample-first), the milestone 2
loader must record relationship metadata in `EntityMap` (declaration only), and
Level 2 starts from attributes that already exist instead of inventing them.

**Status.** Accepted.

### ADR-0005 addendum — navigation/FK consistency is enforced at the write boundary (2026-08-28)

**Context.** With both `Transaction.UserId` and `Transaction.User` on the model, the
two can disagree in memory. The owner wants the library — not hand-written setters —
to prevent a row being saved with a `user_id` different from the assigned `User`.
Setter injection into POCOs is not possible without runtime proxies (banned, §2) or
source generators (Level 4).

**Decision.** The FK property is what CRUD writes. On `Insert`/`Update`, if a
`[ManyToOne]` navigation is non-null and the key of the assigned object disagrees
with the FK property value, the operation **throws a consistency error** instead of
writing (error code registered in `spec/errors.md` when CRUD lands, milestone 7).
An inconsistent pair can exist transiently in memory but can never reach the
database. Reads stay consistent by construction: Level 2 explicit loading populates
the navigation from the FK. Full in-memory auto-sync (relationship fixup) is the
Level 3 change tracker''s job, as in EF; Level 4 may add an optional source-generated
sync setter.

**Status.** Accepted.

### ADR-0005 addendum 2 — navigation properties have no public setter (2026-08-28)

**Context.** The write-boundary mismatch check (addendum 1) still allowed user code
to *create* an inconsistent navigation/FK pair in memory. The owner chose to close
the front door instead: forbid public setters on navigation properties.

**Decision.** A `[ManyToOne]` property must not expose a public setter — declare it
`{ get; private set; }`. The library is its only writer (Level 2 loading populates it
from the FK, via the non-public setter), so navigation and FK can never disagree by
construction. The loader rejects a navigation property with a public setter (error
code registered with the loader). The same rule will bind `[OneToMany]` when Level 2
introduces it: collection navigations are get-only. Consequences accepted
explicitly: at Level 1 navigation properties are pure declarations and stay null —
there is no supported way to populate them until Level 2 loading exists. The
addendum-1 write-time mismatch check is kept as defense-in-depth (reflection and
deserializers can still bypass access modifiers).

**Status.** Accepted.

## ADR-0006 — Read-by-key API: GetAsync / GetOrDefaultAsync (2026-08-28)

**Context.** §6 had CRUD by key for Insert/Update/Delete but no read-by-key; finding
an entity by id required registering a hand-written query. Hibernate
(`session.find`), EF (`db.Users.FindAsync`), and Eloquent (`User::find`) were
compared: session-centric vs. collection-facade vs. Active Record statics.

**Decision.** Session-centric, generated from `EntityMap` like the rest of CRUD
(milestone 7):

- `db.GetAsync<T>(key, ct)` — the default: a missing row **throws** with an error
  code naming the entity, table, and key. Strict-by-default, mirroring the existing
  `QuerySingleAsync` / `QuerySingleOrDefaultAsync` pair.
- `db.GetOrDefaultAsync<T>(key, ct)` — the explicit opt-in null-returning variant.
- Composite keys pass a **tuple** (`(userId, roleId)`) validated at runtime against
  the `EntityMap` key definition — arity, order, and types, each mismatch a named
  error. Stricter than EF''s `object[]`; compile-time-checked typed overloads can come
  from the Level 4 source generator.
- No Active Record statics: which session (and so which database and transaction) is
  always visible at the call site (§7.17, and the multi-database design seed).
- Level 1 always queries (one visible `SELECT` with explicit columns). The Level 3
  identity map may later satisfy the call from the session cache without any API
  change. Error codes registered in `spec/errors.md` when CRUD lands.

**Status.** Accepted; implemented in milestone 7.

## ADR-0007 — [Index] mixes DDL declaration into mapping metadata (2026-08-28)

**Context.** Indexes are DDL, and the project''s direction is schema-from-migrations,
so the recommendation was to keep `CREATE INDEX` in migration files only. The owner
explicitly chose otherwise: "I am going to mix both DDL and mapping in there" —
class-level index declarations on the model, several per entity, with optional names.

**Decision.** `[Index]` is a class-level, repeatable attribute placed below
`[Table]`: `[Index(nameof(UserId))]`,
`[Index(nameof(Status), nameof(CreatedAtUtc), Name = "...", Unique = true)]`.
Columns are referenced by property name and resolved through the column mapping
(unknown or unmapped property = loader error); an omitted name is derived as
`ix_<table>_<col1>[_<colN>]`. Declared indexes are recorded in `EntityMap` (§7.1
updated) and exported with the metadata JSON.

**Scope by level.** Level 1: declaration-only — nothing generates or verifies
indexes; real indexes still come from migration SQL. Level 3: draft migrations
generate `CREATE INDEX` from this metadata (its consuming feature). Open option:
SchemaGuard may later verify declared indexes exist (`PRAGMA index_list`), decided
when milestone 6 scope is set.

**Status.** Accepted (owner overrule of the schema-only-in-migrations recommendation,
recorded per working agreement).

### ADR-0007 addendum — per-column sort order (2026-08-28)

`[Index]` gains `Descending` (bool array parallel to the columns, e.g.
`new[] { false, true }` for `(status ASC, created_at DESC)`) and an
`AllDescending` shorthand, mirroring EF Core''s shape — arrays of constants are the
only per-column syntax attributes allow. Omitted means all ascending. Loader errors:
`Descending` length differing from the column count, or combining `Descending` with
`AllDescending`. SQLite supports `DESC` index columns, so Level 3 generation renders
it directly.

### ADR-0007 addendum 2 — inline per-column direction replaces parallel arrays (2026-08-28)

The owner asked for `Index(name, [Status, DESC], [CreatedAt, ASC], [XXXX])`-style
inline direction. C# attributes cannot express tuples or jagged arrays (CS0182), so
the closest legal form is adopted and **replaces** addendum 1''s
`Descending`/`AllDescending` arrays: each column is one string,
`"PropertyName"` (ascending by default) or `"PropertyName ASC|DESC"`, e.g.
`[Index(nameof(Status), nameof(CreatedAtUtc) + " DESC")]` — constant concatenation
keeps `nameof` refactor-safety. The attribute stores raw strings; the loader parses
them (property resolved through the column mapping, direction token
case-insensitive), and an unknown property, bad token, or empty column list is a
loader error.

### ADR-0007 addendum 3 — SortOrder token stream replaces the string suffix (2026-08-28)

The owner found the `nameof(X) + " DESC"` concatenation unintuitive and proposed
`new[] { nameof(UserId), "DESC" }` per column — jagged arrays are not attribute-legal
(CS0181/0182), but `params object[]` with enum constants is. Adopted, replacing
addendum 2''s string-suffix form: the columns are a token stream read left to right —
a string names a property, a `SortOrder` (`Asc`/`Desc`) applies to the column before
it, no token means ascending:
`[Index(nameof(Status), nameof(CreatedAtUtc), SortOrder.Desc)]` =
`(status ASC, created_at DESC)`. Direction is now typed (a misspelled direction no
longer compiles). Loader errors: leading or doubled `SortOrder`, a token that is
neither string nor `SortOrder`, unknown/unmapped property, empty list. Pattern
precedent: xUnit `[InlineData(params object[])]`.

## Decision — session-first confirmed; ergonomics belong to an app-side DAO layer (2026-08-28)

**Context.** After comparing Hibernate/EF/Eloquent, discussing lazy loading
(rejected; Laravel''s own `preventLazyLoading` cited), an Active-Record facade
(`Model<T>` statics over an ambient session), and the owner''s prior hand-rolled DAO
stack (Fidelis: models never held connections; a singleton `DBFactory` was the
ambient session, with a global-transaction TODO as its scar), the owner decided:
"I will use Db like you said, it can be controlled in the DAO layer later."

**Decision.** The library API stays session-first data mapper: `Db` is the only
gateway (§6, §7.17, ADR-0006). No Active-Record facade, no `Model<T>` statics, no
ambient session in the library at any level. Call-site convenience (per-entity DAOs,
repositories, managers) is **application architecture layered on top of `Db`** —
thin classes taking the session as a dependency — and stays out of the library.
Level 2 still owes `include:` + batch `LoadAsync` per the outline; the Level 4
facade idea is dropped unless the owner reopens it.

**Status.** Accepted.

> Clarification (same day): the Fidelis stack is cited as pain-point evidence only —
> the owner notes it was "made with haste" and it is not a template. The shape of any
> app-side layer above `Db` is deliberately unspecified and will be designed fresh,
> sample-first, when needed.

## ADR-0008 — Relation sources: [Table], [View], [Statement] (2026-08-28)

**Context.** The owner asked for a mapping layer for custom queries ("Statement
attribute, like Table but for custom query") plus view and materialized-view
mapping. Precedent: EF keyless entities with `ToView`/`ToSqlQuery`, Hibernate
`@Subselect`.

**Decision.** `EntityMap` gains a **relation source**; a class carries exactly one of
`[Table]`, `[View]`, `[Statement]` (two sources = loader error):

- `[View("name")]` — read-only entity over a database view. `[Key]` allowed (enables
  `GetAsync` at milestone 7); `[Generated]`, `[Version]`, `[Index]` are loader
  errors. `Materialized = true` marks a materialized view: mapping-identical, refresh
  is a dialect operation, and **SQLite has none** — the flag is dormant metadata,
  testable only when a dialect with materialized views arrives (Level 4 Postgres).
  Chosen over a separate `[Materialized]` attribute because the mapping layer sees no
  difference.
- `[Statement("Reports/DailySales.sql")]` — the class is the result shape of a
  `.sql` embedded resource (path relative to `Sql/`, honoring §7.5: SQL in files,
  never inline in attributes). Read-only and keyless at Level 1 (`[Key]`,
  `[Generated]`, `[Version]`, `[Index]` are loader errors). Complements the registry:
  the registry binds (args, result) pairs for arbitrary queries; `[Statement]` makes
  a class self-describing so SchemaGuard validates it by preparing the statement,
  no registry entry needed.
- CRUD writes exist only for table-backed entities; view/statement writes refuse
  with a named error (§7.14 updated).

**Samples.** `UserTransactionTotal` (`[View]`, keyed on `user_id`) and `DailySales`
(`[Statement]` over `Sql/Reports/DailySales.sql`, embedded via the sample csproj).
Projections do not extend `BaseModel`. The view''s `CREATE VIEW` DDL arrives with
migrations (milestone 5).

**Status.** Accepted.

### ADR-0008 addendum — standalone [MaterializedView], new [Procedure]; SEQUENCE deferred (2026-08-28)

**MaterializedView.** The owner overruled the Materialized-flag-on-[View] design with
a correct capability argument: a materialized view is physically stored and **can be
indexed**, a plain view cannot. Attribute legality should follow the attribute, not a
flag, so `[MaterializedView("name")]` is standalone: read-only, `[Key]` allowed,
`[Index]` ALLOWED (the distinguishing capability), `[Generated]`/`[Version]` errors.
Refresh is a dialect operation.

**Procedure.** `[Procedure("name")]` maps a class to the result set of a stored
procedure / set-returning function; parameters bind from an args record at call time;
read-only and keyless at Level 1; invocation rendering is dialect-specific
(`EXEC` / `SELECT * FROM fn(...)` / `CALL`).

Both are **dormant on SQLite** (it has neither) — declaration-only metadata,
testable when a Level 4 dialect arrives; deliberately no sample entities until then,
since SchemaGuard could never validate them against the reference database. The
exclusivity rule is now: exactly one of
`[Table]`/`[View]`/`[MaterializedView]`/`[Statement]`/`[Procedure]`.

**SEQUENCE — considered, deferred (recommendation accepted pending owner decision).**
Unlike the relation sources above, a sequence is not a mapping concept — it is a key
*strategy* detail (ADR-0003 dropped the sequence strategy with SQLite). An attribute
today would be dormant metadata with no consumer at any Level 1-3 milestone. When
Postgres returns at Level 4, the sequence key strategy returns with it, and a
`[Sequence("name")]` (or a Key-strategy parameter) can be added against real tests.
Revisit then, or earlier if the owner decides otherwise.

> Refinement (owner, same day): a sequence is not an attribute/class mapping at all —
> it is a standalone schema object that needs its own place in the metadata model
> (e.g. a named sequence declaration that a key strategy references, migrations
> create, and the dialect renders). Deferred as agreed; when it lands (Level 4 with
> Postgres), design it as a first-class metadata object, not an entity attribute.

### ADR-0008 addendum 2 — [Statement] carries inline SQL and a declared parameter contract (2026-08-28)

**Context.** The original design linked `[Statement]` to a `.sql` embedded resource
per §7.5. The owner overruled: the SQL should be text in the attribute, and the
parameter names AND types must be declared there too — a statement entity is fully
self-contained.

**Decision.** `StatementAttribute(string sql, params object[] parameters)`: the SQL
is an inline constant (raw string literals keep it readable), followed by parameter
declarations as **(name, Type) token pairs** read left to right — the same
token-stream style as `[Index]`, since attributes allow strings and `typeof` but not
tuples: `[Statement("... where created_at >= @since", "since", typeof(DateTime))]`.
Loader errors: odd token count, a token that is neither string nor `Type`, duplicate
names, or declared parameters mismatching the SQL''s `@placeholders` in either
direction (PRM family). §7.5 gains the explicit exception; registry queries keep
their `.sql` files. Tradeoff accepted knowingly: inline SQL loses .sql-file tooling
(highlighting, external review) and gains a self-describing class that SchemaGuard
validates without a registry entry. The sample `DailySales` now carries its SQL and
a `since` parameter; the sample''s `Sql/` resource folder is gone.

**Status.** Accepted (owner overrule of SQL-in-files for this source).

> Note (owner request, same day): sample entities for the dormant sources were added
> after all — `MonthlySalesTotal` (`[MaterializedView]`, with the distinguishing
> `[Index]`) and `UserActivityReport` (`[Procedure]`). Consequence for milestone 6:
> the dialect gains capability flags (`SupportsMaterializedViews`,
> `SupportsProcedures`) and SchemaGuard SKIPS relation sources the dialect cannot
> host instead of failing on them — so these samples validate as dormant on SQLite
> and light up automatically when a Level 4 dialect arrives.

## ADR-0009 — Inline SQL is the primary registry form (2026-08-28)

**Context.** §7.5 made `.sql` embedded resources the rule and `Query.Inline` the
escape hatch. The owner inverted it ("I don''t want any Query.Embedded"), consistent
with the `[Statement]` decision (ADR-0008 addendum 2): SQL lives next to the code
that owns it.

**Decision.** `Query.Inline(...)` is the primary form; raw string literals keep
multi-line SQL readable, and the registry entry carries SQL, args type, and result
type in one place. `Query.Embedded` **remains in the library** as the supported
option for teams preferring `.sql` files (mechanism + `QRY-003` + one covering
test kept) — removing it entirely is a separate decision the owner has not made.
The samples gained a full inline registry (`Schema` DDL commands — interim until
milestone 5 migrations — plus `Queries`/`Commands` with their args records), and
the integration tests now consume it, which also makes it the registry SchemaGuard
will enumerate at milestone 6. SchemaGuard validates both forms identically. The
Level 4 generate-registry-from-.sql idea narrows to embedded users only.

**Status.** Accepted.

## ADR-0010 — Statement entities execute by type; the registry is the escape hatch (2026-08-28)

**Context.** The owner: the point of `[Statement]` is "to avoid calling the Query
directly" — custom reads should flow through typed declarations on model classes,
not free-floating registry entries; an open direct-query door "defeats the purpose"
of the ORM, even while conceding generated calls alone are never enough.

**Decision.** Statement-backed entities execute through the session by type:
`db.QueryAsync<DailySales>(new DailySalesArgs(since), ct)` (plus
`QuerySingleAsync` / `QuerySingleOrDefaultAsync` / `StreamAsync` variants) — SQL and
parameter contract come from the entity''s `EntityMap`, no registry entry involved.
Args bind against the declared parameters: a type mismatch is `PRM-012`; calling the
statement API on a non-statement type is `QRY-004`; name mismatches reuse
PRM-001/002 at bind time.

**Resulting layering of read surfaces, preferred first:**
1. Generated CRUD by key — `GetAsync`/`GetOrDefaultAsync` (milestone 7).
2. `[Statement]` entities — custom SQL as a typed, self-contained declaration.
3. The Level 2 query model (AST) — composable typed queries, when it arrives.
4. The registry (`Query.Inline`, optional `Query.Embedded`) — the **explicit escape
   hatch**, kept because typed surfaces cannot express everything (e.g. a custom
   WHERE returning an existing table entity such as `User`); still validated by
   SchemaGuard, never removed silently.

**Status.** Accepted.

## ADR-0011 — DDL and INSERT are generated from metadata via the dialect (2026-08-28)

**Context.** The owner: the sample''s handwritten `Schema` and insert `Commands`
"should not exist — the ORM should be able to map it directly from the attribute.
DDL/Insert should be generated from the dialect instead."

**Decision.** Pulled forward from milestone 7 (insert) and formalized now (DDL):

- `IDialect` gains renderers: `CreateTableSql` (column types from metadata, NOT NULL
  from nullability, key per strategy — `INTEGER PRIMARY KEY` for database-generated,
  composite `primary key (…)` for natural, STRICT), `CreateIndexSql`, and
  `InsertSql` (explicit non-generated column list, `RETURNING` for generated keys).
- `db.CreateTableAsync<T>` / `db.CreateViewAsync<T>`: idempotent (IF NOT EXISTS)
  **dev/test utility** — versioned migrations (milestone 5) remain the
  schema-evolution path; whether generated DDL feeds migrations further is a
  milestone 5 discussion. Wrong source kind → `DDL-001`; materialized view on a
  dialect without them → `DDL-002` (`SupportsMaterializedViews`).
- `db.InsertAsync<T>(entity, ct)`: generated from `EntityMap`; database-generated
  keys read back via RETURNING and written onto the entity; empty client-GUID keys
  assigned first; read-only sources throw `CRUD-003`; navigation/FK disagreement
  throws `CRUD-004` (ADR-0005 add.1, enforced early). Returns `Task` — the §6
  "returns id" sketch is finalized at milestone 7 with Update/Delete/Get.
- The sample''s `Schema` class is deleted; `Commands` shrinks to the partial update
  (§7.15 — legitimately hand SQL); `Queries` remains the ADR-0010 escape hatch.

**Status.** Accepted.

### ADR-0008 addendum 3 — every non-table source carries its defining SQL (2026-08-28)

The owner: views, materialized views, and procedures must carry their SQL in the
attribute like `[Statement]` does ("I missed checking this"). `[View(name, sql)]`,
`[MaterializedView(name, sql)]`, `[Procedure(name, sql, params…)]` — procedures also
declare (name, Type) parameter pairs, validated against the body''s placeholders
(PRM-010/011); view/matview defining SELECTs take no parameters (a placeholder is
PRM-010); empty SQL is MAP-019. `EntityMap.StatementSql` became `DefiningSql`; the
JSON export includes the normalized SQL (and procedure parameters), so ports must
reproduce it. `CreateViewAsync` generates CREATE VIEW from it; procedure creation
has no renderer until a dialect with `SupportsProcedures` exists (Level 4).

### ADR-0011 addendum — generated select-all; sample DAO layer (2026-08-28)

`db.QueryAllAsync<T>` joins the generated surface: explicit column list from
metadata, ordered by the key when one exists; works for tables, views, and
materialized views (statement/procedure → `QRY-005`). This removed the last
no-filter registry queries.

The sample gains the reference **DAO layer** (`Dao/BaseDao<TEntity>` + `UserDao`,
`TransactionDao`): instance-based with the session constructor-injected — not
extension methods (owner considered them; C#-only idiom, and no inheritance means
no base object) and never statics (ambient session). Generic operations come from
generated code; per-entity methods wrap the registry escape hatch and the statement
entity. Criteria finds ("user with specific criteria, no query per table" — owner)
are explicitly the **Level 2 query AST** (§10.4 forbids a string-based interim);
the base class documents that slot. Milestone 7 adds Get/Update/Delete to the base.

## Design seed — Level 2 criteria API (2026-08-28)

**Context.** The owner compared LINQ (best in C#, unportable), Hibernate Criteria,
and Eloquent chaining while planning ports that now include **Rust** alongside Go
(new — §12 currently lists Go/Java/PHP; confirm Rust''s place when Level 1 exits).
Their sketch: `db.get<User>.where(Criteria.Or(Criteria.Eq("Id",1), …))`.

**Direction (Level 2, not implemented at Level 1 — §10.4 stands).**
- **Criteria objects are the AST and the portable spec core**: static factories
  (`Eq`, `In`, `Ge`, `And`, `Or`, `Not`, …) composing an explicit tree — expressible
  in every target language incl. Rust/Go (no inheritance, extensions, or LINQ
  required). Explicit nesting also removes SQL''s and/or precedence ambiguity, which
  the owner''s own example sketch tripped over.
- **Session-first surface**: `db.Query<User>().Where(criteria)…` (Eloquent''s chained
  *feel*, Hibernate''s bones, no model base class — that would only be needed for
  Eloquent-style statics, already rejected). `BaseDao<T>.FindAsync(criteria)` is the
  DAO integration ("no query per table").
- Criteria reference **property names**, resolved to columns through `EntityMap`
  (`nameof` in C#; unknown name = named error). Rendering emits explicit columns
  (never `select *`, VAL-021) and binds every value as a parameter, reusing IN-list
  expansion.
- **LINQ is a Level 2+ optional C# front-end** compiling lambdas into the same
  criteria tree; ports skip it. Conformance gains `ast/` cases: criteria tree as
  JSON → expected SQL per dialect (§9 already reserves this).

**Status.** Seed for the Level 2 design; recorded so milestone work at Level 1
(4–8) doesn''t foreclose it.

> Clarification (owner, same day): `Where(...)` accepts multiple criteria that are
> **implicitly ANDed** — `Where(Or(Eq("Id",1), In("Name",…)), Ge("Created", now))`
> renders `(id = 1 or name in (…)) and created_at >= @p`. Hibernate''s add()
> accumulation / Eloquent''s chained-where semantics; each argument is its own tree.

> Scope addition (owner, same day): the criteria chain includes **ORDER BY**
> (e.g. `.OrderBy("CreatedAtUtc", SortOrder.Desc)` — reusing the SortOrder token,
> property names resolved through EntityMap) and naturally limit/offset. **GROUP BY
> is deliberately excluded**: aggregations are written as raw SQL in `[Statement]`
> entities — criteria stay a row-filter/sort language, never a full SQL replacement.

## ADR-0012 — Criteria core and key reads pulled to Level 1 (2026-08-28)

**Context.** After milestone 4 the sample registry held only reads whose typed
replacements were scheduled (milestone 7 key reads, Level 2 criteria). The owner
directed both pulls: "pull the GetAsync forward and pull criteria forward from
Level 2."

**Decision.**
- **Key reads (ADR-0006, early):** `db.GetAsync<T>(key, ct)` (missing row
  `CRUD-001`) / `GetOrDefaultAsync` (null); composite keys pass a ValueTuple
  validated against the EntityMap key — arity, order, types (`CRUD-002`; safe
  integer widening allowed so `GetAsync<Order>(7)` works on a long key); works on
  tables and keyed views; statements/procedures throw `QRY-005`.
- **Criteria core (the §10.4 AST):** `Criteria` factories (Eq/Ne/Gt/Ge/Lt/Le/Like/
  In/IsNull/IsNotNull/And/Or/Not) build explicit trees;
  `db.Query<T>().Where(…).OrderBy(prop, SortOrder).Limit/Offset` — Where args and
  repeated calls implicitly AND. Property names resolve through EntityMap
  (`QRY-006` unknown), values always bind as parameters (`@c0…`), selects list
  explicit columns, empty In renders `1 = 0`, limit/offset render via the new
  dialect member `LimitOffsetClause`. GROUP BY stays excluded (statements own
  aggregation). **Still Level 2:** lambda/LINQ front-ends compiling to this tree,
  and `ast/` conformance cases (added when the tree gets a JSON form).
- `BaseDao<T>` gains `GetAsync`/`GetOrDefaultAsync`/`FindAsync(criteria)`/`Query()`.
  The sample `Queries.cs` registry is **deleted** — every read is typed now;
  `Commands.cs` keeps the single §7.15 partial update, the registry mechanism
  (`Query.Inline`/`Embedded`) remains the library''s escape hatch per ADR-0010.

**Status.** Accepted (owner overrule of the Level 2 timing; §10.4 honored — the
pulled-forward form IS the AST, no strings).

## ADR-0013 — Migrations: versioned code, per-object, generated at authoring time (2026-08-29)

**Context.** The brief specified versioned .sql files (Flyway-style). The owner:
"I don''t want the migrate only run from sql file" — consistent with the inline-SQL
philosophy throughout — and, after comparing EF/Hibernate-Flyway/Laravel plus the
wider design space (state-based, hybrid-generated, DAG, snapshot, zero-downtime,
dialect-abstraction schools), sketched a per-object structure with per-action
data hooks. Auto-sync against shared databases was considered and rejected
(rename-vs-drop ambiguity, no data migrations, SQLite ALTER limits, no history);
the owner''s conclusion: "no auto migrate, but the migration is automatically
generated."

**Decision.**
- **Structure:** root `V<version>` classes under `Migrations/` are the recorded,
  checksummed units; they compose per-object steps
  (`Migrations/Table/<Object>/V<version>_<Desc>.cs`, `View/…`) in explicit order.
  Folder = namespace; one namespace per database. Autoscan validates (orphan steps
  MIG-004, version mismatches MIG-003) but the root decides.
- **Actions:** tables execute rename → add → remove → raw SQL regardless of
  declaration order (the rename-first rule is the data-loss answer); views run as
  declared. Optional per-action `Pre`/`Post` hooks carry data work and share the
  version''s atomicity. Literal column specs keep applied migrations frozen;
  metadata-rendered DDL only for initial creates (frozen by checksum).
- **Recording:** one `schema_version` row per (version, object); checksum = SHA-256
  of rendered Up SQL; full-plan validation (MIG-010/011/020) before execution; the
  SQLite run = one `BEGIN IMMEDIATE` transaction (lock + whole-run atomicity).
- **Auto vs manual dissolves at authoring:** the post-milestone-6 diff generator
  writes these same artifacts by diffing metadata against a migrated database;
  hand-written and generated versions interleave freely. Dev-sync
  (`CreateTableAsync`) remains a dev/test utility; `baseline` covers fresh installs.
- The sample''s test schema is now created exclusively by its `Migrations/` tree
  (including seed data via a `.Post` hook); the CLI loads the app assembly.

**Status.** Accepted.

### ADR-0013 addendum — the diff baseline is a shadow database (2026-08-29)

The owner pinned the generator''s baseline: the model is the final truth
(code-first), but "sync based on what? not all devs have a read on the database;
production could be different." Decision: the generator diffs metadata against a
**shadow database** — the committed migrations replayed into a throwaway SQLite
(temp/:memory:), then introspected. SQLite being embedded makes this
environment-free: every dev always has the baseline, deterministically ("the state
history produces"), with no snapshot artifact to maintain. Real databases are never
diffed: apply time trusts only recorded history (MIG-010/011 refuse divergence)
and milestone 6''s SchemaGuard validates the actual schema at startup (plus
MIG-030). Generator lands after milestone 6, which builds the introspection it
needs.

### ADR-0013 addendum 2 — long-history mitigation: shadow caching and squash (2026-08-29)

The owner: "when the migration has been too much, it is impossible to fight it
against shadow db." Two planned generator-era features answer it: (1) **shadow
caching** — the shadow SQLite file is cached keyed by the (version, checksum)
hash-chain it embodies (sound because applied migrations are frozen); generation
replays only migrations added since the cached prefix, making cost proportional to
recent deltas, not total history. (2) **squash** — `squash --to N` introspects the
shadow at N and emits one per-object literal baseline version declaring the range
it replaces: fresh databases apply the baseline and continue from N+1; databases
already past N recognize it and rewrite their history rows once; a database midway
inside the squashed range is a loud error (squash deliberately, after all
environments pass N). Pre-N files become deletable. Both land with the diff
generator (post-milestone 6).

### ADR-0013 addendum 3 — per-table snapshots; force sync as gated repair (2026-08-29)

Owner refinements to the generator/apply design:

- **Generate:** each table''s migration folder carries a generated, versioned schema
  snapshot (`Migrations/Table/<Object>/schema.json`, "as of version N") updated when
  the generator emits a migration for that table; diffing is metadata vs snapshot —
  no replay, no database, and per-table snapshots localize merge conflicts (unlike
  EF''s single ModelSnapshot). Views/statements/procedures are **self-reflecting**
  (defining SQL lives in the attribute): their diff is a stored hash of the last
  migrated SQL → a RecreateView step when changed. The **shadow database becomes a
  manual tool**: `shadow --from V0001|V<N>` replays history to verify or regenerate
  snapshots.
- **Apply:** after all migrations complete, the real database is compared against
  the final model. Residual difference (unmanaged drift) is an **error by default**,
  listing the differences; `--force` reconciles it automatically — sync runs only
  AFTER migrations, never instead of them.
- **Force sync scope:** default is purely additive (create missing
  tables/columns/indexes/views). Destructive or reshaping changes (drops; SQLite
  type changes = table rebuild) require `--allow-delete` (default false — "delete is
  rather fearful"). Sync never infers renames at any setting; renames are
  migrations-only. On validation failure the developer''s exits are: revise
  migrations, or force sync.

Error codes for sync refusal/reporting are registered when this is implemented
(post-milestone 6, with the generator).

## ADR-0014 — CRUD completion: update/delete semantics (2026-08-29)

**Decisions finalized with milestone 7:**

- **`InsertAsync` returns `Task`** (final answer to the §6 "returns id" sketch,
  deferred by ADR-0011): the generated key is written onto the entity — one rule
  that works for int64, GUID, and composite strategies alike. §6 updated.
- **`UpdateAsync(entity)`** writes every mapped non-key column by key (§7.15).
  With a `[Version]` column: `SET … , version = version + 1 WHERE key AND
  version = @old`; zero rows throws `ConcurrencyException` (`CRUD-010`); on success
  the entity''s version is bumped in memory so sequential updates keep working.
  Without one, zero rows is `CRUD-001` (strict: silently updating nothing is a bug).
  Navigation/FK consistency (`CRUD-004`) and read-only guards (`CRUD-003`) apply as
  on insert.
- **`DeleteAsync<T>(keyOrEntity)`** — one method, runtime dispatch: a key (or
  tuple, ADR-0006 validation) deletes by key, zero rows `CRUD-001`; passing the
  entity gives the version-checked form (`CRUD-010` when stale). §7.16''s "same on
  delete" made concrete.
- `ConcurrencyException` is its own public type carrying `CRUD-010` (§7.16 names
  it), distinct from `SimpleOrmException` so callers can catch the
  reload-and-retry case specifically.
- Client-GUID keys proven end to end (empty GUID assigned on insert).
- Conformance gains `crud-cases/`: step scripts with snapshots (`as`/`from`) and
  `$last` keys so stale-version conflicts are expressible as data; the lifecycle
  and concurrency cases run through the same generated paths every port must match.

**Status.** Accepted.

## ADR-0015 — Milestone 8 performance pass (2026-08-29)

**Work.** (1) The compiled mapper gained **typed-getter fast paths**: provider-native
types (int16/32/64, string, double, float, bool, decimal, Guid) compile to direct
`GetInt64`/`GetString`/… calls — no boxing, no converter dispatch — wrapped in a
compiled try/catch so conversion failures still carry `MAP-031` ("errors name
things" holds on the fast path). Types with rules stay on the converter path by
design: DateTime/Offset (the VAL-020 UTC rule), enums, and handler-registered types
(checked per plan). (2) **Direct list materialization**: one shared loop replaces
per-row async-iterator machinery; the reader opens async, rows read synchronously —
the SQLite provider is synchronous underneath (ADR-0003) — with cooperative
cancellation checked every 64 rows. `StreamAsync` keeps true async streaming.
(3) `DbBatch` (§8.8''s example) was evaluated and **not adopted**: Level 1 has no
multi-statement write path to batch; it becomes relevant with the Level 3 unit of
work.

**Results** (BenchmarkDotNet ShortRun, net10.0, this machine; benchmarks/ project —
the milestone-8-only Dapper/BenchmarkDotNet dependencies live there):

| benchmark | SimpleOrm | Dapper | ratio |
|---|---|---|---|
| 1000 rows → entities | 870.6 µs / 166 KB | 893.5 µs / 213 KB | **0.97, −22% alloc** |
| single row, GetAsync | 69.7 µs | 69.4 µs | 1.004 |
| single row, QuerySingleAsync | 71.1 µs | 69.4 µs | 1.024 |
| raw reader loop (floor) | 666.2 µs / 165 KB | — | 0.75 |

The §8.8 target (within 10% of Dapper on net10.0) is met on every benchmark;
the 1000-row case is faster than Dapper with allocations at the raw-reader floor.

**Status.** Accepted. Milestone 8 complete — all eight Level 1 milestones done.

## ADR-0016 — Repository in core; versioned snapshots realized; downs are derived (2026-08-29)

Four owner directives in one pass:

- **A real change migration in the sample**: `V0002` adds `users.display_name`
  (literal `AddColumn` + `.Post` backfill), which forced the ADR-0013 freeze rule
  into practice — `V0001_CreateUsers` is now literal SQL, because a
  metadata-rendered create is only stable while the object never changes again.
  The strict-mapping ripple (every hand-written `select … from users` needed the
  new column or failed `MAP-002`) is the §7.7 contract demonstrating itself.
- **`Repository<TEntity>` moves into the core** (was the sample''s `BaseDao`):
  generic Insert/Update/Delete/Get/GetOrDefault/GetAll/Find/Query over one injected
  session — boilerplate every app rewrote. Named `Repository`, deliberately not
  `DbContext` (EF''s word for the *session*, which is `Db` here) and not a
  `DbSet`-style property facade (rejected earlier). The sample''s layer is now
  `Repositories/` with per-entity subclasses.
- **Versioned schema snapshots implemented** (ADR-0013 add.3 made real):
  `simpleorm snapshot --out <MigrationsDir>` writes
  `Table/<Object>/V000N.schema.json` — object, `asOfVersion` (last version touching
  the object), `generatedAt` (ISO-8601 UTC), columns, indexes — one file **per
  (table, version)** so version-to-version diffs have endpoints. Tables only;
  views/statements/procedures self-reflect. The sample commits User V0001 (its
  historical shape) and V0002 plus V0001 for the other tables.
- **No hand-written `Down()` DDL** (owner): a rollback''s DDL derives from
  diffing adjacent snapshots once the generator exists; `Down()` stays as the
  manual escape hatch and generator target, and new step-level `PreDown`/`PostDown`
  hooks carry the underivable data work (stash before a destructive revert,
  restore after) — reversibility is judged on the down *core* only, so hooks alone
  still refuse `migrate down` (`MIG-020`). Sample migrations carry no downs;
  refusing honestly is the designed behavior until generation lands.

**Status.** Accepted.

## ADR-0017 — The generator toolchain: diff, shadow, force sync, derived downs (2026-08-29)

The migration-authoring toolchain designed in ADR-0013's addenda, built. The model
is the final truth; snapshots are the recorded past; the diff between them is the
next migration. Four pieces:

- **Snapshot format v2 (storage types, name-sorted).** `V000N.schema.json` columns
  now carry the dialect **storage type** (`TEXT`/`INTEGER`/`REAL`/`BLOB`, via the
  new `IDialect.StorageType`) instead of neutral CLR tokens, plus `key`/`generated`
  flags; columns and indexes are name-sorted. Rationale: storage types are what
  CREATE TABLE emits and what `pragma table_info` reports, so the same file is
  (a) directly renderable as DDL (derived downs, trusted baselines) and
  (b) byte-comparable with database introspection — the metadata-side
  `snapshot` command and the introspection-side `shadow` command must produce
  identical files, and the test suite asserts they do. `TableSchema` is the shared
  model; `SchemaSnapshot.Export/Parse` own the format.
- **`simpleorm diff`** (`MigrationGenerator` in core): compares each table's
  metadata against its latest committed snapshot — **no database involved** — and
  emits ordinary migration source: a per-object step with literal SQL actions and
  a **generated `Down()`** derived from the snapshot, plus the root `V000N` class
  (new tables FK-ordered). Generated code is indistinguishable from hand-written
  code and freezes the same way; nothing special is recorded. Renames are declared
  (`--rename table.old=new`, repeatable), **never inferred**. Removals require
  `--allow-remove` (`DDL-003`). Type/nullability changes and non-nullable
  additions are refused as `DDL-004` — write those by hand (the `AddColumn`
  default/backfill path exists for exactly that). Views are excluded: their
  definitions self-reflect; view changes stay hand-authored (`RecreateView`).
- **`simpleorm shadow`** (`SqliteShadow` in the SQLite package): rebuilds
  snapshots from *history* rather than from the model — replays the versions into
  a throwaway temp database, introspects the touched tables after each version
  (`pragma table_info` / `index_list` origin `c` / `index_xinfo`), and writes the
  per-version files. Full-rebuild equality with the committed snapshots is the
  proof that snapshots derive from migrations. **Range form (owner):**
  `--from V000N [--to V000M]` trusts version N as correct — the baseline state is
  reconstructed from the committed snapshots at ≤ N (rendered straight to DDL),
  versions ≤ N are baselined in the shadow's version table and **never verified**,
  and only (N, M] replays and re-snapshots. This is the "migration history has
  grown too long to fight" escape valve, and the mechanism a future squash reuses.
  The rowid-alias key (`INTEGER PRIMARY KEY`, single pk) introspects as
  `key`+`generated`; composite-key order is not recorded (name-sorted), which DDL
  reconstruction tolerates because SQLite doesn't care.
- **`simpleorm migrate --force`** (`SchemaSync` in core): after migrations apply,
  compares the live schema against the model and syncs the difference. Additive
  fixes (missing table, missing nullable column) apply immediately; deletions
  (extra columns) only with `--allow-delete` (`DDL-003`, off by default — "delete
  is rather fearful"); type/nullability mismatches and non-nullable additions are
  reported as `DDL-004` and never auto-applied. Renames are never inferred here
  either — an undeclared rename syncs as add + (gated) drop.

First live run: the shadow rebuild exposed real drift the metadata-side snapshot
command had papered over — `Role` declares `ix_roles_role_name` (unique) but no
migration ever created it. `diff` then authored `V0008_AddRoleNameIndex`
(committed in the sample) and converged: a second `diff` reports no changes,
SchemaGuard validates clean. New codes: `DDL-003`, `DDL-004`. CLI grew repeated
and boolean flag parsing (`--rename` ×N, `--force`, `--allow-delete`,
`--allow-remove`).

**Status.** Accepted.

### ADR-0017 addendum 1 — views, materialized views, and procedures snapshot by DDL; the MIG-012 apply guard (2026-08-29)

Owner: view, MV, and procedure get the same `schema.json` approach as tables —
"the differences is we don't compare the column, we compare the ddl" — and, at
apply time, "if the previous schema is the expected, we continue to apply, but if
the previous schema is different, we notify, only when force then view is
recreated. This is because sometimes view is adjusted outside the code due to
urgency."

- **DDL-shaped snapshots.** `View|MaterializedView|Procedure/<Object>/V000N.schema.json`
  holds `{ object, kind, asOfVersion, generatedAt, ddl }`. The DDL is normalized —
  whitespace collapsed, and the create-view prefix canonicalized to lowercase
  `create [materialized] view <name> as` — because databases rewrite that prefix
  when storing it (SQLite drops `IF NOT EXISTS` and recases `CREATE VIEW`), and
  the rendered and introspected producers must still agree byte-for-byte. The
  normalized form stays executable: baseline restores run it directly. Both
  producers exist for views (metadata renders `CreateViewSql`; shadow reads
  `sqlite_master` via the new `IDialect.ViewDefinitionSql`); MV and procedure use
  the same format but are capability-gated like everything else about them —
  dormant on SQLite, alive when a Level 4 dialect brings the flags (and, for
  procedure *steps*, a `ProcedureMigration` type plus a create-procedure render,
  added to `IDialect` only when that dialect exists, per §7.25).
- **Diff by DDL.** `simpleorm diff` compares the normalized current definition
  against the latest DDL snapshot: missing → create step, different → change step.
  Emitted view steps are **literal DDL** (never `CreateView()`/`RecreateView()`,
  which render from current metadata and would drift the checksum of an applied
  step the next time the definition changes). The generated Down restores the
  previous snapshot's definition verbatim — derived downs now cover views. View
  steps compose after table steps in the generated root (§7.22 ordering).
- **The `ExpectDefinition` guard (`MIG-012`).** A generated change step opens with
  `actions.ExpectDefinition(<previous ddl>)` — a precondition, not SQL. When the
  step applies, the runner reads the live definition and compares (normalized):
  match → continue; mismatch or absent → the run refuses with `MIG-012` naming the
  view ("changed outside migrations; review the drift, then rerun with --force"),
  and since the whole run is one transaction, nothing applies — the urgency hotfix
  in the database is left untouched. `migrate --force` (runner:
  `MigrateAsync(allowViewDrift, notify, ct)`) recreates over the drift and reports
  it through the notify channel. Downs carry the mirrored guard (expecting the
  step's own definition). Guards are evaluated at execution time, not during
  pre-run validation, so two pending view changes in one run chain correctly; they
  do count into the step checksum (the expectation is part of the step's
  identity). Hand-written steps may call `ExpectDefinition` too; steps without it
  behave as before.
- Also in this pass: the sample's view step folder renamed
  `View/UserTransactionTotals` → `View/UserTransactionTotal` (snapshot and
  generated-step folders key off the *type* name, as tables always did; checksums
  cover rendered SQL only, so the namespace rename is safe), and the single-line
  SQL emitters' C#-string escaping fixed (`\"`, not the verbatim `""`).

**Status.** Accepted.

### ADR-0017 addendum 2 — indexes match structurally, never by name (2026-08-29)

Owner: "checking index should be focused more on 'what the indexed column' rather
than focus comparing the name. In reality, index mostly need to be added directly
to the database rather than waiting for deployment. If the index already exists
although the name is different, we can count that as implemented."

An index's identity is its **signature**: the unique flag plus the ordered
(column, direction) list. Names are labels. Both comparison sites now use it:

- **`simpleorm diff`** (model vs snapshot): a model index whose signature exists
  in the snapshot under any name is implemented — no step emitted; a snapshot
  index with no model signature is a removal (dropped by its actual name, still
  gated by `--allow-remove`). A pure index rename in the model is a no-op.
- **`migrate --force` sync** (model vs live database) now checks indexes on
  existing tables — previously only table-create brought indexes along. Live
  indexes are introspected through the new `IDialect.IndexesInfoSql` (created
  indexes only — key columns, direction, uniqueness; on SQLite
  `pragma_index_list` origin `c` joined with `pragma_index_xinfo`). A model index
  structurally missing is additive (created immediately — this is the urgency
  case in reverse); a live index matching no model signature is a gated deletion;
  a live index matching under a different name counts as implemented and is left
  alone, name and all.

Uniqueness is part of the structure, deliberately: a unique index found where the
model wants a plain one (or vice versa) is a different object — the constraint
semantics differ — so it diffs as add + gated remove, never as a silent match.

**Status.** Accepted.

## ADR-0018 — Rollbacks derive at runtime from the snapshots; nobody writes Down (2026-08-29)

Owner, rejecting the plan to backfill (or keep generating) `Down()` bodies:
"i don't like adding down to each migration. Down could be deducted from the
previous schema." ADR-0016 said downs derive from snapshots; ADR-0017 realized
that at *authoring* time by generating `Down()` into steps. This goes the rest of
the way: **no step carries down DDL at all** — the runner deduces it at
`migrate down` time from the versioned snapshots.

- **Derivation.** For each step being reverted, the runner resolves the object's
  snapshot at that version and the latest one before it (creation when none).
  Tables: the one thing two shapes cannot reveal is whether a column moved or was
  replaced — a rename and a drop+add look identical — so the step's **typed
  `RenameColumn` actions** are inverted first, data-preservingly, and the
  remaining shape diff derives the rest: dropped columns restored (a NOT NULL
  restore lands nullable with a notice — the constraint and the data are not
  derivable; hooks carry data), added columns dropped, indexes reverted
  structurally (add.2). Views: expected-definition guard on the current DDL
  (MIG-012 — a rollback must not silently destroy an outside hotfix either), drop,
  previous definition restored. A same-column type/nullability change is not
  derivable → `MIG-020`, hand-write `Down()`. Data-only steps derive an empty
  rollback (schema unchanged is the correct structural answer); their data work
  belongs to `PreDown`/`PostDown` — the sample's V0005 seed now demonstrates it
  (its re-seed after a hookless rollback tripped V0008's unique index; the
  `PreDown` delete closes the cycle).
- **Precedence.** A hand-written `Down()` is the manual override and always wins;
  `PreDown`/`PostDown` hooks wrap whichever core runs. The whole reverting range
  resolves before anything executes (§7.23); `MIG-020` now means "no snapshot to
  derive from and no override" rather than "no down file".
- **Snapshots travel with the assembly.** The runner needs history at rollback
  time, deployed or not, so `Migrations/**/*.schema.json` are **embedded
  resources** (one csproj line; `SnapshotSet.FromAssembly` reads them,
  `--snapshots <dir>` overrides from source). `SnapshotDdl` (create table/index
  from a snapshot) moved into the core — the shadow's trusted baseline and the
  deriver share it.
- **The generator emits no `Down()` anymore** (table or view steps; the sample's
  V0008 was stripped accordingly). Its diff logic and the deriver are the same
  algorithm at two moments; the runtime one wins because it needs no regeneration
  when snapshots change and it keeps migrations pure Up.
- Also: migration statement failures now name the failing SQL (`MIG-021`) —
  found the hard way while debugging the deriver's rename handling.

Proven end-to-end: the sample's full 8-version history migrates up, down to zero,
and up again — with not a single `Down()` in the codebase.

**Status.** Accepted. Supersedes ADR-0017's generated-`Down()` emission.

## ADR-0019 — Level 2 begins: milestone plan; M1 = relationship metadata (2026-08-29)

Owner: "go level 2 first" (chosen over the Go port; the port follows Level 2).
Level 2 scope from the brief (§11): relationship metadata, explicit → batch →
eager loading, graph reshaping with per-result identity, the query AST and its
dialect renderer, explicit null semantics, a fluent front-end, dynamic
composition. Milestones, one at a time, stop and report — same discipline as §8:

1. **Relationship metadata.** `[OneToMany]` and `[ManyToMany]` declarations join
   `[ManyToOne]`/`[ForeignKey]`; `RelationshipMap` gains a kind and link info;
   JSON export + conformance entities + spec/metadata-model.md. Declaration-only:
   nothing loads yet. Owned types are NOT in M1 — they land later in Level 2 once
   loading exists to give them meaning.
2. **Query AST + renderer + null semantics.** The ADR-0012 criteria core
   formalized as the Level 2 AST, every SQL string rendered by the dialect;
   explicit null rules; dynamic composition documented; `spec/query-ast.md` +
   `conformance/ast/`. Also closes the Level 1 spec debt (session/CRUD/criteria
   documents) since this milestone rewrites that ground anyway.
3. **Explicit + batch loading.** `LoadAsync` for navigations — one visible round
   trip per call; the batch form loads a navigation for N entities in one IN
   query. No hidden queries, no lazy proxies (the §2 principle holds: Level 2's
   answer is that no implicit form exists).
4. **Eager loading + graph reshaping.** Join-based includes through the AST;
   reshaping joined rows into graphs with per-result identity (§7.4 key
   equality); the json_group_array path stays supported.
5. **Fluent front-end.** Typed lambda → AST for the criteria surface; LINQ
   provider explicitly out (later). String-based Criteria stays first-class.
6. **Level 2 exit.** Spec + conformance completeness; exit criteria mirror
   Level 1's ("reimplementable from spec/ + conformance/ alone").

**M1 decisions:**

- `[OneToMany(nameof(Target.FkProperty))]` on a collection navigation: the
  foreign key lives on the target, named by its property. `[ManyToMany(typeof(Link))]`
  names the link entity explicitly — never inferred; the link's `[ForeignKey]`
  declarations (ADR-0005) resolve which of its properties reference each side,
  and must do so exactly once per side.
- Collection navigations follow ADR-0005 add.2: no public setter (`MAP-011` —
  the library is the only writer), initialized empty
  (`{ get; private set; } = [];`), transient — never a column, never written.
- Element type comes from the property's `IEnumerable<T>` with a single entity
  `T`; anything else is `MAP-020`. An unknown target FK property is `MAP-021`;
  a link that misses or ambiguously references a side is `MAP-022`.
- Relationships stay attribute-declared only; `EntityMapBuilder` parity waits
  for a demonstrated need (no abstractions for the future, §13).
- JSON export: `one_to_many` carries `references` + `targetForeignKeyProperty`
  (property name — target column names belong to the target's own export);
  `many_to_many` carries `references`, `through`, and the two link FK property
  names. `many_to_one` is unchanged.

**Status.** Accepted.

### ADR-0019 addendum 1 — one-to-one; composite foreign keys; polymorphic and "through" ruled out; loading defaults (2026-08-29)

Owner, on the relationship taxonomy comparison: "i dont like polymorphic, no for
that, as well as 'through'. ok to implement the rest. Remember that the default
is lazy, only when requested to eager then it is load automatically." And
mid-build: "don't forget about the composite key. I remember this could need
special implement."

- **`[OneToOne]`** completes the four classic cardinalities: the singular inverse
  of a many-to-one — same resolution as `[OneToMany]` (FK on the target, named by
  property), but the property is a single entity reference (`T?`), and a
  collection there is `MAP-020`. True 1:1 integrity is the database's job: the
  sample's `user_profiles` carries the unique index on `user_id`, and migration
  V0009 (authored by `simpleorm diff`, snapshotted by the trusted-range shadow)
  ships it.
- **Composite foreign keys.** Every FK declaration is a **list**, one entry per
  part of the referenced side's key, in that key's order:
  `[ManyToOne(nameof(UserId), nameof(RoleId))]` references a composite-key
  target; `[OneToMany]`/`[OneToOne]` list the target's FK properties in this
  entity's key order; a many-to-many link references a composite side with
  several `[ForeignKey]` properties, pairing in declaration order. Counts are
  validated against key arity wherever the key shape is declared ([Key]
  attributes) — mismatches are `MAP-016`/`MAP-021`/`MAP-022`; convention-mapped
  targets skip the arity check rather than guess. The `CRUD-004` write-time
  navigation/FK consistency check is now composite-aware (it silently skipped
  composite targets before). JSON export uses arrays
  (`foreignKeyColumns`, `targetForeignKeyProperties`, `linkForeignKeysToOwner`/
  `...ToTarget`).
- **Ruled out permanently** (owner): polymorphic relations (a type-name column
  instead of a real FK — no database integrity, against §2) and
  "through"-style traversal relations (Eloquent's hasManyThrough — plain SQL or
  a `[Statement]` says it better). Self-referential relationships need no
  special kind; owned types stay scheduled later in Level 2.
- **Loading contract for M3/M4** (owner): the default is unloaded — a navigation
  stays empty/null until loading is requested; requesting **eager** loads it
  automatically with the query. Within the §2 no-hidden-queries principle this
  is deferred-until-requested, not access-triggered: touching an unloaded
  navigation never fires SQL. Whether unloaded access should *throw* instead of
  returning empty (strictness vs. convenience) is an open M3 design question.

**Status.** Accepted.

## ADR-0020 — L2 M2: the query AST is rendered by the dialect; strict null semantics (2026-08-29)

Level 2 milestone 2 (owner authorized autonomous continuation). The ADR-0012
criteria core was already an AST as data; this milestone completes §10.4's
contract and fixes its null-semantics hole.

- **`SelectAst`** is the criteria query as data: source `EntityMap`, implicitly
  ANDed predicate list, orderings, limit/offset. Every front-end — today's
  string-based chain, M5's fluent front-end — produces this and never SQL text.
- **The dialect renders it**: new `IDialect.SelectSql(SelectAst, bindParameter)`.
  `AnsiSelectRenderer` (core) is the **reference rendering** — explicit column
  list, `@c0…` parameters in render order (WHERE, then limit, then offset),
  property→column resolution (`QRY-006`) — and a dialect normally delegates to
  it, overriding only where its SQL disagrees (SQLite: nothing beyond the
  existing limit/offset knob). This keeps a second dialect's cost near zero
  while satisfying "front-ends never emit SQL text".
- **Null semantics are explicit and strict** (the old renderer emitted
  `col = NULL` for `Eq(p, null)` — a silent-match-nothing bug by our own §2
  rules): `Eq(p, null)` renders `is null`, `Ne(p, null)` renders `is not null`
  (signatures now accept null); any ordered comparison with null, or a null
  element inside an IN list, is meaningless three-valued SQL and throws
  **`QRY-007`** with the IsNull/IsNotNull guidance; an empty IN list keeps
  rendering a visibly false predicate (`1 = 0`).
- **`conformance/ast/`** is born (the §9 folder reserved for Level 2): a JSON
  encoding of `SelectAst` (`op` vocabulary: eq/ne/gt/ge/lt/le/like/in/
  is_null/is_not_null/and/or/not) with the exact expected SQL and ordered
  parameter values per dialect, or an error code — rendering is pure, no
  database. Eight cases cover the kitchen sink, both null renders, both QRY-007
  refusals, empty IN, negation, and QRY-006.
- **Level 1 spec debt closed**: `spec/query-ast.md`, `spec/session.md`,
  `spec/crud.md` (authored in a parallel workflow, reviewed and edited in).
- GROUP BY stays deliberately absent (aggregations are `[Statement]` entities);
  joins arrive with M4's eager loading and will extend `SelectAst` rather than
  replace it.

**Status.** Accepted.

### ADR-0020 addendum 1 — the adversarial review pass: hardening decisions (2026-08-29)

M2 ran through a 39-agent review workflow (four lenses, every finding
adversarially verified); 22 findings survived refutation. The decisions they
forced:

- **Degenerate composites render identity truth-values**: empty `And()` → `1 = 1`
  (true), empty `Or()` → `1 = 0` (false) — dynamic composition legitimately
  produces empty lists, and the previous rendering (`()`) was invalid SQL failing
  with an unnamed provider error. Pinned by `empty_composites.json`.
- **`QRY-008`**: negative limit/offset refuses. SQLite silently treats a negative
  LIMIT as *no limit* — a page-arithmetic bug returning the whole table — and
  dialects disagree on the meaning, which the AST contract cannot tolerate.
- **The bind seam is property-aware** (`BindCriteriaParameter(value, property)`
  replaced `Func<object?, string>`): criteria values bound property-blind, so an
  enum against an `[EnumAsInt]` column bound as its *name* and silently matched
  nothing. Per-column conversion now applies to criteria exactly as to writes.
- **Property resolution refuses ambiguity**: exact-case match wins; a
  case-insensitive match fitting more than one property is `QRY-006` naming the
  candidates, never a silent first-wins (the MAP-003 precedent).
- **The `In` overload trap is closed**: `In(property, "Ada")` resolved to
  `In<char>` (string is `IEnumerable<char>`) and queried per character; a
  dedicated `(string, string)` overload makes a lone string a one-element list.
  Null IN lists (null array / null enumerable) now throw ArgumentNullException
  naming the property instead of an unnamed LINQ crash at render.
- **Code fixed to match spec, not vice versa**, in three places the reviewers
  caught the spec describing intent the code missed: IN-list expansion now
  rewrites only real placeholders (`SqlPlaceholders.Occurrences` over the masked
  SQL — lookalikes inside string literals/comments were being rewritten);
  `IDialect.SupportsArrayParameters` now exists (§7.12 promised it since ADR-0003;
  SQLite: false); `UpdateSql` excludes generated non-key columns (the binder
  already did — the SQL and the binding disagreed, so any entity with a
  database-owned non-key column could never update).
- **`MAP-019` grew a rule**: a database-generated key must be an integer type —
  `[Key][Generated] Guid` previously loaded as DatabaseGenerated and rendered
  `INTEGER PRIMARY KEY`, silently mismapped.
- The criteria command is disposed when rendering refuses mid-bind (QRY-006/7/8
  after parameters landed on the live command).
- Spec corrections: the null-semantics guarantee scoped to null *literals in the
  tree* (NULL column data evaluates by standard SQL); operator tokens pinned
  (`ne` renders `<>`, never `!=`) with `comparison_operators.json`; session.md's
  conformance section describes what exists (a params-carrying case format for
  `PRM-001/002` is reserved, like `ast/` was); crud.md's update contract and
  key-requirement wording aligned with the code.
- New conformance/ast cases: comparison operators (+ explicit `and` node +
  case-insensitive property), empty composites, limit-only, offset-only
  (SQLite's `limit -1` idiom), negative-limit `QRY-008`, like-null `QRY-007`,
  orderBy `QRY-006`. The ast runner is strict about order tokens.

**Status.** Accepted.

## ADR-0021 — L2 M3: explicit and batch loading; nothing loads implicitly (2026-08-29)

Level 2 milestone 3, on the ADR-0019 add.1 contract (owner): the default is
unloaded; loading happens when requested; never on access.

- **Two calls, one contract**: `LoadAsync(entity, nameof(T.Nav), ct)` and the
  batch form `LoadEachAsync(entities, nameof(T.Nav), ct)` — distinct names
  because a single-method overload pair falls into the identity-beats-interface
  overload trap (a `List<T>` binds to the single-entity overload as its own
  TEntity). String-named navigations match the criteria surface; the M5 fluent
  front-end adds lambdas over the same core.
- **Visible, bounded round trips** (§2): one criteria query per navigation per
  call — many-to-many is exactly two (link rows, then targets) — chunked at 500
  owners per query only for the parameter budget, never one query per entity.
  All SQL goes through the SelectAst pipeline (ADR-0020): explicit columns,
  parameterized, dialect-rendered, ordered by the target key for determinism.
- **Kinds**: many-to-one fills from the owner's FK tuple (owners sharing a
  target share the loaded instance within one call; a null FK part leaves the
  navigation null); one-to-many fills a fresh list per owner (empty, never
  null); one-to-one fills a single instance or null — **more than one matching
  row is `REL-002`** (the unique index is what makes a 1:1; drift is refused,
  not resolved silently); many-to-many resolves the declared link's FK pairs and
  orders by target key. Composite keys ride the same paths via OR-of-ANDed
  equality tuples.
- **Errors**: `REL-001` (not a declared navigation — the message lists what is),
  `REL-002` (above), `REL-003` (shape disagreements the declaration-time loader
  could not validate: unmapped FK properties on the target/link, arity vs. an
  undeclared key). New `REL-` family in errors.md.
- **Unloaded access stays silent-empty for now**: a navigation reads as its
  initialized empty/null until loaded. The strict alternative (throwing on
  unloaded access) remains the open owner question from ADR-0019 add.1 —
  reading it is not I/O, so nothing hidden happens either way.
- **Conformance**: `conformance/load-cases/*.json` — owner keys in, loaded
  values out (object/null for singular, target-key-ordered arrays for
  collections; listed columns checked, lengths exact), or an error code. The
  fixture seed grew roles/links/one profile (additively — no existing case
  reads those tables); the case database builder is shared plumbing now
  (`ConformanceDatabase`).

**Status.** Accepted.

### ADR-0021 addendum 1 — the loading review pass: structural identity, value-wise order (2026-08-29)

M3 ran through a 29-agent adversarial review (three lenses, every finding
refuted or confirmed). The confirmed findings and decisions:

- **Key/FK tuples match by structural value equality, never string tokens.**
  The first implementation tokenized tuples with `Convert.ToString`, which is
  lossy: DateTime keys lose fractional seconds (two distinct keys, one token —
  live-reproduced loading the *wrong entity* silently) and every `byte[]` key
  stringifies as `System.Byte[]`. Replaced by a tuple comparer implementing the
  §7.4 identity rule (element-wise equality; `byte[]` by content — which
  `EntityMap.KeysEqual` itself gets wrong for blobs, a pre-existing nit).
- **Collections order by the target key compared as values** — the token sort
  put `10` before `2`, violating the spec, and the test suite masked it by
  re-sorting before asserting. Fixed; the unit test now uses ids across a
  digit-length boundary asserted unsorted, and the conformance case seeds role
  id 10 so any port with the string bug diverges.
- **The empty batch validates too**: `REL-001` fires for a wrong navigation name
  regardless of list size (previously an empty list short-circuited validation).
- **Owners with a null key part are excluded from querying** (navigations stay
  empty/null), symmetric with the many-to-one null-FK rule — previously a null
  key part leaked into the IN list and failed with a misdirected `QRY-007`.
- **Dangling many-to-many links are skipped, deliberately**: a link row whose
  target row is gone contributes nothing, exactly as a join would — referential
  integrity is the database's story. Documented in spec/loading.md rather than
  invented as an error.
- **Repository parity**: `LoadAsync`/`LoadEachAsync` pass-throughs.
- **Conformance format grew composite owner keys** (arrays of parts in key
  order; `|`-joined in `loaded`) with a UserRole case; `REL-003` gained a unit
  test (an `[Ignore]`d target FK property — declared-time check passes, load
  refuses); `REL-002`/`REL-003` are documented as implementation-test territory
  (the case format cannot seed drifted data or shape-broken metadata).

**Status.** Accepted.

## ADR-0022 — L2 M4: eager loading is multi-query, not joins (2026-08-29)

ADR-0019 sketched M4 as "join-based includes through the AST; reshaping joined
rows with per-result identity". Building M3 changed the calculus, and per §13
the change is recorded rather than silently made:

- **Eager loading ships as `Include(params string[])` on the criteria chain**:
  the root query runs, then **one batch load per included navigation** (the M3
  machinery — `LoadEachAsync` — with its structural identity, target-key
  ordering, chunking, and REL codes). `SingleAsync`/`SingleOrDefaultAsync`
  eager-load their one row the same way. An unknown navigation is `REL-001`
  even when the query matches no rows. This is the "requested eagerly, loaded
  automatically with the query" contract of ADR-0019 add.1, with every round
  trip still visible and countable: 1 + one per navigation (many-to-many: two).
- **Why not joins now.** (1) Paging: a JOIN multiplies root rows, so
  `Limit`/`Offset` on the joined set limits child rows, not roots — the classic
  ORM bug; multi-query pages correctly by construction. (2) Per-result identity
  falls out of M3's key-tuple maps instead of needing joined-row deduplication.
  (3) It is Eloquent's actual strategy (`with()` runs separate queries), the
  ergonomic reference the owner has favored throughout. (4) The join machinery
  earns its complexity only when criteria can *filter on related data* — a
  front-end feature no milestone needs yet.
- **What this defers, explicitly**: joins in `SelectAst` and joined-row graph
  reshaping move out of the Level 2 exit criteria; they land when
  filtering-on-related arrives (Level 2 extension or Level 3), and the
  json_group_array nesting path (§7.10) remains the single-round-trip option
  meanwhile. **Open for the owner**: whether a join-based single-query include
  mode is still wanted as an alternative (EF offers both as "single vs. split
  query"); the AST was left extensible for it.
- Conformance: eager loading composes two already-pinned primitives (the
  criteria query, `ast/`; batch loading, `load-cases/`), so it is pinned by
  implementation tests rather than a third case family duplicating both.

**Status.** Accepted. Amends ADR-0019's M4 sketch.

### ADR-0021 addendum 2 — unloaded access throws; dead links are null; lambdas are per-language sugar (2026-08-29)

Owner rulings on the three open questions:

- **Lambda front-end (M5)**: approved as an *addition*, explicitly per-language
  ("exclusive for C#… Java also start implementing lambda") — the portable
  contract stays the string/AST criteria core; each implementation may layer its
  language's lambda idiom over it. LINQ-the-provider remains out.
- **Unloaded access throws** — "it should throw error except it is really
  null." Implemented where the language allows interception: an entity **read
  from the database** gets its collection navigations set to a guard list that
  throws `REL-004` on any access (Count, index, enumeration); loading —
  explicit, batch, or eager — replaces the guard with the real list, and a
  loaded-but-empty collection reads as empty. Entities constructed by user code
  keep their own initializers (a new entity genuinely has nothing). Singular
  navigations **cannot** throw on read without proxies (§2 forbids), wrappers
  (`tx.User.Value` — an API-shape change the owner would need to choose), or
  source generators (Level 4): they stay null until loaded. The guard is
  installed by every materialization path (queries, streams, key reads,
  criteria) via a compiled per-type marker — one delegate call per row, only
  for types that declare collection navigations.
- **Dead links load as null** — "the foreign key is there, but it is a dead
  link, it will return null since there is no real Model to go there." Loading
  a many-to-one/one-to-one whose FK points at no row resolves the navigation to
  null, not an error; `REL-004` is strictly about *not having loaded*. Pinned
  by a live test (FK intact, target row deleted, load → null).

**Status.** Accepted.

### ADR-0022 addendum 1 — fetch modes: eager loading is configurable (2026-08-29)

Owner, on whether join includes are still wanted: "can it be configurable? i
think it will depends on the need. As a developer, at some point i tend to find
which one is faster, at other point, which one is more efficient" — after the
Hibernate walkthrough. So `Include` gains `Fetch(FetchMode)`; the modes must
load **identical graphs** and differ only in round trips and data shape:

- **`MultiQuery` (default)**: root query + one batched key-list query per
  navigation. No duplicated data, paging always correct — the ADR-0022 baseline.
- **`SubSelect`** (the Hibernate idea worth stealing): each navigation query
  filters by `IN (select … from the root query)` — the root's where, orderings,
  and paging ride into the subquery, so it pages correctly, never chunks, and
  stays one query per navigation regardless of root count. Composite keys render
  as row-value IN. Mechanics: the AST grew an internal `InSelect` node and a
  `Projection` (the subquery selects only the FK/key columns); no front-end or
  conformance encoding exposes them yet.
- **`Join`**: one SELECT with LEFT JOINs — fewest round trips ("faster" when
  latency dominates). The AST grew `SelectJoin` (alias, ON pairs, projected or
  not — a many-to-many's link joins unprojected); the reference renderer aliases
  the root `t` and joins `j0…`, columns as `alias_column`. Each row partitions
  into **segment readers** — a column-window `DbDataReader` decorator — so every
  entity segment materializes through the **one mapping pipeline** (§7.11),
  unchanged. Roots deduplicate by §7.4 identity; children attach with the same
  semantics as the multi-query path (shared instances, `REL-002` on one-to-one
  duplicates, collections sorted value-wise by target key). Strict where
  Hibernate famously is not: **`REL-005`** refuses join + limit/offset (never
  HHH000104-style in-memory paging) and **`REL-006`** refuses joining two
  collection navigations (never a silent Cartesian product) — one collection
  plus any number of to-one navigations is fine.
- Conformance: the four kind cases in `load-cases/` carry `"viaQuery": true` —
  the runner replays each through `Include` under **all three modes** against
  the same expectations, so a port whose modes diverge fails the suite.
- The unloaded-navigation guard (`REL-004`) composes: join-loaded roots guard
  their non-included collections exactly like every other materialized entity.

**Status.** Accepted.

### ADR-0022 addendum 2 — the fetch-modes review pass (2026-08-29)

A 24-agent adversarial review of the fetch-modes feature; the confirmed
findings and decisions:

- **Join-loaded children guard too**: child entities materialized by join mode
  never received the `REL-004` unloaded-collection sentinel — their own
  navigations read as silently empty, the exact bug the guard exists to catch
  (live-reproduced). Each attachment now marks freshly materialized children.
- **Single-navigation joins count raw rows**: the per-root identity dedup
  existed to cancel cross-navigation fan-out, but with one included navigation
  there is no fan-out — and the dedup was swallowing genuine duplicate-key
  source rows and masking `REL-002` (live-reproduced with a drifted 1:1).
  Raw counting now applies with one navigation (full mode parity, pinned across
  all three modes); with several, dedup stays and the residual — a same-key
  duplicate source row is indistinguishable from fan-out — is documented, with
  MultiQuery as the escape.
- **Keyless views refuse with a name**: a keyless root or target crashed join
  mode with a raw InvalidOperationException (and could materialize a phantom
  child from an all-NULL LEFT JOIN row); now `REL-003` at plan time, before any
  SQL. MultiQuery remains the mode that serves keyless views.
- **`REL-005` narrowed to what is actually unsound**: to-one joins never
  multiply root rows (they join the full target key), so paging with
  to-one-only includes now works in join mode; only a collection include
  refuses.
- **SubSelect paging made deterministic**: a paged subselect re-evaluates the
  root, so a non-total ordering could pick a *different* page in the two
  evaluations; the paged root now gains key-tiebroken ordering applied to both
  the root execution and every subquery (the criteria path was refactored onto
  one `ExecuteAstAsync`, retiring the string-building callback). Subquery
  orderings are dropped when unpaged (dead weight). Docs corrected: the
  many-to-many link→target hop still key-lists client-side.
- **Client-side key ordering is ordinal for strings** — `Comparer<string>`
  is culture-sensitive and diverged from SQLite's BINARY `ORDER BY`
  (live-reproduced); `CompareKeyTuples` now compares strings ordinally.
- Includes validate (`REL-001`) before any SQL in every mode; the reflection
  child-plan call unwraps TargetInvocationException so mapping codes surface;
  the conformance viaQuery replay asserts owner-set completeness (count and
  key set), closing its silently-passing gap.

**Status.** Accepted.

## ADR-0023 — Roadmap rulings: dialect order, port order, M5 deferred; field-testing begins (2026-08-30)

Owner rulings closing this development stretch:

- **Level 4 dialect order: SQL Server first, PostgreSQL second.** The owner will
  field-test SimpleOrm against Fidelis (their real inventory/sales domain) on a
  SQL Server machine (not this computer). Known seam-stress points to expect
  when that dialect starts: `RETURNING` → `OUTPUT`/`SCOPE_IDENTITY`, no
  row-value `IN` (the SubSelect composite rendering already documents the
  dialect override), `TOP`/`OFFSET-FETCH` paging, `sp_getapplock` for the
  migration run lock, no STRICT tables (the strict-by-default story needs a
  SQL Server answer), and a different storage-type table for snapshots/diff.
- **Port order: PHP first, then — if it proves out — Go, then Rust.** This
  supersedes §12's "Go first" ordering: PHP aligns with the owner's real
  codebase (Fidelis), so the first port doubles as field validation. Go and
  Rust keep their role as the spec-neutrality stress tests (Rust is new to the
  list; Java drops out of the near-term order). The §12 rules stand: ports
  share no code, consume `spec/` + `conformance/` unchanged, and no port starts
  before the current level's exit criteria hold.
- **M5 (lambda front-end) is skipped for now** — "my focus is testing the ORM
  for basic need first." It remains approved as a per-language addition
  (ADR-0021 add.2) and can start any time on an owner sample.
- **Where this leaves Level 2**: M1–M4 done (relationships, AST + renderer,
  loading, configurable eager fetch); M5 deferred; M6 (exit audit +
  benchmark refresh) pending. Next session continues from here — this ADR,
  CLAUDE.md §7b/§11/§12, and the git log are the state.

**Status.** Accepted.

## ADR-0024 — SQL Server dialect pulled forward from Level 4 (2026-08-30)

Owner: "create dialect for the sqlserver". Additional dialects are §3 Level 4,
but ADR-0023 already ruled SQL Server first *and* started the Fidelis field test
on a SQL Server machine — a field test the dialect is a precondition for. So the
first Level 4 dialect ships now, explicitly **runtime-first**: everything the
session, criteria/loading pipeline, SchemaGuard introspection, and the migration
runner need; the snapshot/diff tooling stays SQLite-only for the moment (below).
`SimpleOrm.SqlServer` (netstandard2.0;net10.0) references Microsoft.Data.SqlClient
— conditionally per TFM (5.1.6 on netstandard2.0, 6.1.1 on net10.0; SqlClient
dropped netstandard after 5.x), the §4 escape hatch used for the first time.

**The seam grew members** (§7.25's rule — add only when a second dialect needs
them; every SQLite implementation returns today's strings byte-for-byte, so
conformance pins and recorded checksums are untouched):

- `QuoteIdentifier` — consulted by the reference renderer and the generated
  select/CRUD SQL for every relation and column name. SQLite: unquoted (the
  reference rendering *is* the SQLite rendering); SQL Server: always bracketed —
  a legacy schema is full of reserved words (`transaction`, `order`, `desc`).
- `PagingRequiresOrderBy` — OFFSET/FETCH is only legal after ORDER BY; a paged,
  unordered select gains the constant `order by (select null)` (the page was
  order-arbitrary anyway).
- `SupportsRowValueIn` — `(a, b) in (select …)` doesn't parse on SQL Server; the
  reference renderer rewrites a composite subquery membership as a correlated
  EXISTS over the same subquery (root aliased `t`, derived table `s`, pairwise
  correlation), same rows, same parameter order.
- `VersionTableSql` — the runner's `schema_version` DDL was hard-coded SQLite
  (`IF NOT EXISTS` + STRICT); now dialect-owned, same columns everywhere.
- `RenameTableSql`/`RenameColumnSql`/`AddColumnSql`/`DropColumnSql`/
  `DropTableSql`/`DropIndexSql` — the typed migration actions, the derived-down
  deriver, and force-sync all render through these: SQL Server says `alter table
  … add` (no `column`), renames via `sp_rename`, and scopes `drop index` to the
  table. Checksums are per-database and a database lives on one dialect, so the
  divergence is invisible to any recorded history.

**ADR-0023's predicted stress points, resolved:**

- *Generated keys*: `insert …; select cast(scope_identity() as bigint)`, not
  `OUTPUT` — an OUTPUT clause without INTO fails on tables with triggers, and
  legacy databases (Fidelis) have triggers; the generated-key strategy is
  identity-only (ADR-0020), which scope_identity always covers.
- *Run lock*: one transaction + `sp_getapplock @LockOwner='Transaction'`
  (exclusive, 60s timeout, refusal raises). SQL Server DDL is transactional, so
  the SQLite failure semantics (a failed run applies nothing) carry over.
- *Strict-by-default*: needs no analog — SQL Server enforces declared types
  natively; STRICT was only ever the SQLite realization of the principle.
- *Storage types*: dates are **datetimeoffset** (the only marker-carrying
  storage — see below); strings/blobs are `(max)`, except key columns
  (`nvarchar(450)`/`varbinary(450)`, the indexable length); decimal defaults to
  `decimal(38, 9)` (declare real precision in migrations); enums-as-text are
  `nvarchar(100)`; booleans `bit`.
- *Capabilities*: procedures yes (still dormant — no execution machinery until
  Level 4), materialized views no (indexed views are a different mechanism),
  array parameters no (TVPs are not simple array parameters; §7.12 IN-expansion
  applies, well under the 2100-parameter cap at the 500 chunk size).

**The §7.9 date rule on a database with real date types.** SQLite stores ISO-8601
TEXT and the trailing `Z` is the UTC marker; the SQL Server analog of "a stored
value without UTC marking is a lint error" is: `datetimeoffset` reads back marked
(→ `DateTime` Kind=Utc via the offset), while `datetime`/`datetime2` values carry
no marker and **refuse with `VAL-020` at read time**. The escape for legacy
columns is the documented one — an `ITypeHandler<DateTime>` that declares the
column's actual kind (handlers win over the fixed table). `TypeConverter` learned
the provider-native temporal shapes (DateTimeOffset→DateTime via UTC,
date→DateOnly, time→TimeOnly) — value conversions, not dialect hooks.

**Conformance.** `conformance/ast/*.json` `expect` blocks now carry one entry
per dialect and the runner renders every case through every dialect — a case
missing a dialect's expectation fails the suite (error cases stay
dialect-neutral). The cases pin the cross-dialect parameter contract: bind order
is WHERE→limit→offset everywhere, even though OFFSET/FETCH renders `@c5` before
`@c4`.

**Testing.** Live tests run on SQL Server LocalDB — a per-fixture database,
created and dropped like the temp-file SQLite databases (ADR-0003), no mocks —
via `[SqlServerFact]`, which skips as a group where LocalDB is absent, so CI
still needs only the .NET SDK. Covered live: identity write-back, paging with
and without ORDER BY, reserved-word identifiers end-to-end, version-column
concurrency, transaction scopes, explicit/batch/link loading, subselect includes
over a paged root, the composite-key EXISTS rewrite, migrations up/down under
the applock, and the VAL-020 refusal on markerless datetime2. (Caveat: this
machine's LocalDB 2019 CU27 crashes on current Windows 11 — the known ntdll
incompatibility — so the live suite's first green run happens on a machine with
a working engine.)

**CLI.** `--dialect sqlite|sqlserver` (default sqlite) selects the dialect for
`migrate`/`status`/`baseline`/`validate`/`snapshot`/`diff`; `shadow` refuses
non-sqlite by name.

**Deferred, named** (not silently): `shadow`/snapshot regeneration for SQL
Server (rollbacks there need `Down()` overrides until snapshot tooling learns
the dialect — `MIG-020` says so); `diff`/force-sync against SQL Server compile
but are untested; the `MIG-012` view-guard DDL normalization against
`sys.sql_modules` output is untested; `migrations-cases/` and `cases/` fixtures
for SQL Server; procedure execution (Level 4 as always). SQL Server's schema
qualifier (`dbo.`) rides on the existing `[Table(Schema=…)]` metadata but got no
special handling this pass.

**Status.** Accepted.

## ADR-0025 — PostgreSQL dialect: the promised seams come due (2026-08-30)

Owner: "create PostSQL Dialect" — the second Level 4 dialect, second in
ADR-0023's order, pulled forward on the ADR-0024 precedent. PostgreSQL is the
dialect several Level 1 seams were explicitly designed for, and this ADR
realizes those promises rather than inventing new ones. `SimpleOrm.Postgres`
(netstandard2.0;net10.0) references Npgsql conditionally per TFM (4.1.14 on
netstandard2.0 — the last line with that target — and 9.0.3 on net10.0); the
4.1 leg carries known legacy-temporal caveats and net10.0 is the recommended
target for Postgres.

**Promises realized:**

- **`SupportsArrayParameters` finally does something** (§7.12, declared since
  ADR-0003, consulted by nothing until now). On a true-returning dialect,
  `ParameterBinder` binds a collection-typed args property as **one typed-array
  parameter** and leaves the SQL untouched — registry SQL is written
  dialect-natively (`where id = any(@Ids)`). The array is typed by what the
  conversion pipeline produces (enums → names, temporals → per the binding
  convention), and an **empty collection binds as a typed empty array** — 
  `bigint = any(text[])` refuses even with no elements, so the element type is
  derived from the property's declared element type, not the (absent) first
  value. The observable contract of spec/session.md — always parameterized,
  empty matches no rows — holds identically; SQLite and SQL Server keep the
  expansion strategy.
- **The migration run lock evolves as §7.23 anticipated — mostly.** One
  transaction for the whole run holding
  `set local lock_timeout = '60s'; select pg_advisory_xact_lock(1399418224, 1818578802)`
  (the key pair is arbitrary but fixed: ASCII 'Simp', 'leOr'). The anticipated
  *per-migration transactions* turned out unnecessary: Postgres DDL is
  transactional, so the single-transaction run keeps SQLite's atomicity — a
  failed run applies nothing — and the xact-scoped lock can never leak past a
  rollback. §7.23's text updated accordingly.
- **Materialized views come alive** (ADR-0008 called them dormant until "Level 4
  Postgres"). `SupportsMaterializedViews` is true; `CreateViewSql` branches:
  `create or replace view` for views, `create materialized view if not exists`
  for MVs. Live-tested. (`REFRESH MATERIALIZED VIEW` machinery is still nobody's
  job — Level 4.)
- **`RETURNING` works exactly as on SQLite** — the generated INSERT differs from
  the reference only in quoting.

**The one genuinely new seam: native temporal binding.** SQLite stores ISO-8601
TEXT (the string *is* the value) and SQL Server implicitly converts strings;
Postgres does neither — `timestamptz >= text` refuses outright. So `IDialect`
gained `BindsTemporalsNatively` (SQLite/SQL Server false, Postgres true):
`TypeConverter.ToDatabase` then hands the provider UTC-normalized
DateTime/DateTimeOffset (and DateOnly/TimeOnly) values instead of ISO strings —
same VAL-020 refusal for Kind=Unspecified, same instant either way. Reads were
already handled by ADR-0024's provider-native temporal support: `timestamptz`
comes back Kind=Utc (the marker-carrying storage), `timestamp without time
zone` comes back Unspecified and refuses with `VAL-020`, ITypeHandler being the
legacy-column escape. SQL Server stays on string binding as shipped — flipping
it to native is a possible later cleanup, deliberately not smuggled into this
change.

**The boring parts** mirror ADR-0024: always-double-quoted identifiers
(lowercase snake_case matches the catalog fold, so quoting is
reserved-word-proof and nothing else changes), `pg_catalog` introspection
queries with bigint-cast flags, storage types spelled the way `format_type`
reports them (`timestamp with time zone`, `double precision`; `numeric`
unconstrained — arbitrary precision needs no SQL Server-style default; `text`
for strings, keys included — no length hack needed), identity keys
(`generated by default as identity` so fixtures may insert explicit keys), the
`BaseTypeName` normalizer stripping mid-name precision
(`timestamp(3) with time zone`), and CLI `--dialect postgres`.

**Verified live** — unlike ADR-0024's still-pending LocalDB run, the full
integration suite executed against a real local PostgreSQL 16.4: identity
write-back and temporal round-trips, array parameters (filled and empty),
native-temporal criteria comparisons, reserved-word identifiers, version-column
concurrency, transaction scopes, explicit/batch/link loading, the composite-key
subselect through **native row-value IN** (the form SQL Server rewrites),
materialized view create+read, migrations up/down under the advisory lock, and
the VAL-020 refusal. Conformance `ast/` cases now pin three dialects per case.

**Deferred, named**: same list as ADR-0024 — `shadow`/snapshot/`diff`/force-sync
for Postgres untested or sqlite-only (rollbacks beyond `Down()` overrides need
snapshot tooling); criteria `InList` still renders per-value placeholders on
every dialect (valid everywhere; an `= any` rendering for Postgres is a possible
later optimization); `jsonb`, arrays-as-columns, `REFRESH MATERIALIZED VIEW`,
and schema qualifiers are Level 4 material.

**Status.** Accepted.

### ADR-0017 addendum 3 — `diff --amend`: regenerate the newest version in place (2026-09-08)

Owner: "when generating by model, there should be a method to update the last
generated" — while a version is still being shaped, a second `diff` should
replace it, not stack a `V0008` on a draft `V0007` nobody applied. And the
boundary is not git: "it doesn't necessarily [mean] not committed. I can commit
it but if I haven't pushed it, or anyone is using it, or the group agrees for
the developer to update it first is also possible."

**The ruling.** Whether a version may be amended is human protocol — unpushed,
pushed-but-unapplied, team agreement. The tool never consults repository
state. **The databases' recorded checksums are the tool's boundary**: every
database that applied the old version holds its checksum, so after an amend
`migrate` reports `MIG-010` there — the existing drift guard, which already
does not care who committed what. Each such database rolls back
(`migrate down --to N-1`) or is recreated.

Follow-up ruling, on the first cut refusing hand-written versions outright:
"seeder is not part of migrating DDL, it is about the data, so ignore it for
now. Other than that, even for [hand-written], it should be able based on the
latest current schema (the one embedded in code) compared with the one in
migration." So the distinction is not who wrote the files but whether the
version can be *computed*: the model against the schema the migrations below
it produce. Hand-written versions are amendable behind `--force` (the
project's "I know, do it" idiom), because their raw SQL, hooks, and data steps
are not reproducible from a diff — the run names every such file so they can
be re-added by hand. And "the schema in the migrations" must actually be
recorded: an object that earlier versions migrate but no snapshot describes
would diff as brand new (a bogus `create table`), so `diff` — in both modes —
refuses and points at `shadow`/`snapshot`. That guard closed a hole the plain
`diff` always had.

**The algorithm** (`DiffCommand`, the diff logic moved out of top-level
`Program.cs` into a class so it could be tested end to end):

1. Target `V000N` = the newest version in the assembly, not `N + 1`. No
   versions → refuse.
2. Locate the tool's own files by the generator's layout: `V000N.cs` and every
   `{Table,View,MaterializedView}/*/V000N_*.cs`, plus any `V000N.schema.json`
   (a snapshot of the draft itself).
3. Vet them: a file without the `Generated by simpleorm diff` header is
   hand-written and refuses unless `--force` (then it is replaced and named);
   the root's new **dialect stamp** must match `--dialect` (storage types are
   dialect-specific — a legacy root without a stamp is accepted); the count of
   step files must equal the count of compiled `V000N_*` steps in the
   namespace (a diverged layout refuses rather than guessing).
4. Baseline = the snapshot history **below N**, per object — the schema the
   migrations below N produce. Today's "newest snapshot" rule is wrong for
   amend: a snapshot taken of the draft would hide the change. An object with
   migration history below N but no snapshot refuses (see the follow-up ruling).
5. Diff, with the same refusals (`DDL-004`, `DDL-003`) and the same declared
   renames (re-declared on the command line; an undeclared one shows as
   drop+add and trips the `--allow-remove` gate — visible, never silent).
6. Commit: only after every refusal has passed, delete the located files and
   write the new set, printing each. An empty diff removes the version
   entirely (the model no longer differs from the recorded past). Hand edits
   inside generated files are lost — the printout and the developer's own
   diff show what to re-add.

Steps 1–5 read, step 6 writes: the tree never ends up half-replaced. With
`--db`, an applied draft gets a warning (never a refusal — other databases
exist the tool can't see) naming the `MIG-010` that follows and the rollback
to run first.

**Mechanics.** `MigrationGenerator.EmitRoot` now stamps the dialect
(`Generated by simpleorm diff (ADR-0017, dialect sqlite)`) and exposes the
marker and stamp parser; the CLI project is referenced by the tests, which
drive `DiffCommand.ExecuteAsync` against a compiled fixture (`V0001`/`V0002`)
and a temp migrations directory.

**Conformance** (owner: "also add it in the conformance"): `conformance/amend-cases/`
— a migrations tree *as data* (`files` with or without the generator's header,
a root's dialect stamp, `snapshots` at their paths) plus the command in; the
exit code and the files deleted/written/kept, content fragments, and
output/error substrings out — eleven cases against a fixed fixture
(`amend_widgets`: V0001, V0002, one step each; model `id, name, remark`) that
each implementation defines natively, exactly as `entities/` fixtures work.
They pin: regenerate-in-place against the history below (a draft snapshot at N
ignored *and* deleted), removal on an empty diff, the applied warning,
hand-written refusal without `--force` and replacement with it, the dialect
and layout refusals, nothing-to-amend, the recorded-past refusal in both modes,
and plain `diff` unchanged. The C# runner (`ConformanceAmendTests`) is the only
amend test; the shape-vs-shape `diff-cases/` are untouched.

**Status.** Accepted.

## ADR-0026 — The PHP port: Levels 0–1, built to one standard by parallel agents (2026-09-08)

Owner: "Generate the PHP version, summon several sonnet 5 agents, make sure all
agents follow the same standard so the code generated seems uniform. Don't forget
to keep maintainability, extensibility and reusability." PHP is first in the
ADR-0023 port order because it doubles as field validation against Fidelis.

**Scope** (§12: the first port covers Levels 0–1). Metadata (all three loaders,
the naming convention, the byte-identical `EntityMap` export — including
relationship *metadata*, because the pinned entity exports carry it), mapping
and the fixed conversion table, parameters (IN-expansion), the session with
generated CRUD, the ADR-0012 criteria core with the reference renderer,
SchemaGuard, code migrations with the runner, derived rollbacks, snapshots, the
diff generator with `--amend`, force-sync, the shadow replayer, and the CLI.
Out: relationship loading, fetch modes, joins (Level 2); non-SQLite dialects
(the `Dialect` seam is mirrored member-for-member so they slot in later).
Lives in `php/` beside `dotnet/`; shares no code; consumes `spec/` and
`conformance/` unchanged — with the two exceptions the port exposed, below.

**Method: foundation first, then fan-out.** Uniformity across independently
working agents cannot come from prompts alone, so before any agent started the
tree already held (1) `php/CODING-STANDARD.md` — the contract: PHP 8.4,
`strict_types`, PSR-12/PSR-4, `final`/`readonly` by default, enums for closed
sets, no static state, errors as `SimpleOrmException(code, target, message)`,
byte-identical SQLite SQL, real-database tests only, one implementation per
concept, and a §10 table that is the **only** place a PHP divergence from the
spec may live; (2) the shared contracts as code — `EntityMap` and friends, the
attributes, the `Dialect` interface (exactly the C# `IDialect` member set), the
AST nodes, `Decimal`, `TypeHandler`, `CanonicalWriter` (the one JSON writer,
reproducing System.Text.Json's escaping); (3) the ten fixture entities
mirroring `dotnet/samples`. Then three Sonnet agents in parallel (metadata;
dialect + renderer + binding + conversion; migrations model + generator +
snapshots), then two (session/CRUD/criteria; runner + sync + diff command),
then one (SchemaGuard + CLI + shadow + class discovery), each told which C#
files to mirror and which pinned artifacts to hit byte-for-byte, then a
uniformity review over the whole tree. Where an agent needed a divergence it
added a §10 row rather than coding around it — the table gained five rows that
way, each with its reason.

**The PHP shape** (the §10 table, in one paragraph): synchronous methods and a
`Generator` for streaming; PHP 8 attributes with the same names; the neutral
`ColumnType` token resolved once on `PropertyMap` (explicit
`#[Column(type: ColumnType::Int32)]` where PHP's `int` cannot say `int32`,
`date`, `guid`…); `Types\Decimal` as a string-backed value object;
`DateTimeImmutable` in UTC under the §7.9 rule; "required" = a typed property
without a default; navigations `public private(set)` (PHP 8.4 asymmetric
visibility) with the target class explicit on collection navigations;
statement parameters as a name→token map; `#[Index([...], name:, unique:)]`
with `SortOrder` tokens; registries as public static methods returning
`Query`/`Command`; `$db->from(User::class)` for criteria; `get(User::class, 7)`
/ `get(UserRole::class, [$a, $b])`; migrations and entities discovered from a
PSR-4 directory (`--src/--namespace`, `--migrations`) with snapshots as files
beside them; SchemaGuard describing statements inside a shield transaction
that is always rolled back (PDO exposes column metadata only after execute);
handler types exporting as `php:<class>`.

**What the port changed in the spec** (§12: "the port changing the spec is the
point"):

1. `conformance/amend-cases/` were C#-flavored: `.cs` paths and call-syntax
   fragments (`AddColumn("remark", "TEXT")`). Now source paths carry no
   extension (each implementation appends its own) and fragments are tokens
   (`remark`, `TEXT`, `V0002_AddRemark`, the generator header); both runners
   updated and green.
2. The entity export's `targetForeignKeyProperties` / `linkForeignKeysTo*`
   carry **language-side property names** (`UserId`), contradicting the
   export's own rule ("column-centric and language-neutral"). Interim: the PHP
   exporter maps `userId` → `UserId` (documented in §10). Proposed fix, for an
   owner ruling: export column names there too — a C# + `conformance/entities`
   change.
3. `MAP-017`'s duplicate-parameter sub-case is unreachable where parameters
   are a map; the spec should state it as "the declaration must be a
   name→type mapping without repeats", which every language can express.
4. Port pitfall worth a line in the spec's session section: PDO binds every
   `execute($array)` value as text, which silently breaks numeric comparisons
   on expression columns; the port binds typed. "Values bind with their
   database type" is a spec-level statement, not a C# accident.

**Dependencies.** `php >= 8.4`, `ext-pdo` (+ the SQLite driver, loaded with
`-d extension=pdo_sqlite` by the Composer scripts so no `php.ini` changes);
dev: `phpunit/phpunit ^11.5` — the xUnit analog, the one dev dependency
(recorded here per §4's "ask first").

**Verification.** Every Level 0–1 conformance folder runs through a PHP
runner — `entities` (10/10 byte-identical), `cases` (7/7), `crud-cases` (2/2),
`ast` (15/15 `sqlite` pins byte-identical), `diff-cases` (9/9),
`snapshot-cases` (3/3 byte-identical), `migrations-cases` (9/9), `amend-cases`
(11/11) — unchanged except as listed above; `load-cases/` is Level 2 and
excluded. PHP suite after the review pass: **310 tests, 880 assertions, all
green** (`composer test`, PHP 8.4.23, real temp-file SQLite databases). The C#
suite is unchanged by the port apart from the amend runner's extension rule:
304 passed, 10 skipped (the SQL Server LocalDB suite — no working engine on
this machine; the PostgreSQL suite ran live). The
review pass consolidated four duplicated implementations (ISO-8601 formatting
into `Types\Iso8601`, snake_case into the naming convention, directory listing,
typed PDO binding into `Parameters\PdoBinder`), aligned 22 class docblocks to
the citation rule, removed a PHP-only `PRM-012` emission from SchemaGuard that
the reference never makes, and found one real bug (acronym runs mis-split by a
private snake_case regex — now pinned by a test).

**Status.** Accepted.

## Design seed — partial updates: an opt-in column list on `UpdateAsync` (2026-09-08)

**Itch.** §7.15 makes `UpdateAsync` a full-row write: every mapped non-key,
non-version, non-generated column, no dirty checking. On wide rows (a large
`TEXT`/JSON column beside a handful of flags) changing one flag rewrites the
whole row, and two users editing disjoint columns of an unversioned row
silently overwrite each other. Hand SQL is the Level 1 answer; it works but
loses the generated-CRUD guarantees (explicit column list from `EntityMap`,
version check, `CRUD-001`/`CRUD-010`).

**Seed.** A **separately named method** (owner ruling 2026-09-08: no overload — PHP, Go, and Rust have no overloading, and the spec must name one operation per concept) that keeps every §7.15–16 guarantee
and narrows only the SET list:

```csharp
await db.UpdateAsync(user, ct);                                                   // full row, unchanged
await db.UpdateOnlyAsync(user, [nameof(User.DisplayName), nameof(User.Email)], ct); // named columns
```

- **Property names, not column names** — same vocabulary as criteria
  (ADR-0012/0020); the dialect renders columns from `EntityMap`.
- **Validation, each a named error:** unknown property; a key, version, or
  generated property in the list; an empty list. Codes to be registered in
  `spec/errors.md` before any message exists (§13) — next free block is
  `CRUD-005..` (`CRUD-001..004` and `CRUD-010` are taken).
- **Version semantics unchanged:** `SET …, version = version + 1 WHERE key AND
  version = @version`; zero rows is still `CRUD-010`. Row-level optimistic
  concurrency stays row-level — a column list does not make disjoint edits
  merge, and that is deliberate.
- **Navigation consistency check (ADR-0005 add.1)** runs only for FK
  properties in the list.
- **No new metadata.** No `[Column(WriteMode…)]`, no per-property "large"
  flags; the caller says what changed because at Level 1–2 the caller is the
  only one who knows.
- **Name.** `UpdateOnlyAsync` proposed (PHP `updateOnly`); `UpdateColumnsAsync` /
  `PatchAsync` were the alternatives — "Patch" rejected for its HTTP
  connotation. Final name is the owner's call; the spec section is titled by
  the operation ("update by column list"), not the C# name.

**Explicitly not this seed:** snapshot-based dirty tracking (Level 3 — needs a
loaded-state snapshot per entity, which is the identity map's job); an
AST-rendered `UPDATE … WHERE <criteria>` without loading the entity (a
plausible later Level 2 item on top of ADR-0020's renderer, but a different
feature — set-by-criteria has no version to check).

**Where it lands.** `spec/crud.md` update section + `conformance/crud-cases/`
(an `"op": "update"` variant carrying `"columns"`), the PHP port's `update()`
under the same rule. TODO marker sits on `Db.UpdateAsync`.

**Status.** Seed, shape agreed (full-row `UpdateAsync` unchanged + a named
column-list method). **Timing ruling (2026-09-08): not before the Go port is
done** — the port order (ADR-0023) comes first. Realized the same day by
ADR-0028 once ADR-0027 landed the Go port.

## Decision — seeding stays with the developer (2026-09-08)

Asked what the project says about seeding: nothing beyond migration data steps
(`.Sql(insert …)` + `PreDown` delete, sample V0005), the `diff --amend`
`--force` exception for such steps (ADR-0017 add.3), and the test-only
`conformance/fixtures/seed.json`. Two directions were sketched — typed,
diffable reference-data steps in migrations; and a separate
environment-seed CLI command using the fixtures JSON shape.

**Ruling.** Neither. "It should be up to the developer how to seed, [and]
whether it has a specific environment or not." The library provides the
mechanism it already has — a data step is a migration step like any other,
with hooks for its removal — and takes no position on reference vs.
environment seeds, no seed file format, no `seed` command. The generator's
`--force` exception for data steps stands as documented.

**Status.** Accepted; reopen only if a port or Fidelis shows the raw-SQL data
step is insufficient.

## ADR-0027 — The Go port: Levels 0–1 by the ADR-0026 method; the spec meets a language without attributes, exceptions, or async (2026-09-08)

Owner: "Build the Go port of SimpleOrm under `go/`, Levels 0–1 only, replicating
the method that produced the PHP port." Go is second in the ADR-0023 port
order because it is the spec-neutrality stress test §12 predicted: struct tags
instead of attributes, errors as values, `context.Context` instead of
cancellation tokens, no inheritance, no constructors, no assemblies. The port
changing the spec is the point.

**Scope** (§12: the first ports cover Levels 0–1) — the same as ADR-0026:
metadata (the declaration, convention, and builder loaders; the byte-identical
`EntityMap` export including relationship metadata), mapping with the fixed
conversion table and the handler registry, parameters (IN-expansion), the
session with generated CRUD and explicit transactions, the ADR-0012 criteria
core with the reference renderer, SchemaGuard, code migrations with the
runner, derived rollbacks, snapshots (format v2), the diff generator with
`--amend`, force-sync, the shadow replayer, and the CLI. Out: relationship
loading, fetch modes, joins (Level 2); non-SQLite dialects (the `Dialect`
seam mirrors `IDialect` member for member so they slot in later). Lives in
`go/` beside `dotnet/` and `php/`; shares no code; consumes `spec/` and
`conformance/` unchanged — not one case file needed editing this time.

**Method: foundation first, then fan-out** — the ADR-0026 method, repeated.
Before any agent started, the tree held (1) `go/CODING-STANDARD.md`: Go 1.25+,
`gofmt`/`vet`, packages mirroring the C# assemblies (`orm`, `orm/sqlite`,
`orm/cli`) with the areas under `orm/internal/*`, file names mirroring the C#
file names, names under Go's initialism rule, errors as `*core.Error` values
with `errors.As`, real-database tests only, one implementation per concept,
and a §10 table that is the **only** place a Go divergence may live; (2) the
contracts as code — `internal/core` (errors, `EntityMap` and friends, the
neutral tokens, `Decimal`/`GUID`/`Enum`, snake_case with the normative
vectors, the type-handler registry, the canonical JSON writer reproducing
System.Text.Json's escaping, the placeholder scanner, the `Dialect` seam, the
AST and criteria factories, the descriptor types), the `orm` façade (aliases,
the registry entry types), the metadata loader shell, the full SQLite dialect
port verified against a real database, test support, and the ten fixture
entities. Then three Sonnet agents in parallel (metadata + `entities` runner;
renderer + converter + JSON handler + binder + `ast` runner; migrations model
+ snapshots + generator + sample tree + amend fixture + `diff-cases`/
`snapshot-cases` runners), then two (session/CRUD/criteria/result mapper +
`cases`/`crud-cases`; runner + derived downs + force sync + diff/amend +
`migrations-cases`/`amend-cases`), then one (SchemaGuard + CLI + shadow), then
a uniformity review over the whole tree. Every agent owned whole packages, so
a colleague's half-written file never broke another's build; each reported
the divergences it needed instead of coding around them.

**The Go shape** (the §10 table, in one paragraph): per-field facts in the
`orm` struct tag (`column`, `column=name`, `key`, `generated`, `version`,
`enum_int`, `type=<token>`, `ignore`, `many_to_one=`, `one_to_many=`,
`one_to_one=`, `many_to_many`) and everything a tag cannot carry — the
relation source with its SQL, indexes, typed references — in a small
descriptor: `func (User) Entity() orm.EntityDef { return orm.EntityDef{Source:
orm.Table("users"), Indexes: …, ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID")},
ManyToMany: …} }`, generics standing in for `typeof(T)`; enums as named types
implementing `orm.Enum` (`EnumNames()`), `orm.Decimal` string-backed,
`orm.GUID`, `time.Time` always bound as UTC (Go has no Kind=Unspecified);
nullability is a pointer; a DTO's non-pointer field is `required`; no
constructors, so `MAP-003` cannot occur; embedded structs are inheritance
(own fields first, then the embedded ones — the reference's order); every
I/O function takes a `context.Context` and `Stream` is an `iter.Seq2[T,
error]`; generic methods become package functions — `orm.Query(ctx, db,
entry, args)`, `orm.Get[User](ctx, db, 7)`, `orm.Insert(ctx, db, &user)`,
`orm.Delete[T]`/`orm.DeleteEntity`, `orm.From[User](db).Where(…).List(ctx)` —
with the registry entry types named `QueryEntry`/`CommandEntry` because a Go
type and a function cannot share the name `Query`; `orm.In[T](property,
values ...T)` replaces the three C# overloads and dissolves the
string-is-IEnumerable<char> trap; explicit `orm.Registry{Entities, Entries,
Migrations, Snapshots}` replaces assembly scanning, `embed.FS` replaces
embedded resources, and the CLI is a library (`cli.Run(ctx, registry, args,
stdout, stderr)`) the application's own `main` embeds — a Go binary cannot
load another binary's values; migration versions and steps are types with the
reference's exact names (`V0002_AddDisplayName`, underscores included, parsed
by `MIG-001`), a step embedding `orm.TableMigration[models.User]` and
implementing `Action`, the optional `Down`/`PreDown`/`PostDown` as optional
interfaces, one Go package per object directory (`Table/User` → `package
user`) so the generator emits gofmt-clean sources and `DiffOptions` takes
`Set`, `OutDir`, and the migrations `Package` import path; the session pins
one `*sql.Conn` from the driver's pool and every transaction is `BEGIN
IMMEDIATE` (`_txlock=immediate`), exactly what Microsoft.Data.Sqlite's
`BeginTransaction()` does; SchemaGuard describes statements without executing
them through modernc's column-origin API, exposed as the optional
`StatementDescriber` capability beside the mirrored `Dialect` seam, because
`database/sql` has no schema-only describe.

**What the port changed in the spec** (§12):

1. `spec/errors.md` gained **`MAP-023`** — malformed mapping declaration: a
   language whose annotations are not type-checked at compile time needs a
   code for an unparseable tag, an unknown token, an embedded pointer, a
   builder naming a missing field. Unreachable in C# and PHP, where the
   attributes are typed.
2. **`MAP-011` is unenforceable in Go**: fields have no setters, and a
   navigation must be exported for the library to populate it at Level 2.
   The spec should state the rule as an intent — "the library is the only
   writer of a navigation" — enforced where the language can express it
   (C# setters, PHP `private(set)`), documented where it cannot.
3. Typed declarations make three C# codes unreachable: `MAP-003` (no
   constructors), `MAP-012` (one `Source` value), and the odd-count/wrong-token
   sub-cases of `MAP-017` (typed `orm.Param[T]`) — which sharpens ADR-0026's
   finding 3: `MAP-017` should read "the parameter declaration is a name→type
   mapping without repeats".
4. The `targetForeignKeyProperties`/`linkForeignKeysTo*` interim of ADR-0026
   recurs: Go exports `ToPascalCase(field)` (`UserID` → `UserId`) to stay
   byte-identical. Two ports now carry the same workaround; the column-name
   proposal is ready for the owner's ruling.
5. Two reference behaviors worth a spec line, found the way ADR-0026 found
   PDO's text binding: the session's SQLite transactions are `BEGIN IMMEDIATE`
   (spec/session.md says nothing about the mode; Microsoft.Data.Sqlite's
   default is what the reference actually does), and SchemaGuard's
   "describe without executing" needs a driver-level column-origin API — a
   port pitfall on any platform whose standard database layer hides it.
6. `conformance/` proved language-neutral: every folder ran unchanged; the
   ADR-0026 neutralization of `amend-cases/` (extension-less source paths,
   token fragments) is what made `.go` appending trivial. The one remaining
   flavor is the pinned directory layout `Table/<Object>/` — mixed-case
   directories are legal but unidiomatic Go package paths; tolerated rather
   than changed.

**Dependencies.** `modernc.org/sqlite` v1.58.0 — the pure-Go, cgo-free SQLite
driver, the Go analog of `Microsoft.Data.Sqlite`/`pdo_sqlite`, and the port's
only runtime dependency (recorded here per §4's "ask first"; the owner's brief
pre-approved it). Tests use the standard `testing` package only.

**Verification.** Every Level 0–1 conformance folder runs through a Go runner
— `entities` (10/10 byte-identical), `cases` (7/7), `crud-cases` (2/2), `ast`
(15/15 `sqlite` pins byte-identical), `diff-cases` (9/9), `snapshot-cases`
(3/3 byte-identical), `migrations-cases` (9/9), `amend-cases` (11/11) — with
no case file changed; `load-cases/` is Level 2 and excluded. The shadow
replayer reproduces every committed sample snapshot byte-for-byte modulo
`generatedAt` — the model-vs-history integrity check of spec/migrations.md,
passing on a second implementation. Go suite before the review pass: **331
tests, all green** (`go vet ./... && go test ./...`, Go 1.26.5, real temp-file
SQLite databases). The C# and PHP suites are untouched by the port (no shared
file they read changed; `spec/errors.md` only gained a row).
The review pass consolidated four duplicated implementations (the scalar/DTO
member classification SchemaGuard had copied from the result mapper — now
`mapping.IsScalarType`/`NamesMatch`; the live column/index introspection that
force-sync, SchemaGuard, and the shadow replayer each queried their own way —
now one reader in `migrations`; the snapshot-directory lookups of the diff
command and the shadow replayer; the conformance runners' JSON-number
normalization), added the missing doc comments on the criteria factories,
removed five construction-log comments, aligned four runner test names, and
added the one §10 row the code had documented only in a comment (SchemaGuard
names a registry entry by its SQL source description — Go values carry no
field names). Every SQL-composition site was swept: identifiers come from the
metadata or literal DDL, values always bind. No Go-only emission the reference
never makes, no behavioral bug. Suite after the review: **331 tests, all
green**; `gofmt`/`go vet` clean.

**Status.** Accepted; uncommitted pending the owner's review.

## ADR-0028 — Update by column list: a separately named operation in all three implementations (2026-09-08)

The 2026-09-08 design seed, built once ADR-0027 satisfied its timing ruling
("not before the Go port is done"). Owner: "work on the TODO".

**Decision.** Beside the full-row update (§7.15, unchanged byte-for-byte) each
implementation gains one **separately named** operation that writes only the
properties the caller lists:

```csharp
await db.UpdateOnlyAsync(user, [nameof(User.Name), nameof(User.Email)], ct);
```
```php
$db->updateOnly($user, ['name', 'email']);
```
```go
err := orm.UpdateOnly(ctx, db, &user, "Name", "Email")
```

Never an overload or an optional argument: PHP and Go have no overloading, and
the spec names one operation per concept ("update by column list",
`spec/crud.md`). The rules, identical everywhere:

- **Property names**, the criteria vocabulary — never column names; the dialect
  renders columns from `EntityMap`. Go takes field names, variadic (idiomatic;
  an empty call is the empty-list error, not a compile error).
- **Validation before any write**, each a named code registered in
  `spec/errors.md`: `CRUD-005` unmapped name; `CRUD-006` a key, version, or
  database-generated property (an update never writes them); `CRUD-007` empty
  list or repeated name. `CRUD-003`/`CRUD-002` from the shared writable-keyed
  guard precede them.
- **Everything else is the full-row update**: keyed WHERE, `version = version
  + 1` in the SET with the entity's version in the WHERE, `CRUD-010` on zero
  rows, in-memory bump on success, `CRUD-001` without a version column. The
  navigation/FK consistency check (ADR-0005 add.1) runs only for the FK parts
  in the list.
- **Concurrency stays row-level** — deliberately. Two callers updating disjoint
  columns of the same versioned row still conflict; the list narrows what is
  written, never what is checked. Pinned by
  `conformance/crud-cases/partial_update_concurrency.json`.

**Dialect seam.** One new member, `UpdateOnlySql(map, properties)` (PHP
`updateOnlySql`, Go `UpdateOnlySQL`), rendered by the same private function as
the full-row form in every dialect (SQLite, SQL Server, PostgreSQL) — a test in
each implementation pins that listing every writable column reproduces
`UpdateSql` byte-for-byte, so no checksum or conformance pin moved.

**Not this ADR.** Snapshot-based dirty tracking (Level 3: needs a loaded-state
snapshot per entity, the identity map's job). An AST-rendered `UPDATE … WHERE
<criteria>` that never loads the entity (a different operation — no version to
check). Any metadata such as `[Column(WriteMode…)]` — at Levels 1–2 the caller
is the only party who knows what changed.

**Conformance.** `crud-cases/` gained `partial_update.json` (the unlisted value
proves untouched; every validation code) and `partial_update_concurrency.json`
(disjoint-column conflict, version bump, `version` refused as `CRUD-006`).
The case format: an `update` step with `"columns"` (column names, like
`values`); the runners resolve columns to property names and pass an unknown
column through unchanged so `CRUD-005` is expressible. All three runners
updated; no existing case changed.

**Verification.** C#: 310 passed, 10 skipped (SQL Server LocalDB, no engine on
this machine), both TFMs build. PHP: 319 tests, 949 assertions (the two
sample-project failures seen first were a stale Composer autoloader after the
ADR-0027-era merge — `composer dump-autoload` fixed them, no code change). Go:
12 packages ok, `gofmt`/`vet` clean.

**Status.** Accepted. The `Repository` surfaces (C# `Repository<T>`, the PHP
sample's `Repository`, Go `Repository[T]`) forward it under the same name.

## ADR-0029 — Level 2 exit rulings: what closes the level and how it is proven (2026-09-08)

Asked "what is left for Level 2 exit", the tree was checked and six open
items were put to the owner one at a time. Findings that prompted them:
`spec/query-ast.md` still lists joins under "Deliberately absent" although
ADR-0022 add.1 gave `SelectAst` joins and projection for the Join fetch mode,
and no `conformance/ast/` case contains a join; `load-cases/` pin the three
fetch modes only through the runner's `viaQuery` replay; the benchmarks
predate criteria and loading (last touched 2026-08-29); M5 was skipped
(ADR-0023); owned types were scheduled "later in Level 2" (ADR-0019) and never
built; and two ports carry the same three spec deviations (ADR-0026/0027).

**Rulings.**

1. **Fluent front-end (M5) moves out of Level 2.** Typed lambdas remain
   approved per-language sugar over the string/AST core (ADR-0021 add.2),
   built on demand on an owner sample. Not an exit criterion; the portable
   contract is what the ports pass today.
2. **Owned types are built before exit.** A value object stored as columns of
   its owner's table (no table of its own) becomes a Level 2 milestone: a
   metadata kind, mapping rules, an export-format change, spec + conformance,
   and both ports follow. This is the one ruling that adds work rather than
   removing it.
3. **The entity export names FK references by column.**
   `targetForeignKeyProperties` / `linkForeignKeysTo*` become column-name
   lists (`targetForeignKeyColumns: ["user_id"]`), consistent with the export's
   own "column-centric and language-neutral" rule. C# changes, the
   `conformance/entities/*.json` regenerate, PHP and Go drop their PascalCase
   workarounds (ADR-0026 finding 2, ADR-0027 finding 4).
4. **`MAP-017` is rephrased neutrally**: "the parameter declaration is a
   name→type mapping without repeats"; how a language spells it is its own
   business. Code and C# behaviour unchanged (ADR-0026 finding 3, ADR-0027
   finding 3).
5. **`MAP-011` becomes an intent**: "the library is the only writer of a
   navigation", enforced where the language can express it (C# setters, PHP
   `private(set)`), documented where it cannot (Go exported fields). Code
   kept (ADR-0027 finding 2).
6. **Exit is proven by extending the Go port to Level 2** — relationships,
   AST joins, explicit/batch/eager loading in all three fetch modes — from
   `spec/` and `conformance/` only, the way ADR-0026/0027 proved Level 1. Go
   was chosen over PHP because no setters and no proxies is the hardest test
   of the loading design; the places it gets stuck are the remaining spec
   debt, and the places it changes the spec are the point (§12).

**Owed work, not rulings** (surfaced by the same check): the join/projection
node vocabulary in `spec/query-ast.md` with join cases in `conformance/ast/`;
a spec sweep for "future" wording that later work has overtaken; the M6
benchmark refresh covering criteria and at least one loading path.

**Proposed order** (owner picks the first item next session): spec debt on
joins → the three export/wording fixes (3–5, small, and they unblock the Go
port) → owned types (2) → benchmark refresh → the Go Level 2 port (6) as the
exit audit → declare Level 2 closed.

**Status.** Accepted. CLAUDE.md §3/§7b updated to match.

## ADR-0030 — Owned types: value objects as columns of the owner, with a flat EntityMap (2026-09-08)

ADR-0029 ruling 2 built. An **owned type** is a value object stored as columns
of its owner's table — `UserProfile.Address` → `address_street`,
`address_city`, `address_postal_code` — with no table, key, version, generated
column, relationship, index, or nested owned type of its own.

**Declaration — two markers, both required.** The type carries `[Owned]` at
class level (that is what keeps it out of the entity set: the CLI, SchemaGuard,
and the shadow rebuild skip such classes, and loading one as an entity is
`MAP-024`). The owner's navigation carries `[Owned]` too, optionally with a
`Prefix` — mapping stays opt-in per property (ADR-0004), and a property whose
type merely happens to be owned does not map by itself. The members follow the
entity rules for `[Column]`/`[Ignore]`/`[EnumAsInt]`/`MAP-010`; anything else
inside is `MAP-024` (one code, each sub-case its own message).

**The load-bearing decision: the `EntityMap` stays flat.** Each member becomes
an ordinary `PropertyMap` of the owner — column `<prefix><member column>`,
property name the dotted path `Address.City`, placed at the navigation's
declaration position — carrying an `Owner` link to an `OwnedMap` (navigation,
prefix, nullability, members). Consequences, all deliberate:

- CRUD, criteria (`Criteria.Eq("Address.City", …)`, orderings, indexes), DDL,
  snapshots, diff, force-sync, SchemaGuard, and the export needed **no
  changes** — they read `map.Properties` and see columns. The export records
  no owned structure: it is column-centric, and two implementations that
  flatten the same declaration must export identically.
- Only the result mapper regroups: after the owner's constructor algorithm
  (which never sees owned members — dotted names match no parameter), each
  owned navigation is constructed through its parameterless constructor and
  attached; a **nullable navigation whose member columns are all NULL stays
  null**, a required one is always constructed.
- **Nullability:** a nullable navigation forces every member column nullable
  (a null value stores as all-NULL); a required navigation keeps each
  member's own nullability. A NULL into a non-nullable member of a present
  segment is `MAP-031`, as for an entity.
- `PropertyMap` gained `GetValue`/`SetValue` accessors that walk the path
  (creating the owned instance on first write); the session, loading, and
  the conformance runners use them instead of `PropertyInfo` directly — the
  shape PHP (`getValue`/`setValue`) and Go (`Get`/`Set`) already had.
- `UpdateOnlyAsync` accepts the navigation's name as shorthand for all its
  members (ADR-0028's list semantics unchanged; a repeat is still `CRUD-007`).

**Not in this ADR.** Nesting (an owned type inside an owned type — `MAP-024`;
one level covers the value-object case and keeps paths two segments deep).
Collections of owned types (that is a one-to-many table). Owned declarations
in the manual `EntityMapBuilder` and the convention loader (attribute loader
only; the others refuse nothing new — a class-typed property there is what it
always was). Constructor-parameter construction for owned types
(parameterless + setters; records with positional parameters are `MAP-024`).

**Fixture and conformance.** `UserProfile.Address` (sample V0010 adds the three
nullable columns, snapshot embedded); `conformance/entities/user_profile.json`
regenerated (flat); `conformance/cases/owned_read.json` (the seeded profile
now carries an address); `conformance/crud-cases/owned_profile.json` (full
update writes owned columns, column-list update writes one, an owner inserted
without the value reads back all-null). The runners' `UserProfile` entry and
accessor use are the only runner changes. Ports follow with their native
declaration (PHP attribute on class + property; Go `orm:"owned"` tag with the
class marker as a method or tag on the struct) and the same `MAP-024` rules.

**Verification (C#).** 324 passed, 10 skipped; both TFMs build. Sample
migration count moved 9 → 10 in the tests that pin it.

**Ports (same day).** PHP: `#[Owned]` on class and property, `OwnedMap`,
`PropertyMap::getValue/setValue` walk the path, mapper regroups, CLI and
SchemaGuard scans skip owned classes — 326 tests OK. Go: `orm.OwnedType`
marker interface (the idiom for a class attribute, like `orm.Enum`) plus the
`owned` / `owned=<prefix>` tag option, `core.OwnedMap`, `Get`/`Set` walk the
navigation and allocate a nil pointer on write, `Loader.Load` refuses an owned
type, the mapper leaves a pointer navigation nil for an all-NULL segment —
12 packages ok. Both carry `UserProfile.Address` and sample V0010 with the
byte-identical snapshot; every conformance folder is green in all three.

**Status.** Accepted; implemented in C#, PHP, and Go.

### ADR-0029 addendum 1 — the spec sweep and the benchmark refresh (2026-09-09)

**Spec sweep.** Six passages described as future what later milestones had
built: eager loading (`loading.md`), indexes "declaration-only until Level 3"
(consumed by DDL/snapshots/diff since ADR-0011/0017), relationships
"declaration-only until Level 2" and "metadata only until milestone 3"
(`metadata-model.md`), "the Level 2 fluent front-end later" (`query-ast.md` —
now per-language sugar outside the contract, ADR-0029), "future Go/Java/PHP
ports" (`conformance/README.md`), and CLAUDE.md's `[Index]` note. All reworded
to the present; no rule changed.

**Benchmark refresh** (`dotnet/benchmarks`, new `LoadingBenchmarks`; ShortRun,
1000 rows × 3 child rows, SQLite, Ryzen 7 5700U):

| Path | SimpleOrm | Dapper by hand |
|---|---|---|
| criteria `Where(Eq).OrderBy.Limit(100)` | 134 µs / 8.2 KB | 123 µs / 6.4 KB (inline SQL) |
| registry query, same SQL | 123 µs / 7.3 KB | — |
| graph, `Fetch(SubSelect)` | 4.1 ms / 1.45 MB | 3.8 ms / 1.15 MB (multi-mapped join + manual grouping) |
| graph, `Fetch(Join)` | 6.4 ms / 2.23 MB | — |
| graph, `Fetch(MultiQuery)` and `LoadEachAsync` | 7.2 ms / 1.83 MB (500-key chunks) | 16.0 ms / 1.12 MB (one 1000-key `IN`) |

Findings: the AST renderer costs ~9% over inline SQL for a small query (within
the §8.8 target; the registry path is at parity); on SQLite the parameter-heavy
`IN` list dominates batch loading — Dapper's single 1000-key list is the slowest
of all, the reference's 500-key chunks half that, and **SubSelect, which binds no
keys, is the fastest mode** and within 8% of a hand multi-map; **Join allocates
~2× the SubSelect path** (segment readers, per-row key tuples, a Gen2 collect)
and is slower than SubSelect here, so its value is round trips, not speed. The
Dapper graph baselines stop at a grouped lookup and so carry slightly less work
than the reference, which attaches the collections. Not acted on: the Join
allocation is a candidate for the Level 4 performance pass, noted rather than
tuned — the mode's contract (one round trip, no in-memory paging) is what Level
2 pins.

**Status.** Both owed items closed; the Go Level 2 port is the last item before
declaring exit.

## ADR-0031 — The Go port reaches Level 2 from the spec alone; Level 2 exit (2026-09-09)

ADR-0029 ruling 6, executed. The exit criterion for a level is "a second
developer could reimplement it from `spec/` + `conformance/` alone". Levels
0–1 were proven that way twice (ADR-0026/0027). Level 2 is proven by
extending the Go port — the port with no attributes, exceptions, async,
inheritance, or setters — to relationships, the Level 2 AST, and all three
fetch modes, under a rule stricter than the earlier ports: **the agents were
forbidden to open `dotnet/` or `php/`**. Every question the spec failed to
answer was recorded instead of resolved by peeking; that list is the audit.

**Method.** The ADR-0026 method again: the foundation first (the Level 2 AST
types, `FetchMode`, `Load`/`LoadEach`, the chain's `Include`/`Fetch` hooks,
stub files per area, CODING-STANDARD §10 rows), then one Sonnet agent for the
renderer + `ast/level2` runner, two in parallel for the batch/MultiQuery/
SubSelect engine + `load-cases` runner and for join mode, then a uniformity
review. Six commits; 12 Go packages green throughout.

**What the Go shape is.** `orm.Load(ctx, db, &e, "Nav")`, `orm.LoadEach(ctx,
db, []*T, "Nav")`, `orm.From[T](db).Include("Nav").Fetch(orm.FetchSubSelect)`;
the `REL-004` guard is unreachable (a slice read cannot be intercepted), so
the Go expression of "unloaded is not empty" is the **nil vs empty slice**
distinction — a never-loaded collection is nil, a loaded-but-empty one is an
empty non-nil slice; singular navigations are nil pointers until loaded.
Recorded in §10; the spec already allowed it ("where the language cannot
intercept a property read").

**The audit: 21 questions, all clarifications, no rule changed.** The three
agents reported every unanswered question; each was checked against the
reference and the answer written into the spec under "Clarifications" —
`spec/query-ast.md`: projection resolves at build time; one root alias for
the whole select once anything aliases it; both ON sides resolve; membership
arity. `spec/loading.md`: the key-tiebroken ordering rule stated precisely
(paged SubSelect root: every key property not already ordered on, ascending,
applied to root and subqueries); SubSelect projection for composite owners;
the link→target hop chunks; duplicate owners; value-wise comparison per type
(decimals numerically, never by text); join alias numbering; first-appearance
root order; null-FK owners under joins; refusal precedence; composite link FK
order; and how a `viaQuery` replay selects its roots. Two of the questions
exposed **reference behaviour that was not an error code**: a join off an
undeclared parent alias threw `InvalidOperationException`, and a membership
arity mismatch was an index error — both are `QRY-006` now, pinned in C#.
Nothing in the spec was C#-shaped in a way that changed a rule; the gaps
were silences, and the port that could not peek is what surfaced them.

**The review pass found two real bugs** in the freshly written engines: the
join engine ordered `Decimal` keys by their text (the exact failure the spec
warns against — fixed with a numeric `Decimal.Compare`), and the batch
engine's arity check skipped a keyless related key, panicking on a keyless
owner for three kinds and full-scanning for a keyless many-to-one target —
`REL-003` now, matching join mode, pinned by four tests. Three duplicated
helpers were consolidated (render-and-run, key comparison, navigation
resolution).

**Verification.** Go: every conformance folder including `load-cases` (6
cases × explicit + 3 modes) and `ast/level2` (7 cases byte-identical);
`gofmt`/`vet` clean; 12 packages ok. C#: 316 passed (two new refusal tests),
22 skipped (SQL Server LocalDB and the Postgres live suite, no server
reachable today). PHP unchanged at Level 0–1.

**Level 2 exit.** Declared. Milestones 1–4 and 5b are done, M5 is outside the
level by ADR-0029, the spec sweep and benchmark refresh are recorded
(ADR-0029 add.1), and the proof stands: a port that never saw the reference
passes the same conformance files. What the exit does **not** claim: the PHP
port stays at Levels 0–1 (its Level 2 upgrade is a field decision for
Fidelis), a Rust port remains the last stress test in the ADR-0023 order, and
the non-SQLite dialects still lack shadow/diff tooling (ADR-0024/0025).

**Next target — the owner's call**, not this ADR's: Level 3 (session
semantics: identity map, snapshot change tracking, unit of work, hooks), the
PHP Level 2 upgrade, the Rust port, or the Level 4 dialect tooling.

**Status.** Accepted. Level 2 closed.

## ADR-0032 — After Level 2: the multi-language programme and the PHP Level 2 plan (2026-09-09)

Owner reframing: "the purpose of SimpleOrm is not focusing on Fidelis, but on
how it is implemented in many language types … to make uniformity for many
languages." Decisions taken one at a time:

1. **PHP goes to Level 2 next** (renderer projection/joins/`in_select` +
   `ast/level2` runner; `load`/`loadEach`; `include(...)->fetch(FetchMode::…)`
   in all three modes with join-mode row segmentation; `load-cases` runner;
   review pass; ADR with the gap list — the ADR-0031 method, agents forbidden
   to read `dotnet/` or `go/`). **PHP version unchanged.**
2. **The unloaded-collection guard in PHP — option 1, both forms.** The mapper
   `unset()`s a database-read entity's collection navigations, leaving the
   typed property uninitialized, so a read throws instead of returning `[]`
   (user-constructed entities keep their `= []` default — ADR-0021 add.2).
   With the opt-in trait `SimpleOrm\Session\Navigations` (a `__get` that
   fires because PHP routes reads of an unset declared property through it)
   the throw is `SimpleOrmException('REL-004', …)`; without the trait it is
   PHP's own `Error` — still a refusal, never a silent empty array. The trait
   is a §10 row (the library cannot add `__get` to a user class).
   **General rule for every port**, to be written into `spec/loading.md`: an
   unloaded collection must not read as empty; throw `REL-004` where the
   language can intercept, the language's own error where it can only refuse,
   and only where neither is possible (Go) fall back to a documented
   unloaded-vs-empty distinction.
3. **New ports cover Levels 0–2 in one go** — one foundation pass, one set of
   agents, one ADR per language; the spec has been proven twice.
4. **Port order after PHP: Rust → Python → TypeScript** (the honest name for a
   Node.js port: metadata needs decorators or typed descriptors, the package
   ships for JavaScript callers). Chosen for how hard each bends the spec:
   Rust (ownership of loaded graphs, `Result` everywhere, derive-macro
   metadata) hardest, Python (the async contract against a synchronous
   `sqlite3`, no static types) second, TypeScript third. Six implementations
   is the evidence the word "uniformity" needs.
5. **A porting guide** (`spec/porting-guide.md`: what to build in what order,
   the foundation-first method, the §10 divergence-table rule, the
   conformance folders per level) is written **after** those three ports,
   from the experience, not before.

Fidelis remains a consumer and a field test, not the target. Level 3/4 work
is not scheduled by this ADR.

**Status.** Accepted. Next session starts with the PHP Level 2 foundation.

## ADR-0033 — The PHP port reaches Level 2 from the spec alone (2026-09-10)

ADR-0032 decision 1, executed: PHP goes from Levels 0–1 to Level 2 by the
ADR-0031 method — the foundation first, then three Sonnet agents in parallel
**forbidden to open `dotnet/` or `go/`**, then a uniformity review, then every
gap resolved against the reference. PHP version unchanged (8.4).

**Foundation** (one commit, 335 tests green): the Level 2 AST types
(`SelectJoin`, `JoinPair` — PHP has no tuples — `SelectAst::$projection`/
`$joins`, `SubqueryMembership` + `Criteria::inSelect`), the pure enum
`FetchMode`, `Db::load`/`loadEach`/`resolveNavigation` (`REL-001`) with
`executeAst` as the one AST execution path, `include()`/`fetch()` on the
criteria chain with the key-tiebroken ordering for a paged SubSelect root,
two engine collaborators as stubs (`Session\DbLoading`, `Session\DbEagerJoin`
— PHP has no partial classes), and **the ADR-0032 guard in both forms**: the
mapper `unset()`s a database-read entity's collection navigations through a
closure bound to the entity's scope (`unset` is a write, so `private(set)`
refuses it from outside), and the opt-in trait `SimpleOrm\Session\Navigations`
turns the resulting `__get` into `SimpleOrmException('REL-004', …)`; without
the trait the read is PHP's own `Error` — never a silent `[]`. Four §10 rows.
The sample entities opt in.

**The three agents** (one commit, 401 tests): (a) the renderer's projection,
joins (`t`/`j<n>` aliasing, `<alias>_<column>` re-aliasing, both ON sides
resolved, `QRY-006` for an undeclared parent alias), `in_select` in row-value
and correlated-EXISTS forms behind `Dialect::supportsRowValueIn()`, and the
`ast/level2` runner — 7/7 pins byte-identical, Level 1 pins untouched;
(b) `DbLoading` for all four kinds with distinct-tuple chunking at 500,
`REL-002`/`REL-003`, SubSelect through `Criteria::inSelect`, the `load-cases`
runner replaying every case explicitly and in all three modes (18 rows);
(c) `DbEagerJoin` with `RowSegment` slicing each joined row per alias into the
existing `ResultMapper` entity plan, first-appearance root order, structural
deduplication, `REL-005`/`REL-006`/`REL-003`. Because they ran concurrently,
(b) and (c) built against the AST contract and re-checked the renderer when
they needed it; nothing blocked.

**The review pass** (one commit, 418 tests) consolidated five duplicated
concepts — key identity/equality/ordering into one `Metadata\KeyTuple`
(`equals`/`token`/`compare`, now also behind `EntityMap::keysEqual`),
collection ordering into `EntityMap::sortByKey`, navigation shape resolution
into `Session\NavigationShape` (both engines validate the same `REL-003`
shapes), navigation writes into `EntityMap::navigationProperty`, and the
criteria query name into `Db::criteriaQueryName` — and found **four real
bugs**: the batch engine ordered a `Decimal`-keyed collection by its text
(the Go-review bug again — SQLite sorts a TEXT-stored key textually, `'10'`
before `'2'`), join mode never validated FK/link mappedness so an `#[Ignore]`d
FK refused as `QRY-006` instead of `REL-003`, a join alias repeating an
earlier one (or the root's `t`) silently rebound a later join, and
`EntityMap::keysEqual` compared `Decimal`s by text. All pinned.

**The audit: 15 gap questions, 13 clarifications, three reference fixes.**
Every question was checked against the C# reference and answered under
"Clarifications (the PHP Level 2 port asked; ADR-0033)" in `spec/loading.md`
(eight) and `spec/query-ast.md` (five). Two of them exposed **the reference
disagreeing with its own spec**, fixed in C# and pinned: the join engine
checked a keyless root (`REL-003`) before `REL-006`/`REL-005`, against the
ADR-0031 precedence clarification; and one-to-many loading trusted SQL
`ORDER BY` on the target key, which is text order for a decimal key — the
very failure the spec warns against, previously fixed in Go and now found in
the reference itself by a pinning test that fails without the sort. The
third is new: alias uniqueness in a select is `QRY-006` in both
implementations (`spec/errors.md` widened accordingly). The remaining
questions were silences the reference already answered consistently
(distinct-tuple chunking, `REL-003` for every kind at load time, `REL-006`
before `REL-005`, post-fetch value-wise ordering in every mode, `REL-002`
under fan-out, subqueries through the dialect seam, the EXISTS rewrite only
for composite membership, projected joins selecting every target column, the
runner's navigation-name spelling and explicit-replay shape). No rule
changed; PHP's `SPEC-GAP` comments were rewritten as references to the
clarifications, as the Go port's were.

**Verification.** PHP: 418 tests / 1366 assertions green — every conformance
folder including `ast/level2` (7 byte-identical) and `load-cases` (6 cases ×
explicit + 3 modes); `NavigationsTest` pins the guard in both forms. C#: 317
passed, 22 skipped (SQL Server LocalDB and the Postgres live suite, no server
reachable today). Go unchanged.

**What the PHP Level 2 shape is.** `$db->load($e, 'user')`,
`$db->loadEach($list, 'transactions')`, `$db->from(User::class)
->include('transactions', 'profile')->fetch(FetchMode::SubSelect)->toList()`;
navigations stay `public private(set)`; the guard is the `unset()` +
`Navigations` trait pair. Four §10 rows describe the whole divergence.

**Next** — the owner's call, per ADR-0032 decision 4: the Rust port at Levels
0–2 in one go, only when asked.

**Status.** Accepted. PHP at Level 2.

## ADR-0034 — Level 3 before the remaining ports; the Level 3 milestone plan (2026-09-11)

Owner ruling, reversing the order in ADR-0032 decisions 3–4: **new-language
ports (Rust, Python, TypeScript) wait until the existing three implementations
are finished** through the levels the owner schedules. **Level 3 starts now.**
The uniformity programme is unchanged — it is served by carrying each level
through C#, PHP, and Go before a fourth language exists, so the spec each new
port receives is complete rather than growing under it. The porting guide
still follows the sixth implementation.

**What Level 3 is** (CLAUDE.md §1/§11 — "full session semantics comparable to
Hibernate but smaller"): a session-wide identity map; snapshot-based change
tracking; a unit of work with FK-ordered flush, batching, and cascades; simple
detach/merge rules; event hooks (audit, soft delete); optional inheritance
mapping; and draft migrations from the metadata diff with `[RenamedFrom]`
declarations. The brief's expectation stands: Level 3 shares a behavioural
spec and conformance cases, but each language's session API will diverge more
than Levels 0–2 did — the §10 tables will grow.

**Milestones (CLAUDE.md §7c) — one at a time, stop and report, never start
the next unprompted.** Each milestone: the reference first with its spec
section and conformance folder, then PHP and Go by the ADR-0031 method
(agents forbidden to read another implementation; gaps become Clarifications),
then a uniformity review — so the spec is proven per milestone, not at the
end. Every new error gets a code before a message; Level 3 opens the `UOW-`
prefix (unit of work and tracking) — `CRUD-`/`REL-` stay for what they name.

1. **Identity map + snapshot change tracking.** One instance per (entity
   type, key) per session, built on the §7.4 identity (`KeyTuple` in PHP,
   `KeyTupleComparer` in C#); every read path that materializes an entity
   (registry queries with entity results, key reads, criteria, loading,
   eager loading, join segments) consults it. Each tracked instance carries a
   snapshot of its mapped column values as read; the dirty set is the
   diff, and `Update` on a tracked entity writes only the dirty columns
   through the ADR-0028 column-list path (a clean entity writes nothing and
   says so). Owned types (ADR-0030) snapshot flat, like their columns.
   Open rulings for the owner at the start of M1: whether tracking is a
   session mode chosen at open (recommended: `DbOptions.Tracking`, default
   **untracked** so Level 0–2 behaviour, benchmarks, and the ports' existing
   suites are untouched; tracked is opt-in per session) or always on; and
   what a re-read of a known key does to a dirty instance (recommended: the
   tracked instance wins, the row's values are not applied — Hibernate's
   rule — with an explicit `Refresh` to overwrite).
2. **Unit of work.** `Persist`/`Remove` schedule work; `FlushAsync` writes
   inserts, updates, and deletes in **FK order** derived from relationship
   metadata (topological over `[ForeignKey]`/navigations, deletes reversed),
   batched (`DbBatch` under `#if NET`, one statement per row elsewhere),
   inside the session's transaction; generated keys flow into dependents'
   FKs before their inserts. Cascades are **declared, never inferred**
   (`[Cascade]` on a navigation, opt-in per operation). Concurrency stays
   row-level (`CRUD-010`). Conformance: `flush-cases/` — a graph of
   scheduled changes in, the exact ordered statement sequence out.
3. **Detach/merge + event hooks.** `Detach` stops tracking; `Merge` copies a
   detached instance's state onto the tracked one by key (or starts tracking
   it) — simple rules, no deep graph merging. Hooks are declared metadata,
   not registered callbacks: audit columns (`[CreatedAt]`/`[UpdatedAt]` set
   at flush) and soft delete (`[SoftDelete]` turns `Remove` into an update
   and makes the flag an explicit criteria filter the user asks for — never
   a hidden WHERE, §2). Before/after write hooks as an interface on the
   entity, if the owner wants them at all.
4. **Inheritance mapping (optional).** Single-table with a discriminator
   column only, if scheduled; table-per-type is out. The owner may skip
   this milestone entirely.
5. **Draft migrations from the metadata diff.** `[RenamedFrom("old")]` on a
   property or class feeds the ADR-0017 generator's `--rename` automatically;
   `simpleorm diff --draft` writes the version with a draft marker the
   author must remove after review. Small; extends existing tooling.
6. **Level 3 exit.** `spec/` covers every milestone (`spec/session.md`
   extended, new `spec/unit-of-work.md`), the conformance folders pass in
   all three implementations, the benchmark shows the untracked path
   unchanged and the tracked overhead measured, and a port could implement
   Level 3 from the spec alone.

**Not in Level 3:** lazy proxies (§2, permanent), second-level caching and
observability (Level 4), savepoints (Level 4), read/write splitting (Level 4).

**Status.** Accepted. Next session starts M1 with the two open rulings.

### ADR-0034 addendum 1 — the two M1 rulings (2026-10-16)

Owner rulings taken at the start of Level 3 M1:

1. **Tracking is a session mode, configured at open** (`DbOptions.Tracking`);
   **the default is untracked**, so every Level 0–2 behaviour, benchmark, and
   port suite is untouched. "Even if always on, it should be configured."
2. **A re-read of a key the session already tracks returns the tracked
   instance and does not apply the row's values** — the tracked instance
   wins, dirty or not; an explicit `Refresh` overwrites it from the row.

## ADR-0035 — Level 3 M1: the identity map and snapshot change tracking, in three implementations (2026-10-16)

The first Level 3 milestone (ADR-0034 §7c M1), done by the per-milestone
method ADR-0034 fixed: the reference first with its spec section and
conformance folder, then PHP and Go by agents forbidden to read another
implementation, then a uniformity review, then every gap resolved against
the reference and written into the spec. The two owner rulings of ADR-0034
add.1 — tracking is a session mode, default untracked; a re-read of a
tracked key returns the tracked instance without applying the row — are
what everything below builds on.

**The shape.** `DbOptions.Tracking = TrackingMode.Tracked` (PHP
`new DbOptions(…, tracking: TrackingMode::Tracked)`, Go
`orm.Options{Tracking: orm.TrackingTracked}`). A tracked session keeps one
instance per (entity type, key) — the §7.4 identity, value-wise — and a
snapshot of each instance's mapped values **in database-bound form** (the
type converter's output), so "dirty" means "the database would receive a
different value". Every read path that materializes a keyed entity
consults the map — registry queries and streams, key reads, criteria,
select-all, explicit and batch loading (link rows included), eager loading
in every fetch mode with join-mode roots and children. `IsTracked`,
`ChangesOf` (metadata order, dotted for owned members), `IsDirty`
(`UOW-001` for an instance the session does not track — every instance of
an untracked session), `RefreshAsync` (the explicit overwrite; `CRUD-001`
when the row is gone; both modes). Writes keep the map honest: `Insert`
and a full-row `Update` register with a snapshot of every column;
`Update` on a tracked instance writes only the dirty non-key/version/
generated columns through the ADR-0028 column-list path and **nothing at
all when clean**; `UpdateOnly` moves the snapshot for the written columns
and the version only (an unwritten change stays dirty) and does not
register an unknown instance; `Delete` forgets the key; every write and
the refresh pass the identity gate — an instance that is not the
session's instance for its key is `UOW-002` before any SQL (a key is
identity; it never updates). Nothing adds a round trip. `spec/session.md`
"Tracking", `spec/errors.md` `UOW-`, `conformance/tracking-cases/` (11
step scripts replayed in the declared mode; what an update wrote is
observed through the dialect seam, the one place every implementation
renders SQL).

**The ports.** PHP: `Session\DbTracking` + `TrackedInstance` beside the
Level 2 collaborators, `KeyTuple` as the one identity, `lcfirst` per
dotted segment in the runner; 32 new tests, 450 total. Go: `orm/tracker.go`
+ `orm/tracking.go`; **the pointer is the tracked shape** — `From[*T]`,
`Get[*T]`, `[]*T` navigations return the tracked instance, value-shaped
results are copies and never registered (a §10 row and now a spec
Clarification); 28 new tests. Both ports' default suites unchanged.

**The audit: 15 + 19 gap questions, one rule changed, four reference
fixes.** The PHP list found the reference less careful than the port in
four places, fixed in C# and pinned before the review ran: `UpdateOnly`
re-snapshotted the whole row (an unwritten change read as clean), the
entity form of `Delete` and `Refresh` skipped the identity gate, a key
reused after an external delete left a stale holder indexed, and the
snapshot held raw values so a JSON list mutated in place read as clean —
that last one became the rule: **snapshots hold the database-bound form**.
Every remaining question was a silence the reference already answered
consistently; the answers are the Clarifications in `spec/session.md`
(link rows tracked; live values in loading; the order of refusals on a
column-list update; a failing read keeps what it resolved; metadata order;
transactions leave the map alone; navigations are not dirty state; keyed
views tracked; re-reads still map the row; value-shaped results).

**The review pass** aligned both ports to the Clarifications — snapshots in
bound form (PHP dropped its `serialize` deep copy, Go its `reflect.DeepEqual`),
Go's `Refresh` through the identity gate and its link rows through the map,
Go's insert gate widened to every caller-supplied key — found one real bug
(PHP ran the identity gate before the list validation on `UpdateOnly`, so a
foreign instance with an unmapped name was `UOW-002` instead of `CRUD-005`),
and pinned the rest with 12 + 12 tests. Its one open finding became the last
Clarification: **a full-row `Update` validates every navigation (`CRUD-004`)
before the identity gate and before the snapshot** — the reference's order,
now in PHP and Go too, pinned in all three. Left as known: PHP cannot bind an
`array`-typed entity member read through its list JSON handler, so such a
member on a keyed entity refuses `MAP-030` at read time in a tracked session
(it could never be written either; no fixture has one); Go's tracker is a
sorted slice with O(n) registration, fine until the Level 3 exit benchmark
says otherwise.

**Verification.** C#: 340 passed, 22 skipped (SQL Server LocalDB and the
Postgres live suite, no server reachable). PHP: 463 tests, 1571 assertions. Go: every package ok (27 tracking tests + the
11 cases). The NuGet
audit started failing the build on a transitive `System.Security.Cryptography.Pkcs`
advisory in the meantime; the SQL Server dialect pins the patched version.

**Next — M2, the unit of work** (§7c), only when the owner says so.

**Status.** Accepted. M1 done in all three implementations.

## ADR-0036 — Level 3 M2: the unit of work, in three implementations (2026-10-16)

The second Level 3 milestone (ADR-0034 §7c M2), by the per-milestone method:
the reference with `spec/unit-of-work.md` and `conformance/flush-cases/`
first, then PHP and Go by agents forbidden to read another implementation,
then a uniformity review, then every gap resolved against the reference and
written into the spec.

**The shape.** On a tracked session (`UOW-003` otherwise): `Persist(entity)`
schedules an insert for an instance the session does not track and cascades
through navigations declared `[Cascade(Persist)]`; `Remove(entity)`
schedules a delete for a tracked instance, cancels a scheduled insert, and
cascades `Remove` over loaded collections — a never-loaded one is `UOW-005`
(nothing loads implicitly), a null singular navigation cascades nothing;
`FlushAsync` writes inserts in dependency order (a child after the scheduled
parent its navigation or cascade points at; types in foreign-key order,
cycles sharing a rank; then schedule order) with **generated keys flowing
into dependents** before each insert, then every dirty tracked instance as a
column-list update, then deletes children-first — inside the session's
transaction or one the flush opens and commits — and returns a summary. A
cycle is `UOW-004` before any SQL; a conflicting schedule `UOW-006`. **Every
statement is the existing generated CRUD** (one implementation per concept),
so the identity gate, the snapshots, `CRUD-004`, and the version rules apply
unchanged. **Cascades are declared, never inferred**: `[Cascade]` on a
many-to-one/one-to-one/one-to-many navigation, exported as `"cascade"` only
when declared, `MAP-025` anywhere else (a many-to-many's link rows are
entities the unit of work persists explicitly). The fixture
`Transaction.Details` cascades both. Batching (`DbBatch`) is an execution
detail the Level 3 exit benchmark decides; the statement sequence is what the
spec pins.

**The ports.** PHP: `Session\DbUnitOfWork` + `UnitOfWorkSchedule`,
`#[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]` (a
variadic pure enum — PHP has no flags), `ReflectionProperty::isInitialized`
as the "never loaded" test (the mapper `unset()`s unloaded collections);
26 new tests, 489 total. Go: `orm:"one_to_many=…,cascade=persist+remove"`,
`orm.Persist/Remove`, `db.Flush`, an undo journal; 28 new tests. Both share
one step-script engine with their tracking runners.

**The audit: 13 + 19 gap questions; six reference fixes; one rule taken
from the ports.** The reference was the weakest of the three on failure:
a flush that failed after some statements left memory half-applied (an
inserted instance tracked under a rolled-back key, a moved snapshot), so a
retry would have inserted twice — **the Go port's undo journal became the
rule**: a failed flush reverts the in-memory effects of every statement that
ran, and the whole schedule flushes again as it was. Also fixed in C#: a
scheduled insert already written immediately inserted a second time at
flush (now dropped as done), an empty flush opened a transaction, the update
phase ordered by dictionary enumeration (now a registration sequence), a
dirty tracked view instance was silently skipped (now `CRUD-003`), an empty
`[Cascade]` passed (now `MAP-025`), and type ranks broke cycles by
visitation order (now Tarjan components share a rank). Both ports had
independently decided that **a child persisted directly by name survives
its owner's cancellation**; the reference adopted it. The remaining
questions were silences the reference already answered consistently
(persist of a tracked instance still cascades; refusal orders; `UOW-005`
for never-loaded collections only; dependencies are navigations, never FK
values; keys flow only before inserts, copied as they are; deletes in the
entity form; `HasPendingWork` is a question; the runner conventions) — all
written as Clarifications in `spec/unit-of-work.md`.

**The review pass** aligned both ports to the Clarifications — Go's refusal
order and `MAP-025` for an empty cascade, PHP's undo journal
(`FlushJournal` + tracking checkpoints), `hasPendingWork` as a question, type
ranks as Tarjan components in both, as-is key copy, latest-registration
order — found two real PHP bugs beyond the known deviations (a scheduled
insert already written immediately inserted again; a parent scheduled by a
many-to-one persist cascade was not cancelled with its owner) and pinned
every rule with tests in both ports (16 + 10 new). Its one remaining
ambiguity — whether type ranks span only the flush's types or everything
reachable through relationships — was settled on the ports' reading (the
reachable closure), the reference aligned and the Clarification completed.
No `SPEC-GAP` comment remains.

**Verification.** C#: 360 passed, 22 skipped (SQL Server LocalDB and the
Postgres live suite, no server reachable). PHP: 505 tests, 1822 assertions. Go: every package ok.

**Next — M3, detach/merge and declared hooks** (§7c), only when the owner
says so.

**Status.** Accepted. M2 done in all three implementations.

## ADR-0037 — Level 3 M3: detach/merge and the declared hooks, in three implementations (2026-10-16)

The third Level 3 milestone (ADR-0034 §7c M3), by the per-milestone method:
the reference with `spec/hooks.md`, the "Detach and merge" section of
`spec/unit-of-work.md`, and two conformance folders first; then PHP and Go by
agents forbidden to read another implementation; then a uniformity review;
then every gap resolved against the reference and written into the spec.

**Hooks are declared metadata, never callbacks.** Three exist, read by the
same loaders as everything else, carried in the `EntityMap` and exported only
when declared (`"audit"`, `"softDelete"`): `[CreatedAt]` fills with the
**session clock** on insert when unset (a caller's value is kept);
`[UpdatedAt]` stamps on every write that changes a row — it joins a
column-list SET and a tracked dirty SET, never counts as what makes an
instance dirty, never stamps a clean update; `[SoftDelete]` turns a delete
(both forms, and a flushed `Remove`) into the column-list update that sets
the flag, version-checked in the entity form, `CRUD-012` by key on a
versioned entity. **Reads never filter on the flag** — no hidden WHERE (§2);
the flag is a criteria the caller writes. The clock is `DbOptions.Clock`,
UTC (`VAL-020` otherwise), fixed by the conformance runners at
`2026-08-29T00:00:00Z`. Declaration errors are `MAP-026`, which covers every
misplaced hook. The fixture: `BaseModel` carries both audit roles on every
sample table — so the existing CRUD, tracking, and flush pins gained the
stamp — and `Role.IsDeleted` is the flag, added by migration **V0011** in all
three sample trees.

**Detach and merge**, the simple rules: `Detach` forgets an instance and
cancels what was scheduled for it; `Merge` copies a detached copy's mapped
values — the version included, so a stale copy still fails its next update's
`CRUD-010` — onto the session's instance for its key (the tracked one, or the
row read now) and returns it; no deep graph merge, navigations untouched;
`CRUD-001`/`CRUD-002` for a missing row or an unset key — new instances are
persisted, not merged.

**The ports.** PHP: three attributes, `AuditRole`, `DbOptions::$clock`,
`detach`/`merge`, `ReflectionProperty::isInitialized` as "unset"; the sample
project's V0011 + snapshot; 40 new tests, 545 total. Go: tag options
`created_at`, `updated_at`, `soft_delete`, `Options.Clock`,
`orm.Detach`/`orm.Merge`, the builder declares hooks too; V0011 in the sample
migrations; the snapshot produced by its own CLI.

**The audit: 13 + 16 gap questions, six reference alignments, one rule
taken from a port.** The ports were more careful than the reference on
failure paths: a failed immediate write now puts the stamp (and a soft
delete's flag) back, and the flush's undo journal restores an undone
insert's `[CreatedAt]` fill — both from the Go port. Misplaced hooks are
`MAP-026` everywhere (the reference reported `MAP-019` for a hook without
`[Column]` and silently ignored one on an owned member); a hook on a
non-table source is `MAP-026` — the PHP port's stricter reading, the
`MAP-013` precedent; an empty client GUID is an unset key for merge; the
manual builder declares hooks (the Go port's addition), and hook validation
moved into the assembler so both loaders share one rule. The rest were
silences answered consistently (the language's notion of UTC, the clock read
per write, hooks after the gates, `CRUD-012` before the key validates, a zero
generated key as unset, detach's reach over scheduled work, merge's
preconditions) — all Clarifications in `spec/hooks.md` and
`spec/unit-of-work.md`.

**The review pass** aligned both ports — Go's hooks on non-table sources and
merge of a view (`MAP-026`, `CRUD-003`), PHP's `CRUD-012`-before-key order and
zero generated keys — found four more real PHP bugs (a refused update turning
into a `TypeError` on an uninitialized stamp; merge aliasing an owned object;
the two orderings), consolidated PHP's restore logic into one `remember()`
and its by-key soft delete onto the shared update path, and pinned every
Clarification in both ports (30 + 20 new tests). Two of its decisions went
beyond the Clarifications and both ports agreed, so the reference followed:
a failed immediate insert puts its `[CreatedAt]` fill back, and a misplaced
hook on an owned member reports `MAP-026` alone. No `SPEC-GAP` comment
remains.

**Verification.** C#: 380 passed, 22 skipped (SQL Server LocalDB and the
Postgres live suite, no server reachable). PHP: 575 tests, 2162 assertions. Go: every package ok.

**Next — M4, inheritance mapping (optional, single-table only) or straight to
M5, draft migrations** (§7c): the owner's call.

**Status.** Accepted. M3 done in all three implementations.

## ADR-0038 — Level 3 M5: draft migrations, in three implementations; M4 skipped (2026-10-16)

Owner ruling: **M4 (inheritance mapping) is skipped** — §7c marked it optional
and the owner chose not to take it; single-table inheritance stays off the
map unless a later ADR schedules it. **M5 is done** by the per-milestone
method: the reference with the spec section and the conformance additions
first, then PHP and Go by agents forbidden to read another implementation,
then a uniformity review, then every gap resolved into the spec.

**Two additions to the ADR-0017 generator, both declared, nothing inferred.**
`[RenamedFrom("old")]` on a `[Column]` property is a declared rename the
generator reads exactly as a `--rename table.old=new` flag — one
data-preserving `RenameColumn` instead of a drop and an add — but kept with
the model, so the intent survives the one `diff` run; it is **permanent and
harmless once applied**: when the latest snapshot already has the new
column, or has neither, it declares nothing. An explicit `--rename` stays
strict and wins. Exported as `"renamedFrom"`; `MAP-027` when misplaced.
`diff --draft` emits the version with a draft marker (C# `[Draft]` on the
root) that `migrate` and `baseline` refuse — **`MIG-013`, the whole plan,
before anything executes** — until the author has reviewed the code and
removed it; `status` reports `Draft`; `shadow` and `--amend` treat a draft
like any version. The fixture: `Role.Name` declares `name`, the column V0004
renamed it from — a rename the history already holds, so the declaration
declares nothing today, exactly the permanence the rule promises.

**The ports.** PHP: `#[RenamedFrom]`, `#[Draft]` on the root, `SqlVersion::draft(…)`
(a static factory — the variadic step list leaves no room for a flag),
`MigrationState::Draft`, `--draft`, a `replayDrafts` runner option for shadow;
17 + 2 new tests, 598 total. Go: `renamed_from=` beside `column`, a `Drafted`
interface the generated root implements (`func (V0012) IsDraft() bool`),
`SQLVersion.AsDraft()`, a marker-hiding wrapper for shadow. Both share the
diff core's second map for declared renames and report the state in `status`.

**The audit: 13 + 15 gap questions, six reference fixes.** The ports again
read the failure and edge paths more carefully than the reference: two
properties declaring the same previous column crashed the reference's
generator (a dictionary with a duplicate key) — now `MAP-027` at load; a
declaration whose old column the model still maps declared a rename of a
column that still exists — now it declares nothing (the Go port's rule); a
pending draft was not "pending" for `HasPending` and SchemaGuard's `MIG-030`;
`--amend` would have dropped an existing draft marker, letting unreviewed
code become applicable by regeneration (the Go port's catch); `shadow` would
have refused to replay a draft (both ports had built a tooling-only bypass —
the reference gained `MigrationRunner.ReplayDrafts`); and a declaration on
an owned member or a non-table source passed silently — now `MAP-027`, the
`MAP-026` precedent. The rest were silences answered consistently (export
order, "pending" for a recorded draft, `baseline N` reaching only `N`,
`migrate down` ignoring the marker, case-insensitive matching) — all
Clarifications in `spec/migrations.md`.

**The review** (one implementation per concept, both ports against the
Clarifications) found five port bugs and one order the spec had left open:
Go raised two properties declaring the same old column at diff time rather
than at load (`MAP-027`), and its `--amend` fixture lacked the kept-marker
case; PHP accepted a declaration on a view and on a second property with the
same old column, and exported `renamedFrom` before `softDelete`. Both ports
trimmed the declared name where the reference did not, and the three emitted
declared renames in three orders — the reference now **trims and emits in
ordinal order of the old column** (a Clarification), so a generated step is
byte-identical everywhere. The pins for a pending and a recorded draft, the
shadow replay, and "explicit `--rename` wins" exist in all three.


**Verification.** C#: 390 passed, 22 skipped (LocalDB and the Postgres live suite). PHP: 614 tests, 2270 assertions. Go: every package ok, `gofmt` and `go vet` clean.

**Next — the Level 3 exit** (§7c M6): the spec sweep, the conformance matrix
green in three implementations, the benchmark (untracked unchanged, tracked
overhead measured, the `DbBatch` decision), and the proof that a port could
implement Level 3 from the spec alone — only when the owner says so.

**Status.** Accepted. M5 done in all three implementations; M4 skipped.

## ADR-0039 — Level 3 exit: the spec-only audit, the tracked benchmark, the batching decision (2026-10-17)

§7c M6, executed. The exit criterion for a level is "a port could implement
it from `spec/` + `conformance/` alone". Levels 0–2 were proven by building
ports that way; Level 3 was built that way milestone by milestone (ADR-0035
to ADR-0038: the PHP and Go agents forbidden to read another implementation,
their gap lists resolved into Clarifications). The exit adds a proof of a
different kind — **an audit from a language family none of the three
implementations belongs to** — plus the two owed measurements.

**The audit.** One Sonnet agent, allowed to read only `spec/` and
`conformance/`, wrote the design of a **Python** Level 3 port — a synchronous
`sqlite3` driver, no static types, decorators and dataclasses, plain
attributes instead of properties, no overloading — and listed every question
the spec did not answer: **32 gaps** (6 high, 16 medium, 10 low), nine
registry rows narrower than the sections using their codes, six stale or
incomplete passages, eight rules no conformance case could detect. Every one
was answered against the reference and written into the spec. The high ones:
the **dialect seam** behind `expectWritten`/`expectStatements` was defined
nowhere; the **fixture's property and navigation names** (`UpdatedAtUtc` ↔
`updated_at`) were neither exported nor derivable; **"dirty" had two
definitions** that disagreed for the `[UpdatedAt]` column; `ChangesOf` was
silent on a value the converter refuses; **user code had no sanctioned way to
assign a navigation**; and **type rank was underspecified** for unrelated
types and the delete phase — and there the audit found a real divergence:
the reference numbered Tarjan's components in discovery order (a total order
by first appearance in the schedule) while both ports computed a depth, so
`Persist(role1); Persist(user); Persist(role2)` flushed role, role, user in
C# and role, user, role in PHP and Go, undetected by any case. **The depth
is the rule** — rank is a property of the metadata, unrelated types tie and
interleave in schedule order — the reference changed, and
`unrelated_types_interleave_in_schedule_order` pins it.

**Spec.** `spec/session.md` gained "The step scripts — grammar, values, the
fixture, the seam": the step-script form stated normatively for all four
Level 3 folders (verbs and fields, expectation kinds, paths, `expectError`
semantics), the value encoding and by-value comparison, the fixed clock for
every runner, the fixture table (entity → property → column, navigations,
hooks, the owned prefix), the fixture database (metadata-built, seeded, **no
foreign-key constraints**), and the recorder's exact contract. Clarification
blocks in `unit-of-work.md` (18 answers: the two dirties, conversion failures
in a question, navigation assignment, type rank as a depth, delete edges,
summary counting, `UOW-003` first, argument rules, failure propagation,
disposal, what does I/O and the sync-port reading of "async + cancellation",
removal discards edits, strong references, the whole-navigation copy of an
owned type in merge/refresh, "never loaded" ownership, delete's two forms,
token declarations), `hooks.md` (UTC without a `Kind`: a markerless instant
refuses, an offset instant converts as a value, the clock must be UTC
outright; declared type tokens; "unset" port-neutrally), and `migrations.md`
(the draft marker's neutral token `DRAFT`, `--amend` reading it from the root
file, rename plus type change, the case-format fields, `status`'s content).
`errors.md`: `UOW-001`/`UOW-003`/`UOW-004`/`MAP-025`/`MAP-026`/`MAP-027`
rows now carry their sections' full lists, `MAP-023` applies to any port
with untyped declarations, `MIG-030` names a pending draft. `crud.md` lost
its last "Level 3 is future" sentence and gained "Level 3 on these
operations"; `metadata-model.md` pins `cascade`'s position and order and
names the member-order hazard for languages whose reflection lists base
members first; `spec/README.md` and `conformance/README.md` now index every
document and folder (the never-created `schema/sqlite/` row is gone; the
Level 3 folders and the `renamedFrom`/`draft` fields are described).

**Conformance.** Five additions: `flush-cases/unrelated_types_interleave_in_schedule_order`
(the rank rule, and a many-to-one without a cascade scheduling nothing),
`merge-cases/merge_and_refresh_apply_the_owned_rule` (an owned navigation
copied as a whole — null for null, an object otherwise; `value` may now name
an owned navigation with `equals` `null` or `{}`),
`amend-cases/diff_draft_marks_the_root` and `amend_keeps_the_draft_marker`
(`command.draft`, `files[].draft`, the token `DRAFT` — M5's main feature had
no amend coverage), and a `down` step in `draft_refuses_to_apply` that pins
the refusal's atomicity (its final expectation had been the same whether or
not V0001 leaked through). Running them found **one more port bug**: PHP's
`--amend` read the draft marker from the compiled class, not from the root
file it regenerates — the two file-reading implementations and the spec now
agree — and **one more reference deviation**: C# merge and refresh copied an
owned type member by member and so turned a null navigation into an object
of nulls; both ports already copied the navigation whole.

**The tracked benchmark** (`TrackingBenchmarks`, ShortRun, SQLite WAL with
`synchronous=OFF` so the journal does not swamp the library, Ryzen 7 5700U):

| Path | Untracked | Tracked |
|---|---|---|
| 1000-row read, warm session (every row an identity hit) | 751 µs / 174 KB | 822 µs / 230 KB (1.09×, +32% allocation) |
| 1000-row read, fresh session (1000 registrations + snapshots) | 2.2 ms / 238 KB | 3.7 ms / 637 KB — about 1.5 µs and 0.4 KB per row, within a noisy run |
| get + update one row (Dapper by hand: 32 µs) | 33 µs (full row) | 33 µs (the one dirty column); **9.8 µs when clean — no statement** |
| 100 inserts, one transaction, session opened per call (Dapper: 519 µs) | 1.40 ms / 314 KB | `Persist` × 100 + `Flush`: 1.67 ms / 759 KB |
| nothing-to-do flush over 1000 tracked rows (the dirty sweep) | — | 922 µs / 259 KB, of which the read is 822 / 230: **~100 µs and ~30 KB per 1000 instances** |

The untracked path is unchanged: `QueryBenchmarks` still reads 1000 rows at
0.98× Dapper with 21% less allocation and single rows at parity (milestone 8:
0.97×, −22%). Two things were acted on. The dirty sweep allocated ~280 KB per
1000 instances before the exit — a `$"{Entity}.{Property}"` label string per
property per instance, built only for the converter's error message, plus a
list and a LINQ query per instance — now a per-session label cache and an
allocation-free `HasDirtyWritableColumn`; and the rank fix above. Noted, not
acted on: the untracked 100-insert path runs 2.7× a hand-written Dapper
transaction (the `RETURNING` reader and a command per row) — a Level 4
performance-pass candidate that has nothing to do with tracking.

**Batching — decided: the reference does not batch.** `Microsoft.Data.Sqlite`
reports `CanCreateBatch = false`, so `DbBatch` cannot help the reference
dialect at all, and the 100-row flush measures within the cost of the same
inserts issued one by one in one transaction. The statement sequence stays
the contract (`flush-cases`); a dialect whose provider batches (SQL Server,
PostgreSQL) may adopt `DbBatch` under `#if NET` in the Level 4 performance
pass, measured there. Recorded in `unit-of-work.md` "Batching".

**Verification.** C#: 394 passed, 22 skipped (LocalDB and the Postgres live
suite). PHP: 618 tests, 2351 assertions. Go: every package ok, `gofmt` and
`go vet` clean. The four Level 3 folders plus `diff-cases`, `migrations-cases`,
and `amend-cases` run in all three implementations.

**Level 3 exit.** Declared. M1–M3 and M5 are done in three implementations,
M4 skipped by owner ruling (ADR-0038), the spec covers every milestone with
the audit's silences answered, the conformance matrix is green, the
untracked path is unchanged and the tracked overhead measured, and a port
from a fourth language family has a design to start from. What the exit does
**not** claim: that the Python design was built — the proof is the audit's
list and its answers, not a fourth implementation; and that a long-lived
tracked session is free — the sweep is linear in the tracked set by design,
as in every snapshot-tracking ORM.

**Next — per ADR-0032/0034: the new-language ports, Rust → Python →
TypeScript**, each from `spec/` + `conformance/` alone, with the agents
forbidden to read any implementation — the Python audit above is that port's
first gap list already answered. Only when the owner says so.

**Status.** Accepted. Level 3 closed in C#, PHP, and Go.

## ADR-0040 — The Level 4 dialects reach the ports: SQL Server and PostgreSQL for PHP and Go, built from the spec alone (2026-10-17)

Owner: give the PHP and Go ports the SQL Server and PostgreSQL dialects at the
scope the reference has today (ADR-0024/0025, runtime-first), by the porting
method (ADR-0026/0027/0031/0034: foundation contracts first, parallel Sonnet
agents forbidden to read another implementation, a uniformity review, gaps
resolved against the reference into spec Clarifications). Snapshot, shadow,
diff, and force-sync tooling stays SQLite-only in every implementation — the
reference has not done it either, and this ADR does not pretend otherwise.

**The spec had no dialect document.** ADR-0024/0025 recorded the SQL Server and
PostgreSQL decisions in prose and `conformance/ast/` pinned their SELECT
renderings, but storage types, DDL guards, generated-key retrieval, the run-lock
statements, the version-table DDL, the migration-action grammar, the `VAL-011`
tables, the temporal marker rule per dialect, and the introspection result
shapes lived only in `dotnet/`. A port forbidden to read `dotnet/` would have had
to invent them. So the foundation of this pass is **`spec/dialects.md`** — the
seam member by member, the capability-flag table, every rendering of the three
realized dialects (the reference text given so implementations stay uniform
rather than inventive; the `ast/` pins are what must match byte for byte), the
array-parameter and native-temporal-binding contracts restated for drivers
without typed parameters, the live-testing contract (`SIMPLEORM_POSTGRES` /
`SIMPLEORM_SQLSERVER`, a `simpleorm_test_<hex>` database per fixture, skip as a
group), and the shared deferred list. Beside it: a name→dialect registry in each
port (`Dialects` in PHP, the `dialects`/`astDialects` maps in Go) that the CLI's
`--dialect` and the `ast/` runners share — the runners now render every case
through every shipped dialect and fail on a missing expectation, as the
reference runner does; live-test fixtures that probe once per process; the PHP
converter made tolerant of the drivers' temporal text (`+07`, ` +00:00`,
fractional `time`) and `bytea` streams; and the Go `core.TemporalMarkers`
optional capability (below).

**Drivers** (CLAUDE.md §4, owner-approved 2026-10-17): Go `github.com/microsoft/go-mssqldb`
v1.11.2 (Microsoft-maintained, pure Go, TDS over `database/sql`, Windows
integrated authentication, `@name` parameters natively) and
`github.com/jackc/pgx/v5` v5.11.0 through its `stdlib` adapter (pure Go, typed
slices as array parameters, `Prepare` exposes field descriptions for
SchemaGuard; `lib/pq` is in maintenance mode and has neither) — the module stays
cgo-free, as ADR-0027 wanted. PHP needs no package: `pdo_pgsql` and
`pdo_sqlsrv` (or `pdo_odbc` with the SQL Server ODBC driver) are extensions,
listed under `composer.json` `suggest`; `composer test-live` loads them.

**The shapes each language forced** (the §10 rows, in one paragraph each):

- *PHP.* PDO sends every parameter untyped and binds no arrays and no objects,
  so `SupportsArrayParameters` is realized as **one PostgreSQL array literal**
  under `PARAM_STR` (`Parameters\PostgresArrayLiteral`: every string
  double-quoted and backslash-escaped, numbers bare, `NULL`, `{}`; PostgreSQL
  infers the array type from the comparison), and `BindsTemporalsNatively`
  changes nothing observable — the converter hands back the UTC
  `DateTimeImmutable`, the binder sends the ISO-8601 string, and
  `timestamptz >= text` never arises because the parameter is untyped. The
  marker rule is judged on the value's text (PDO returns every temporal as a
  string). The generated key is read by **position** from the first result set
  that has columns (`Db::runInsert` advances with `nextRowset()` — SQL Server's
  `scope_identity()` scalar is unnamed and follows the insert's row-count
  result). SchemaGuard's shield runs each registry entry under a **savepoint**
  on PostgreSQL, which aborts the whole transaction on any error. The run lock
  opens with a raw `BEGIN` because the runner closes with raw `COMMIT`/`ROLLBACK`.
  `SqlServerDialect` takes a `sqlsrv:` or `odbc:` DSN verbatim and never
  emulates prepares.
- *Go.* pgx's `database/sql` adapter accepts only `$n` positionals, so
  `orm/postgres` wraps the connector in a package-local `driver.Connector` that
  **rewrites `@name` to `$n`** (through the port's placeholder scanner, so
  literals and comments are untouched) — the seam gains no member for it.
  `DescribeStatement` prepares the unnamed server statement and resolves names
  with one catalog query (PostgreSQL) or runs
  `sp_describe_first_result_set … @browse_information_mode = 1` with parameter
  types from `sp_describe_undeclared_parameters` (SQL Server). A Go `time.Time`
  always carries a location, so a driver-native temporal has no "unspecified"
  state: the new optional capability **`core.TemporalMarkers`** lets the dialect
  judge a column's driver-reported type name, and the result mapper refuses a
  markerless column read into a temporal property with `VAL-020` on every
  materializing path (query, get, criteria, scalar, DTO, join segments) — the
  marker lives in the column type, not the value. Array parameters bind as
  **typed slices** (an empty collection typed from the declared element type);
  go-mssqldb returns decimals as ASCII bytes (`toDecimal` accepts `[]byte`);
  under native binding `time` stays text (pgx cannot encode `time.Time` for a
  `time` parameter) and `date` binds as the calendar date.

**Reference deviations the ports surfaced — fixed in C# and matched everywhere:**

1. An unpaged subquery kept its `ORDER BY`. SQL Server rejects `ORDER BY` in a
   subquery without `TOP`/`OFFSET` (error 1033), so an ordered, unpaged
   SubSelect root could never have run there; where `PagingRequiresOrderBy` the
   renderer now omits an unpaged subquery's orderings (membership is
   order-insensitive). Pinned on all three dialects by
   `conformance/ast/level2/subselect_unpaged_ordered.json`.
2. SQL Server `CreateTableSql` rendered `nvarchar(max)` for `users.email` and
   `roles.role_name`, which the sample declares unique indexes on — not
   indexable (error 1919), so the reference's own live `OpenAsync` would have
   failed on a working engine. `CREATE TABLE` now gives string and bytes
   columns of a declared index the indexable `(450)` length; `StorageType`
   keeps the key-only rule (snapshots and diff are SQLite-only there anyway).
3. `TableActions.DropIndex` bypassed the seam (`"drop index " + name` while
   ADR-0024 added `DropIndexSql` for exactly this), and the view actions wrote
   the view name unquoted — `drop view order` fails on SQL Server. Both now
   render through the dialect, SQLite byte-identical (checksums untouched).

None of the three was reachable by the SQLite suite, and the SQL Server live
suite has never run on a working engine (ADR-0024's LocalDB caveat still holds
on this machine). Two ports built from the spec found them in an afternoon.

**Review alignment.** The uniformity review (one agent over the whole tree, this time allowed to read the reference) found every seam member of the four dialects byte-identical to C#; made introspection aliasing one rule in all three implementations (the reference's SQL Server and PostgreSQL queries alias their result columns too now, pinned by unit tests everywhere); moved PHP's pgsql savepoint inside `validateStatement` so statement-backed entities are covered like registry entries; found a **pre-existing PHP Level 1 bug** — `date`/`time`/`datetimeoffset` tokens were written as a full UTC `datetime` (unreadable back on SQLite, refused by a PostgreSQL `time` column, the offset lost) — fixed by passing the token to `toDatabase`, which also made the two ports agree on the calendar rule (Clarification 5); added the fetch-mode-parity and lock-exclusivity live tests to both ports; probed `to_regclass('order')` live and retired the quoting worry; merged the §10 rows and wrote the READMEs' dialect sections.

**Conformance.** `conformance/ast/**` renders through three dialects in every
implementation (PHP and Go runners enumerate their registries; a case lacking a
shipped dialect's expectation fails); one new case. `migrations-cases/` and
`cases/` stay SQLite — the live legs are the per-dialect proof, as in the
reference.

**Verification.** PostgreSQL 16.4 live on this machine: PHP 24 live tests
and Go 23 live tests green — key write-back, the `timestamptz` marker, array
parameters filled and empty, temporal criteria, paging, reserved words,
concurrency, transactions, explicit/batch/link loading, SubSelect over a paged
root, composite row-value IN, materialized views, migrations under the advisory
lock, `VAL-020` on `timestamp`, a tracked flush with hook stamps, SchemaGuard.
SQL Server: 16 PHP and 22 Go live tests **written from the spec and never
executed** — no reachable engine here (LocalDB broken, nothing on 1433, and
go-mssqldb cannot address LocalDB at all); the rendering surface is fully
tested without a server and every `sqlserver` pin passes. C# 409 passed /
10 skipped (the PostgreSQL live suite ran); PHP 777 tests (16 skipped); Go every package ok.

**Spec.** `spec/dialects.md` (new) with a Clarifications block of 13 answers
consolidating the four gap lists (42 questions); `spec/query-ast.md` the subquery ORDER BY rule;
`spec/README.md`, `conformance/README.md` indexed.

**Deferred, named** (the same list in every implementation, plus what this pass
could not verify): snapshot/shadow/diff/force-sync beyond SQLite; the `MIG-012`
view guard against `sys.sql_modules`/`pg_get_viewdef` output; `bytea` **writes**
through PDO (`PARAM_STR` parses the payload as escaped text — needs `PARAM_LOB`
or hex encoding; no sample entity has a bytes column); go-mssqldb binding `nil`
as `nvarchar(1)` NULL (a NULL into `varbinary` may refuse, error 257) and
summing trigger row counts into `RowsAffected`; `to_regclass(@relation)` /
`object_id(@relation)` taking an unquoted name (a reserved-word relation name
would not introspect — all mapped names are snake_case); pgx's statement cache
across schema changes in a long-lived session; the SQL Server live legs' first
green run, on a machine with an engine.

**Status.** Accepted. The three Level 4 dialects exist in three implementations
at the reference's runtime-first scope; the spec now describes them.
## ADR-0041 — Inheritance mapping: concrete classes only; a base class is never an entity (2026-10-17)

Owner rulings, taken in a design discussion held before any code (the
standing rule: discuss first, build only when scheduled). ADR-0034 left M4
optional — single-table inheritance with a discriminator, nothing else —
ADR-0038 skipped it, ADR-0039 closed Level 3 with inheritance "off the map
unless a later ADR schedules it". This is that ADR, and it schedules the
opposite: **mapped inheritance is ruled out permanently**, and the one form
the library already has — a base class that shares members — gets the
strictness it lacked.

**The discussion.** Single-table inheritance was walked through subsystem by
subsystem against the code as it stands: a discriminator block on the root
`EntityMap` and `root`/`discriminatorValue` on each subtype; a dispatching
mapper plan whose strictness becomes per-row (`MAP-001`/`MAP-002` relaxed to
the hierarchy's union, a new code for an unknown discriminator value); the
discriminator written on insert as a library-filled property; a subtype
predicate that is either rendered into generated SQL and pinned in
`conformance/ast/` or written by the caller as explicit criteria (the
soft-delete shape, ADR-0037); the identity namespace moved from the exact
runtime type (the ADR-0039 Clarification) to the hierarchy root, with a new
`UOW-002` case; the hierarchy ranking as one node in the flush order; the
union snapshot with subtype-only columns forced nullable (the ADR-0030
precedent); two SchemaGuard rules; a fixture hierarchy in three sample trees
and three migration trees. Nine subsystems, nine conformance folders
extended, a new spec document — about M2 and M3 together, in three
implementations by the per-milestone method.

**Why it is out, permanently.** Four reasons, any one sufficient:

1. **A navigation typed to a hierarchy root is a polymorphic association**,
   which ADR-0019 ruled out permanently. Single-table inheritance either
   reopens that ruling or forbids base-typed navigations, which guts it.
2. **Two of the six target languages have no subclassing.** Go embedding is
   composition (a `[]*Document` cannot hold an `*Invoice`); Rust would model
   the concept as an enum with a variant per subtype. A language-neutral
   spec would have to define "a row whose type is one of a closed set of
   variants, chosen by a column" — a sum type — and the class-family
   encoding would be the odd one, not the rule. Writing the spec from the
   C# shape would be exactly the family bias the uniformity programme
   (ADR-0032) exists to catch.
3. **Identity would stop being the instance's exact runtime type** — the
   rule every Level 3 implementation and the Python audit settled on — and
   every tracking, flush, merge, and refresh path would carry a hierarchy
   lookup for a feature the fixture does not need.
4. **The need is already met without library code.** A `Kind` enum column
   plus one nullable `[Owned]` navigation per variant (ADR-0030: subtype
   columns under a prefix, an all-NULL group reads as null, the caller
   switches on `Kind`, the variant filter is `Criteria.Eq("Kind", …)` —
   the soft-delete shape); or a write model on the table plus one keyed
   `[View]`/`[Statement]` read model per variant (ADR-0008: SQL-first,
   read-only, trackable, with its own navigations). Both go into the
   README as the "Variants" patterns.

**Table-per-type (TPT)** is ruled out permanently as well: every base read
becomes a join over every subtype table — a hidden query in substance (§2) —
and every subtype insert becomes two statements whose generated key must
flow across the hierarchy inside the unit of work. **Table-per-concrete
(TPC)** is not ruled out, because it is what the library already does when
the base class is not an entity — see the next ruling. Its polymorphic form
(a `UNION ALL` read over the concrete tables, a key space shared across
them) is out: SQLite's per-table `INTEGER PRIMARY KEY` makes the (type, key)
identity impossible across tables, and the AST has no union.

**The ruling: identity comes only from the real class.** A concrete class is
an entity: its own relation, its own key space, its own identity. A base
class shares members and nothing else — its `[Column]`, `[Key]`,
`[Generated]`, `[Version]`, hook, and `[RenamedFrom]` declarations are
inherited by every concrete subclass as if declared there (the fixture's
`BaseModel` audit columns; `metadata-model.md`'s inherited-properties rule),
each concrete table carrying its own copy of every column. **A base class
is never read, written, queried, exported, validated, or targeted by a
navigation. A read through a base class throws** — the owner's choice over
silent exclusion, §2 "strict by default". A base class is a type that is
abstract, or that another mapped type extends.

**`MAP-028` — a base class used as an entity.** Reserved now in
`spec/errors.md`; raised by the loader (and therefore by every path that
loads a map: registry queries with an entity result type, key reads,
criteria queries, writes, explicit and eager loading, the builder, the
tracker, SchemaGuard, `export-metadata`) in these cases:

- an **abstract class** loaded as an entity (a query, key read, criteria
  query, write, or navigation target typed to it; a builder registration
  of it);
- a mapped type whose **base class carries a relation source** of its own
  (`[Table]`/`[View]`/`[MaterializedView]`/`[Statement]`/`[Procedure]`) —
  the base would be an entity, so it may not be extended by one; the error
  names both classes. The loader checks this while walking the chain it
  already walks for inherited properties, and must read class-level
  attributes **without** attribute inheritance (C#'s default
  `GetCustomAttribute` inherits), so a subclass never silently adopts its
  base's table;
- in an assembly scan (SchemaGuard, the CLI), a **concrete class without a
  source that another scanned type extends** — the one case a lone load
  cannot see, because a concrete class with `[Column]` properties and no
  source legitimately maps to a convention-named table. Startup catches it
  (`ValidateAsync` runs in every environment, §7.21); declaring base
  classes `abstract` is the documented convention.

What exists today, for the record: scans already skip abstract classes
(`SchemaGuard`, `Program`), but `Query<BaseModel>()` loads a convention
table `base_model` and fails with the provider's "no such table",
`GetAsync<BaseModel>` fails with `CRUD-002` (the wrong name), and a concrete
base class carrying `[Key]` loads, exports, and validates as an entity
silently.

**Ports.** Go: unreachable — embedding is composition and an embedded
struct's fields are the struct's own; recorded like `MAP-023` in C#, a code
that exists for the languages that need it. PHP: `abstract` classes and
`get_parent_class`, the same two checks as C#. Python and TypeScript, when
their ports come: reachable, same rules. Rust: unreachable.

**What changes, and when.** This ADR records the ruling, reserves the code,
and updates the brief (§3 row, §7c M4, §11). The implementation is an
addendum-sized change, not a milestone, done by the usual method — the
reference with the spec sentence and the conformance case first, then PHP
and Go by agents forbidden to read another implementation, then a review:

- the loader rule in `AttributeMapLoader` (the base-chain walk; attribute
  reads without inheritance) and the builder, plus the scan rule in
  SchemaGuard and the CLI;
- `metadata-model.md`: one paragraph beside the inherited-properties rule
  ("a base class is never an entity"; the abstract convention);
  `mapping-rules.md`/`validation-rules.md` rows; the README "Variants"
  section with both patterns;
- conformance: at least `cases/map028_read_through_a_base_class` (a query
  whose result type is the fixture's `BaseModel`, `expect.error`
  `MAP-028`) — the declaration rule pinned by each implementation's own
  tests, and as a conformance case if a fixture-free form is found;
- tests in C#, PHP, and Go for every case above.

Nothing starts unprompted (CLAUDE.md §1).

**Status.** Accepted. Inheritance mapping (single-table, table-per-type,
polymorphic table-per-concrete) ruled out permanently; base classes share
members only; `MAP-028` reserved; implementation not started.

### ADR-0041 addendum 1 — MAP-028 built in three implementations (2026-10-17)

The addendum-sized change the ADR scheduled, done by the usual method: the
reference with the spec text and the conformance case first, then PHP and Go by
agents forbidden to read another implementation, then a review that aligned
the reference to what the ports had read into the spec.

**What was built.** C#: the one map-loading entry point (`EntityMapLoader`)
refuses an abstract type before any loader runs, so every path that loads a map
— registry result types, key reads, criteria queries, writes, explicit and eager
loading of a navigation target, the tracker, SchemaGuard, `export-metadata` —
raises `MAP-028`; `MappingOptions.Register` refuses an abstract registration
where it is written; the attribute loader reads every class-level attribute
without inheritance (`inherit: false` — relation sources, `[Index]`, `[Owned]`)
and reports an ancestor carrying a relation source **or an `[Index]`** with both
class names and the declarations found, collected beside the type's other
errors and applied under every loader (attribute, convention, manual builder);
the scan rule (`EntityMapLoader.BaseClassesUsedAsEntities`) finds a concrete
sourceless mapped class that another scanned class extends — SchemaGuard reports
it once and does not validate it as a relation, and `ScannedEntityTypes` makes
every CLI command that enumerates entities (`export-metadata`, `snapshot`,
`diff`, `migrate --force`, `shadow`) refuse before doing anything. PHP: the same
three checks in `Metadata\BaseClassRules` (`isAbstract()` — interfaces
included — and `getParentClass()`; PHP's `getAttributes()` never inherits, so
"read without inheritance" needed no code), wired into the entry point, the
builder, the registrations, SchemaGuard, and `Cli\Application::mappedTypes()`.
Go: the abstract-class and scan forms are unreachable (a struct type is always
concrete; nothing extends anything; registration is explicit) and are recorded
beside `MAP-023` and as a §10 row — but the agent found and closed the one
reachable analog: **method promotion** carried an embedded struct's `Entity()`
descriptor onto the embedding struct, which silently adopted the embedded
struct's table; `refuseEmbeddedEntities` now reports every such pair with
`MAP-028` naming both types (the deepest declaring struct through nested
embedding) and never reads the promoted descriptor, while a plain embedded
struct (`sample.BaseModel`) keeps sharing fields only.

**Found on the way.** SchemaGuard (C# and PHP alike) let a `MappingException`
for a registry entry's result type escape instead of becoming report rows — any
loader failure, not only `MAP-028`; both now add one row per violation with the
entry as the source and skip that entry's per-column checks.

**Implementation rulings beyond the ADR's letter, for the owner to confirm:**
`[Index]` on a base class is treated like a relation source (with class-level
attributes no longer inherited the alternative was a silently dropped index,
which §2 forbids); an abstract ancestor carrying `[Table]` is `MAP-028` on the
concrete subclass (the base's source names it an entity — it was silently
inherited before); `[Owned]` on a class is read without inheritance too.

**Spec.** `metadata-model.md`: the paragraph "A base class is never an entity"
beside the inherited-properties rule, and a Clarifications section answering
the ports' 15 + 7 gap questions (interfaces are abstract; where the check runs;
a navigation typed to an abstract class — the owner's map loads, every load of
the target refuses; the parent rule under every loader, one error per ancestor;
"read without inheritance" is vacuous in PHP and an instruction against Go's
promotion; the scan rule's candidate and extender sets, chains, the invisible
outside ancestor, what each CLI command does; the registry-entry report rows;
what the conformance runner pins where the code is unreachable).
`mapping-rules.md` Strictness bullet, `validation-rules.md` row and item 2,
`errors.md` row completed, `conformance/README.md` `cases/` row (`result` may
name `BaseModel`), README "Variants" with both patterns (owned variants with a
kind column; a write model plus `[View]`/`[Statement]` read models).

**Conformance.** `cases/map028_read_through_a_base_class.json` (`"result":
"BaseModel"`, `expect.error MAP-028`) runs in all three runners — C# and PHP
refuse at the map load before any row is read; Go resolves the name to the
embedded `sample.BaseModel`, pins that it is embedded by value in the fixture
entities, and passes by construction without running the query. No fixture-free
conformance form exists for the declaration rules (`cases/` needs a fixture
result type and there is no loader-error folder): they are pinned by each
implementation's own tests (C# `BaseClassRuleTests`, PHP `BaseClassRulesTest` +
`BaseClassScanTest`, Go `metadata/map028_test.go`).

**Counts.** C# 413 passed / 10 skipped (was 394 / 22; the live Postgres leg ran on this machine); PHP 646 (was 618); Go `go vet` + `go test ./...` ok.

**Status.** Accepted; the ADR's implementation is complete. Nothing else
inheritance-related changed: single-table and table-per-type stay out
permanently.

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

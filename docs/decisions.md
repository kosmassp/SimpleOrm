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

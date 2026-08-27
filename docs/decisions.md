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

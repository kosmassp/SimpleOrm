# Migrations — versioned code, per-object history

Migrations are **code, never external .sql files** (ADR-0013), organized per object,
recorded per version. Apply is always an explicit act — the application never
migrates at startup (it only checks, `MIG-030`). Generation is an *authoring* tool
(the post-milestone-6 diff generator writes these artifacts); there is no automatic
schema sync against shared databases.

## Structure

```
Migrations/
  V0001.cs                          root version — the recorded, checksummed unit
  V0002.cs
  Table/<Object>/V000N_<Desc>.cs    per-object steps
  View/<Object>/V000N_<Desc>.cs
```

- A **root version** (`V<version>`, `MIG-001`) composes its object steps in explicit,
  reviewable order (tables in FK order, views after). Duplicate root versions or an
  object composed twice in one version: `MIG-002`.
- An **object step** (`V<version>_<Description>`, `MIG-001`) must carry the version
  of the root composing it (`MIG-003`); a step no root composes is an error
  (`MIG-004`) — nothing is silently skipped.
- Discovery scans the assembly, optionally filtered to a migrations namespace; one
  namespace (folder) per database in multi-database apps.

## Steps and actions

Table steps expose ordered actions: whatever the declaration order, execution is
**rename → add → remove → raw SQL** (declaration order inside each group) — renames
running first is what makes a rename expressible without data loss. View steps run
actions in declaration order (create / drop / recreate / raw SQL).

Every action takes optional **per-action data hooks**: `Pre(sql)` runs immediately
before it, `Post(sql)` immediately after (e.g. preserve values before a remove,
backfill after an add, seed after a create). Hooks are inline SQL and share the
version's atomicity.

Column specs in hand-written actions are **literal** (name, type, nullability,
default) so an applied migration's rendered SQL never changes when the model
evolves. Metadata-rendered DDL (`CreateTable()`/`CreateView()`) is legal for an
object's initial creation, frozen thereafter by the checksum.

## Recording and integrity

Table `schema_version` (STRICT on SQLite):

```
version INTEGER, object TEXT, description TEXT, checksum TEXT,
applied_at TEXT (ISO-8601 UTC), execution_ms INTEGER, primary key (version, object)
```

One row per (version, object); a version is applied atomically. The checksum is
SHA-256 over the step's rendered Up statements. Every run validates the whole plan
**before executing anything**: drift on any applied step is `MIG-010`; history rows
unknown to the code are `MIG-011`; `migrate down` past any step lacking Down
statements is `MIG-020` — checked for the full range before the first statement.

## Locking and atomicity

The dialect provides the run lock (§7.25). On SQLite the entire run executes inside
one `BEGIN IMMEDIATE` transaction — exclusive writer, and with transactional DDL a
failed run rolls back completely (no partially applied versions). Dialects with
advisory locks (Level 4 Postgres) evolve this member.

`baseline <N>` records versions ≤ N as applied without running them — the
fresh-database path pairs it with metadata-generated schema
(`CreateTableAsync`, dev/test) or a restored dump.

## Conformance cases

`conformance/migrations-cases/*.json`: a migration set as data plus commands and
expectations. Each implementation feeds the set through its runner (the
`SqlVersion` form) against a fresh database:

```json
{ "name": "…",
  "versions": [ { "version": 1, "steps": [
      { "object": "widgets", "description": "create", "up": ["…"], "down": ["…"] } ] } ],
  "run": [
    { "command": "migrate", "expect": { "applied": [[1, "widgets"]] } },
    { "command": "down", "to": 0, "expect": { "applied": [] } } ] }
```

A `run` step may override `versions` (drift scenarios). `expect` is either the
exact `(version, object)` rows recorded afterwards or an error code.

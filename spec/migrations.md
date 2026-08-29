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
unknown to the code are `MIG-011`; a `migrate down` step whose rollback can
neither be derived from the snapshots nor comes from a `Down()` override is
`MIG-020` — checked for the full range before the first statement. A statement
that fails mid-run reports as `MIG-021`, naming the SQL.

## Derived rollbacks (ADR-0018)

Steps carry **no down DDL** — "Down could be deducted from the previous schema."
At `migrate down` time the runner resolves, per step, the object's snapshot at
that version and the latest one before it, then derives the reverse:

- no earlier snapshot → the version created the object → drop it;
- tables → the step's typed `RenameColumn` actions invert first (snapshots alone
  cannot tell a rename from a drop+add; the actions can, and inverting them
  preserves data), then the shape diff: dropped columns restored (a NOT NULL
  restore lands nullable with a notice — constraint and data are not derivable),
  added columns dropped, indexes reverted structurally;
- views → expected-definition guard on the current DDL (`MIG-012` — a rollback
  must not silently destroy an outside hotfix either), drop, previous definition
  restored;
- a same-column type/nullability change is not derivable → `MIG-020`.

`Down()` remains the manual override and always wins; `PreDown`/`PostDown` hooks
wrap whichever core runs and carry the data work (a data-only step derives an
empty rollback — schema-correct; its inserted rows are the hooks' job). The
snapshots ship **embedded in the migrations assembly**
(`<EmbeddedResource Include="Migrations\**\*.schema.json" />`) so rollbacks work
deployed; the CLI's `--snapshots <dir>` reads them from source instead.

## Locking and atomicity

The dialect provides the run lock (§7.25). On SQLite the entire run executes inside
one `BEGIN IMMEDIATE` transaction — exclusive writer, and with transactional DDL a
failed run rolls back completely (no partially applied versions). Dialects with
advisory locks (Level 4 Postgres) evolve this member.

`baseline <N>` records versions ≤ N as applied without running them — the
fresh-database path pairs it with metadata-generated schema
(`CreateTableAsync`, dev/test) or a restored dump.

## Snapshots (schema history as data)

Each object's shape is recorded per version in
`Migrations/<Kind>/<Object>/V000N.schema.json` (ADR-0016/0017). Tables snapshot by
**columns**; views, materialized views, and procedures snapshot by **DDL** — they
self-reflect from their defining SQL, so the definition *is* the schema (ADR-0017
add.1). Statements are queries, not schema objects: no snapshots.

```json
{ "object": "roles", "asOfVersion": 4, "generatedAt": "…Z",
  "columns": [ { "column": "id", "type": "INTEGER", "nullable": false,
                 "key": true, "generated": true }, … ],
  "indexes": [ { "name": "ix_…", "columns": [ { "column": "…", "direction": "asc" } ],
                 "unique": true } ] }
```

The DDL-shaped form for the other kinds:

```json
{ "object": "user_transaction_totals", "kind": "view", "asOfVersion": 6,
  "generatedAt": "…Z", "ddl": "create view user_transaction_totals as select …" }
```

The DDL is normalized — whitespace collapsed and the create-view prefix
canonicalized (databases rewrite that prefix when storing it) — and stays
executable. Column `type` in the table form is the dialect **storage type** (what
CREATE TABLE emits and what introspection reports), columns and indexes are
**name-sorted** — so every snapshot is producible two ways, and the two must be
byte-identical apart from `generatedAt`:

- **from metadata** (`snapshot`): the current model rendered at the last version
  touching each object;
- **from history** (`shadow`): the migrations replayed into a throwaway database,
  each touched table introspected after each version.

That equality is the integrity check between model and history. Snapshots are also
directly renderable as DDL: derived rollbacks (ADR-0018) and trusted baselines
come from them.

## The generator toolchain (ADR-0017)

- **`diff`** — metadata vs the latest committed snapshot, no database: emits a
  normal per-object step (literal SQL, no Down — rollbacks derive at runtime,
  ADR-0018) plus the composing root, new tables FK-ordered, view steps after
  table steps.
  Generated code freezes exactly like hand-written code. Tables diff by columns;
  views diff by normalized DDL, and a generated view change step is literal DDL
  opening with the `ExpectDefinition` guard (below). Indexes match
  **structurally** — unique flag + ordered (column, direction), never by name
  (ADR-0017 add.2): an index that exists under another name counts as
  implemented, because indexes get added directly to the database in urgencies.
  Renames are **declared** (`--rename table.old=new`), never inferred. Removals gate on `--allow-remove`
  (`DDL-003`); type/nullability changes and non-nullable additions are `DDL-004` —
  hand-written, using the literal `AddColumn(name, type, nullable, defaultSql)`
  path.
- **`shadow`** — rebuilds the snapshot files from history (above). The range form
  `--from V000N [--to V000M]` **trusts** version N: the baseline is reconstructed
  from the committed snapshots at ≤ N (no verification below N), versions ≤ N are
  baselined, and only (N, M] replays and re-snapshots.
- **`migrate --force`** — post-migration live-schema sync to the model: additive
  fixes apply; deletions require `--allow-delete` (`DDL-003`); inexpressible
  changes are reported as `DDL-004` and never auto-applied. Indexes on existing
  tables are checked with the same structural match: a missing one is created, a
  live one matching no model signature is a gated deletion, and one that matches
  under a different name is left alone, name and all.

## The view apply guard (`MIG-012`, ADR-0017 add.1)

Views get adjusted outside the code in urgencies. A view step may declare
`ExpectDefinition(ddl)` — generated change steps always do, carrying the previous
snapshot's definition. When the step applies, the runner compares the live
definition (normalized) against the expectation: match → apply; mismatch or absent
→ the run refuses with `MIG-012` and, the run being one transaction, nothing is
applied — the outside hotfix is preserved for review. Forcing
(`migrate --force` / `MigrateAsync(allowViewDrift: true, notify, ct)`) recreates
the view from the code and reports the drift. Derived view rollbacks carry the
mirrored guard. Guards evaluate at execution time (chained pending view changes
stay valid); declared ones count into the step checksum.

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

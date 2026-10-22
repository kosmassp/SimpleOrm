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
  Trigger/<Class>/V000N_<Desc>.cs   a trigger's object is its declaration class (ADR-0042)
```

- A **root version** (`V<version>`, `MIG-001`) composes its object steps in explicit,
  reviewable order (tables in FK order, views after, triggers last — their
  relation must exist). Duplicate root versions or an object composed twice in
  one version: `MIG-002`.
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
evolves. Metadata-rendered DDL (`CreateTable()`/`CreateView()`/`CreateTrigger()`)
is legal for an object's initial creation, frozen thereafter by the checksum.

Trigger steps (ADR-0042) name the entity and the trigger class
(`TriggerMigration<Role, RoleAuditTrigger>`); their `schema_version` object is
the trigger name. Actions run in declaration order and every one renders
through the dialect from a **declaration** — the entity's (`CreateTrigger()`,
the initial create) or the step's own literal, frozen one (`Recreate(timing,
events, sql, updateOf…)`, `ExpectDefinition(…)`, the forms `diff` emits) —
plus `DropTrigger()`. There is no raw-SQL trigger action: trigger DDL is
dialect-specific, and a step holding one dialect's text would tie the tree to
it; the declaration is neutral, so one tree serves every dialect and a
database's checksums stay those of its own dialect's rendering. Every create
is drop-if-present then create (replace semantics).

The typed actions **render through the dialect** (ADR-0024): ALTER TABLE grammar
diverges — SQLite says `add column` and `rename column`, SQL Server says `add`
and `exec sp_rename`; dropping an index needs the table name on SQL Server. A
database's history is applied, checksummed, and validated by one dialect, so
checksums stay stable per database; the SQLite renderings are the frozen
reference.

## Recording and integrity

Table `schema_version` — same columns and semantics on every dialect, dialect
DDL (SQLite: `IF NOT EXISTS` + STRICT, `INTEGER`/`TEXT`; SQL Server:
`if object_id(…) is null`, `bigint`/`nvarchar`):

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
  preserves data), then the shape diff: the indexes this version added drop
  (before the columns they cover — SQLite and SQL Server refuse to drop a
  column an index still uses, ADR-0043), dropped columns restored (a NOT NULL
  restore lands nullable with a notice — constraint and data are not derivable),
  added columns dropped, the previous version's indexes recreated;
- views → expected-definition guard on the current DDL (`MIG-012` — a rollback
  must not silently destroy an outside hotfix either), drop, previous definition
  restored;
- triggers → the guard on this version's rendering (`MIG-012`, at the creating
  version too, as for views), the drop, and the previous declaration rendered
  through the dialect (creation → guard and drop only); a removal step has no
  snapshot at its version and derives nothing (`MIG-020`, override `Down()`),
  exactly like a dropped table;
- a same-column type/nullability change is not derivable → `MIG-020`.

Every derived statement renders through the dialect (ADR-0043): the snapshots
are the dialect's, and so is the rollback — a snapshot stamped for another
dialect refuses with `MIG-014` for the whole range before anything executes
("Dialects" below). `Down()` remains the manual override and always wins;
`PreDown`/`PostDown` hooks wrap whichever core runs and carry the data work (a
data-only step derives an empty rollback — schema-correct; its inserted rows
are the hooks' job). The
snapshots ship **embedded in the migrations assembly**
(`<EmbeddedResource Include="Migrations\**\*.schema.json" />`) so rollbacks work
deployed; the CLI's `--snapshots <dir>` reads them from source instead.

## Locking and atomicity

The dialect provides the run lock (§7.25). On SQLite the entire run executes inside
one `BEGIN IMMEDIATE` transaction — exclusive writer, and with transactional DDL a
failed run rolls back completely (no partially applied versions). On SQL Server
(ADR-0024) the run is one transaction holding an exclusive
`sp_getapplock @LockOwner = 'Transaction'` (60s timeout, then refuse). On
Postgres (ADR-0025) it is one transaction holding `pg_advisory_xact_lock` under
a 60s `lock_timeout` — transaction-scoped, so the lock can never leak past a
rollback. DDL is transactional on all three, so the failure semantics are
identical everywhere: a failed run applies nothing; the once-anticipated
per-migration transactions proved unnecessary.

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
canonicalized to `create [materialized] view <name> as <body>`: `or alter`,
`or replace`, `if not exists`, and the name's quoting are stripped, because
databases rewrite that prefix when storing it and each dialect quotes its own
way, while the body is the one SQL the entity declares. So a view snapshot's
content is **dialect-neutral** (ADR-0043); it stays executable once a dialect
renders it again from name and body.

A trigger snapshots by **declaration** (ADR-0042), `Migrations/Trigger/<Class>/V000N.schema.json`:

```json
{ "object": "trg_roles_audit", "kind": "trigger", "relation": "roles", "relationKind": "table",
  "asOfVersion": 13, "generatedAt": "…Z",
  "timing": "after", "events": ["update"],
  "sql": "insert into role_changes (role_id, role_name, changed_at) select n.id, n.role_name, {now} from {new: id, role_name} n;" }
```

— the relation and its kind (`table` | `view`), the timing, the events in the
fixed order, `updateOf` (column names) only when declared, and the body
whitespace-collapsed with its tokens **unexpanded**. Its content is
dialect-neutral like a view's. From metadata it is the linked declaration;
from history (`shadow`) it is the declaration the replayed step leaves behind
(ADR-0043, "Dialects" below) — and where the dialect's catalog hands the text
back losslessly (SQLite stores it as submitted; PostgreSQL reconstructs the
header and keeps the body verbatim) the shadow also parses the stored trigger
back — header fields from the dialect's fixed grammar, the expansions folded
back into the tokens — and notes a disagreement; a hand-written trigger the
grammar does not match is noted too. Column `type` in the table form is the
dialect **storage type** (what CREATE TABLE emits and what introspection
reports — spelled exactly as introspection reports it, ADR-0043), columns and
indexes are **name-sorted** — so every snapshot is producible two ways, and the
two must be byte-identical apart from `generatedAt`:

- **from metadata** (`snapshot`): the current model rendered at the last version
  touching each object;
- **from history** (`shadow`): the migrations replayed into a throwaway database,
  each touched table introspected after each version.

That equality is the integrity check between model and history. Snapshots are also
directly renderable as DDL: derived rollbacks (ADR-0018) and trusted baselines
come from them, through the dialect.

**Format v3 (ADR-0043): the dialect stamp.** A snapshot written for any dialect
but SQLite carries `"dialect": "<name>"` right after `"object"`, whatever its
kind — a tree and its snapshots belong to one dialect, like its checksums. An
unstamped document is SQLite's (every format v2 file, every SQLite tree:
nothing on disk changes). Every consumer checks the stamp against the dialect
in use — the derived rollback, the diff baseline, the shadow's trusted
baseline — and refuses a mismatch with `MIG-014`.

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
  path. Triggers diff **by declaration** (ADR-0042): neutral on both sides,
  nothing rendered — a different timing, event set, update-of list, relation,
  or whitespace-normalized body is a change (a layout-only edit declares
  nothing); no snapshot is new; a snapshot history no entity declares any more
  is a removal, gated like a column's. A generated trigger step carries the
  literal declaration (`Recreate`), a change opens with `ExpectDefinition` of
  the previous one, a removal is a bare `DropTrigger()` — and needs the trigger
  class and its entity still in the assembly (keep them until the removal has
  applied). A removal leaves no snapshot of its own, so it is recognised as
  already emitted when the trigger's newest step is later than its newest
  snapshot — never emitted twice. Trigger steps compose after view steps.
- **`shadow`** — rebuilds the snapshot files from history (above), on every
  dialect (ADR-0043): the throwaway database comes from the dialect's shadow
  host (SQLite a temp file; SQL Server and PostgreSQL a database
  `simpleorm_shadow_<hex>` created on the admin connection `--db` names and
  dropped afterwards, failure included — the live-test contract), tables are
  introspected through `ColumnsInfoSql`/`IndexesInfoSql`, views and triggers
  are taken from the replayed step ("Dialects" below). The range form
  `--from V000N [--to V000M]` **trusts** version N: the baseline is rendered
  from the committed snapshots at ≤ N through the dialect (no verification
  below N), versions ≤ N are baselined, and only (N, M] replays and re-snapshots.
- **`migrate --force`** — post-migration live-schema sync to the model: additive
  fixes apply; deletions require `--allow-delete` (`DDL-003`); inexpressible
  changes are reported as `DDL-004` and never auto-applied. Indexes on existing
  tables are checked with the same structural match: a missing one is created, a
  live one matching no model signature is a gated deletion, and one that matches
  under a different name is left alone, name and all.

## The view and trigger apply guard (`MIG-012`, ADR-0017 add.1, ADR-0042)

Views get adjusted outside the code in urgencies. A view step may declare
`ExpectDefinition(ddl)` — generated change steps always do, carrying the previous
snapshot's definition. When the step applies, the runner compares the live
definition against the expectation: match → apply; mismatch or absent
→ the run refuses with `MIG-012` and, the run being one transaction, nothing is
applied — the outside hotfix is preserved for review. The comparison runs in
the dialect's canonical form (ADR-0043, `ViewDefinitionIsVerbatim`): where the
engine stores the text as submitted (SQLite, SQL Server) both sides normalize
and compare as text; where it reformats the parse tree (PostgreSQL's
`pg_get_viewdef`) the runner creates the expected body as a probe view inside
the run, reads it back the same way, drops it, and compares the engine's two
renderings — equal exactly when the parse trees are. The probe runs under a
savepoint, so a body the current schema cannot parse counts as drift instead
of poisoning the transaction. Forcing
(`migrate --force` / `MigrateAsync(allowViewDrift: true, notify, ct)`) recreates
the view from the code and reports the drift. Derived view rollbacks carry the
mirrored guard. Guards evaluate at execution time (chained pending view changes
stay valid); declared ones count into the step checksum.

Triggers reuse the code and the semantics (ADR-0042). A trigger step's
`ExpectDefinition(timing, events, sql, updateOf…)` is a **declaration**; at
execution time the dialect renders it to its create statements, and the live
rows of `TriggerDefinitionSql` (the stored or reconstructed create statements
— one per statement the dialect created: SQLite's per-event triggers,
PostgreSQL's function and trigger), both sides normalized (whitespace
collapsed, the leading `create [or replace] trigger|function` keywords
lowercased), must be the same set. Absent or different refuses; `--force`
recreates with notice. The checksum counts the rendered expectation, so it is
the dialect's, like every other rendering (the reference joins the
normalized expected statements with `
;
`; checksums are never compared
across implementations). `migrate --force` never syncs triggers to the model
(views are not synced either): for both kinds the flag is only this override.
`CreateTrigger()` in a step whose entity does not link the trigger class is
`DDL-001` (schema creation that does not apply); a trigger step on a
statement, procedure, or materialized view is `DDL-001` too. The `--from`
shadow baseline restores triggers after tables and views from their latest
snapshot at or below N — a trigger removed before N is restored wrongly, as a
dropped table would be: the trusted baseline trusts snapshots, not history.

## Dialects (ADR-0043)

Everything above works on SQL Server and PostgreSQL as it does on SQLite —
`snapshot`, `shadow`, `diff` (and `--amend`), the derived rollbacks, and
`migrate --force`. The rules that make it so:

- **A tree belongs to one dialect.** Its table steps hold literal column specs
  in one dialect's text (the sample's `STRICT` creates, `AddColumn("avatar",
  "BLOB")`), its checksums are one dialect's renderings, and so are its
  snapshots: every non-SQLite snapshot carries the dialect stamp (format v3,
  above) and `MIG-014` refuses one of another dialect. The CLI's `--dialect`
  selects the dialect for every command; the sample trees are SQLite's.
- **Storage types compare as introspection spells them.** `StorageType(map,
  property)` renders what `CREATE TABLE` emits and what `ColumnsInfoSql`
  reports back, byte for byte — SQL Server `decimal(38,9)` (no space),
  `nvarchar(450)` for a key column *or a column of a declared index*,
  PostgreSQL's `format_type` spellings — so snapshot-versus-shadow, diff, and
  force sync are plain string equalities. A declared storage facet
  (`[Column(Length = 50)]`, `[Column(Precision = 18, Scale = 2)]`,
  metadata-model.md "Storage facets") renders through the same function, so a
  real tree's `decimal(18,2)` is the model's type, not a `DDL-004` forever.
- **Tables snapshot from the engine, views and triggers from the step.** The
  shadow introspects tables after each version through `ColumnsInfoSql`
  (`name, type, notnull, pk, generated`) and `IndexesInfoSql`. For a view it
  takes the step's last create statement, normalized; for a trigger the
  declaration the step's actions leave behind (`CreateTrigger()`/`Recreate`
  → declared, `DropTrigger()` → gone) — because an engine's catalog may not
  hand them back as declared: PostgreSQL reformats a view, SQL Server's
  `inserted` has lost a `{new: …}` token's column list. A later drop ends the
  history. Where the catalog is lossless (SQLite views and triggers, SQL
  Server views, PostgreSQL triggers) the shadow reads it too and **notes** a
  disagreement; a raw-SQL step with no create falls back to the catalog where
  it is verbatim and is skipped with a note where it is not.
- **The rollback renders through the seam.** `RenameColumnSql`,
  `AddColumnSql`, `DropColumnSql` (SQL Server drops a leftover default
  constraint first), `DropIndexSql`, `CreateIndexSql(table, index)` from the
  snapshot index, `DropViewSql`, `CreateViewSql(name, body, materialized)`
  from the neutral view snapshot, `CreateTableSql(schema)` for the trusted
  baseline, and the trigger members — no tooling path holds SQLite text.
- **The view guard has a mode per dialect** (above): text where verbatim, the
  probe round trip where not.
- **The shadow's database is the dialect's** (`IShadowHost` beside the seam,
  a tooling contract never consulted at runtime): provision on the admin
  connection, drop afterwards, and the optional trigger read-back for the
  self-check (SQLite, PostgreSQL; none on SQL Server).
- **`migrate --force`** reads the live schema through the same two
  introspection queries and renders through the same members; its gates
  (`DDL-003`, `DDL-004`) are unchanged.

Not derivable stays the same everywhere: a type or nullability change, a
removal step, and the data behind a NOT NULL restore (`MIG-020`, the notice).
Still out of scope: materialized views beyond the view treatment on
PostgreSQL, procedures, cross-dialect snapshot conversion, multi-dialect
trees, defaults in snapshots, clustered, filtered, or included-column indexes.

Conformance: `snapshot-cases` expect one document per dialect;
`migrations-cases/postgres/` and `migrations-cases/sqlserver/` replay the
derived rollbacks and the guards on a live engine (skip-gated, each case in a
throwaway database the shadow host provisions); every implementation's live
leg runs a shadow round trip — a short tree replayed, its snapshots equal to
the metadata-side export apart from `generatedAt`.

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

The generator/derived-rollback additions (ADR-0017/18) extend the format so every
behavior in this document is expressible as data:

- top-level `"snapshots": [ <snapshot documents> ]` — the history the runner
  derives rollbacks from;
- step `"renames": [ { "from", "to" } ]` — the typed pairs the derived rollback
  inverts (a raw-SQL step cannot convey them any other way);
- step `"expectDefinition": "<ddl>"` — the MIG-012 view guard, checked against
  the live definition when the step applies;
- `"command": "sql", "statements": [...]` — the outside hotfix, applied between
  runs without touching migration history;
- `"force": true` on `migrate`/`down` — the view-drift override;
- deep expects: `"columns": { "<table>": [name-sorted column names] }` and
  `"ddl": { "<view>": "<normalized definition>" }`;
- step `"trigger": { "relation", "relationKind", "timing", "events", "updateOf"?, "sql" }`
  — a trigger declaration as data, rendered through the dialect (drop, then
  create) ahead of `up` (which may be absent); step `"expectTrigger"` of the same
  shape — the `MIG-012` trigger guard; a `"snapshots"` entry with
  `"kind": "trigger"`; and the deep expect `"triggers": { "<name>": { "relation",
  "timing", "events", "updateOf"?, "sql" } | null }` — the live trigger read back
  through the dialect and parsed to its declaration, or absent (ADR-0042);
- per dialect (ADR-0043): the root folder runs on SQLite; `postgres/` and
  `sqlserver/` hold cases in that dialect's SQL with stamped snapshots
  (`"dialect": "postgres"`), run on a live engine. `columns` reads through
  `ColumnsInfoSql` on every dialect; `ddl` applies only where
  `ViewDefinitionIsVerbatim`; `triggers` only where the dialect reads a trigger
  back (not SQL Server).

Two sibling suites cover the generator itself: `conformance/diff-cases/` feeds
two shapes (snapshot form) plus declared renames through the diff and checks the
resulting change exactly — no database (a case with `"kind": "trigger"` feeds
two trigger declarations, either null, and expects `isNew` | `isChanged` |
`isRemoved` or nothing); `conformance/snapshot-cases/` pins a fixture entity,
version, and generation time and expects the exact exported snapshot document
**per dialect** — `expect` keyed by dialect name, a shipped dialect missing from
it fails the suite (ADR-0043; `"kind": "trigger"` names the linked trigger). `conformance/trigger-sql/` pins
what every dialect renders for a declaration (`dialects.md` "Triggers"). See
`conformance/README.md` for the schemas.

## Draft migrations (Level 3 M5, ADR-0034 §7c)

Two additions to the generator, both declared, nothing inferred:

- **`[RenamedFrom("old_column")]`** on a `[Column]` property (metadata-model.md:
  exported as `"renamedFrom"`) is a declared rename the generator reads
  exactly as a `--rename table.old=new` flag — one data-preserving
  `RenameColumn` instead of a drop and an add — but kept with the model, so
  the intent survives the one `diff` run. The declaration is **permanent and
  harmless once applied**: when the latest snapshot already has the new
  column, or has neither, it declares nothing (the column is then simply
  new, or already renamed). An explicit `--rename` stays strict — an unknown
  side is unsupported — and wins over a declaration of the same old column.
  `MAP-027` for a declaration without `[Column]` or without a name. Table
  renames are not declared this way (a table's rename is a step the author
  writes).
- **`diff --draft`** emits the version with a draft marker (C# `[Draft]` on
  the root class; PHP/Go their own marker) that the runner refuses to apply
  or baseline — **`MIG-013`, the whole plan, before anything executes** —
  until the author has reviewed the generated code and removed it. A draft
  still composes, renders, `status` reports it as `Draft`, and `shadow`
  replays it: it is a version in every respect but applicability.
  `--amend` regenerates a draft like any generated version.

Conformance: `diff-cases/` current columns may carry `"renamedFrom"`;
`migrations-cases/` versions may carry `"draft": true`; `entities/role.json`
pins the export (`Role.Name` was `name` until V0004).

**Clarifications** (the PHP and Go M5 ports asked; ADR-0038):

- **A declaration is a rename only while it can be one**: the old column in
  the snapshot, the new one not yet there, **and the model no longer mapping
  the old one** — a model that still maps it describes two columns, and the
  new one simply adds. An explicit `--rename` of the same old column wins
  and is applied once, never doubled. Matching is case-insensitive, like
  explicit renames; names are trimmed. Renames apply and emit in **ordinal
  order of the old column**, so every implementation writes the same step.
- **Two properties declaring the same previous column** is `MAP-027` at
  load — two columns cannot both have come from one — never a diff-time
  refusal.
- **`MAP-027` covers every misplaced declaration**: without `[Column]`, on an
  ignored property, a navigation, an owned navigation or an owned type's
  member (a prefixed column cannot say which old name it means), a blank
  name, and **a non-table source** (a view or statement has no column diff
  for the declaration to feed — the `MAP-026` precedent). The manual builder
  declares renames under the same rules.
- **Export order of a column's optional keys**: `key`, `generated`, `audit`,
  `softDelete`, `renamedFrom` — `renamedFrom` last.
- **"Pending" for `MIG-013`**: a draft not recorded in `schema_version`. A
  recorded draft is history (its checksum rules apply) and keeps its state;
  `status` reports `Draft` only for an unrecorded one. `baseline N` refuses
  drafts at or below `N` only; `migrate down` ignores the marker. The
  refusal names the version (`V0002`), not an object.
- **A pending draft is pending work**: `HasPending` is true and SchemaGuard
  reports it as `MIG-030`.
- **"Before anything executes"** is met by the run transaction: the version
  table's creation precedes the refusal and rolls back with it.
- **`shadow` replays a draft** through a runner option that only the
  tooling sets (C# `ReplayDrafts`, PHP `replayDrafts`, Go a marker-hiding
  wrapper) — there is no application-side bypass of `MIG-013`.
- **`--amend` keeps the marker** of a root that already carries it, with or
  without `--draft`: unreviewed code never becomes applicable by
  regeneration.

**Clarifications** (the Level 3 exit audit; ADR-0039):

- **The draft marker's language-neutral token** is the word `DRAFT` in the
  root's header comment, written beside the language's own marker (C#
  `[Draft]`, PHP `#[Draft]`, Go `IsDraft()`); `amend-cases` pins it through
  `command.draft` (`diff --draft`) and `files[].draft` (a root laid out with
  the marker). **`--amend` reads the marker from the root file** in the tree
  it regenerates — the marker is text in the tree, not a property of a
  compiled class.
- **A rename whose new column also changes type or nullability**: the rename
  is emitted and the change is reported `DDL-004` under the new name, exactly
  as for an unrenamed column. A declaration naming the column's own name
  declares nothing — the snapshot already has it.
- **Case-format fields.** `diff-cases`: `renames` is a map `{ "old": "new" }`
  (the explicit `--rename`s); `expect.renamed` lists `[old, new]` pairs in
  emitted order; `current.asOfVersion` is `0` — the model has no version.
  `migrations-cases`: a `baseline` step carries `version`; a version object
  may carry `"draft": true`; `draft_refuses_to_apply` pins the refusal's
  atomicity with a `down` to 0 that finds nothing applied.
- **`status` reports one entry per (version, object)**: the version, the
  object, the step's description, and the state — `Applied`, `Pending`, or
  `Draft`. The text layout is the implementation's.

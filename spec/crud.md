# Generated CRUD — key strategies, writes, concurrency

Insert, update, delete, and read-by-key are generated from `EntityMap` and nothing
else (§7.14) — never from attributes, never from hand SQL. Every generated
statement lists its columns **explicitly** (no `SELECT *`, no positional
assumptions) and binds every value as a parameter. Every operation runs on the
session's connection and inside its current transaction, if any (§7.17).

Only **table-backed** entities are writable: a write against a view, materialized
view, statement, or procedure source refuses with `CRUD-003` (ADR-0008). A
table-backed entity always has a key — declaring none is a loader error
(`MAP-019`) — so every write and key read can rely on one; `CRUD-002` remains the
key-shape code for reads against keyed views and malformed key arguments.

## Key strategies (§7.14)

| Strategy | Shape | Insert behavior |
|---|---|---|
| database-generated | single generated **integer** key (SQLite: `INTEGER PRIMARY KEY`; a non-integer `[Generated]` key is a loader error, `MAP-019`) | the insert renders `RETURNING <key column>`; the generated key is read back and **written onto the entity** |
| client GUID | single GUID key | an empty GUID gets a new one assigned before the insert; a caller-set GUID is kept |
| natural / composite | one or more plain key columns (composite `primary key (…)`) | key values come from the entity as-is |

No sequence strategy at Level 1 — SQLite has none; it returns with a dialect that
has them. Insert **returns nothing**: the generated key landing on the entity is
the one rule that works for integer, GUID, and composite strategies alike
(ADR-0014).

## Read by key (ADR-0006)

`GetAsync` / `GetOrDefaultAsync` take the key value — composite keys pass an
ordered tuple — validated at call time against the `EntityMap` key:

- no key defined → `CRUD-002`;
- arity mismatch (tuple size vs. key parts) → `CRUD-002`;
- a part's type differing from the key property's → `CRUD-002` naming the part.
  Safe integer widening is allowed (`GetAsync<Order>(7)` works against an `int64`
  key); an out-of-range value still fails `CRUD-002`.

The generated select lists every mapped column and ANDs every key column in the
WHERE. A missing row **throws** `CRUD-001` naming the entity and key
(`GetOrDefaultAsync` is the explicit null-returning opt-in). A key matching more
than one row — possible on a keyed view whose declared key is not actually
unique — is `QRY-002`. Rows materialize through the one shared mapping pipeline
(`mapping-rules.md`); its strictness and conversion codes apply unchanged.

Key reads work on tables and keyed views; statements and procedures are queries,
not named relations, and refuse with `QRY-005`. The generated select-all
(`QueryAllAsync`) follows the same rules: explicit column list, ordered by the key
when one exists, `QRY-005` for statements/procedures.

## Insert

Writes every mapped **non-generated** column, explicit column list, one parameter
per column. Database-generated columns are never in the list; the key strategy
decides the write-back (above). The navigation/FK consistency check (below) runs
before anything is written.

## Update — full row, optimistic concurrency (§7.15–16)

Update writes **every mapped non-key, non-version, non-generated column by key**
— the version column is database-computed (below), generated columns are
database-owned exactly as on insert, and partial updates are hand SQL at
Level 1. The WHERE ANDs every key column.

With a mapped **version column** (integer-valued):

- the SET renders `version = version + 1` — the database computes it, the entity's
  value is never written into the SET;
- the WHERE adds `version = @current`, bound from the entity;
- zero rows affected → `ConcurrencyException` (`CRUD-010`) — its own exception
  type, distinct from the general error, so callers can catch the
  reload-and-retry case specifically;
- on success the entity's in-memory version is incremented, so sequential updates
  on the same instance keep working.

Without a version column, zero rows affected is `CRUD-001` — silently updating
nothing is a bug, not a no-op.

## Delete

One operation, dispatched on the argument (ADR-0014):

- **a key (or tuple)** deletes by key, validated exactly like a key read
  (`CRUD-002` shapes); zero rows affected → `CRUD-001`;
- **the entity** gives the version-checked form when a version column is mapped:
  the WHERE adds the entity's version, and zero rows affected →
  `ConcurrencyException` (`CRUD-010`). An entity of a type without a version
  column deletes by its key values; zero rows is `CRUD-001`.

## Navigation/FK consistency (`CRUD-004`)

The FK property is what is written; a navigation is never written and never
trusted (ADR-0005). Before insert or update, every non-null many-to-one
navigation must agree with its declared FK properties: the target's key parts are
compared **pairwise, in key order**, against the entity's FK properties
(composite-aware, ADR-0019 add.1). Any disagreement throws `CRUD-004` naming the
navigation and the FK property instead of writing. FK-count/key-arity mismatches
are loader errors (`MAP-016`), never write-time ones.

## Generated DDL (dev/test utility, ADR-0011)

`CreateTableAsync` / `CreateViewAsync` render an object's schema from its metadata
(idempotent: IF NOT EXISTS), then create its declared indexes. Versioned
migrations remain the schema-evolution path — this exists for tests and throwaway
databases. Wrong source kind → `DDL-001`; a materialized view on a dialect
without them → `DDL-002`. Table DDL renders the key per strategy (generated
integer key inline, composite `primary key (…)`) and is STRICT on SQLite.

## Repository — the generic layer (ADR-0016)

`Repository<TEntity>` wraps one **injected session** with the generic surface:
insert, update, delete (key or entity), get / get-or-default, get-all, and
criteria find/query. It delegates to the session's generated operations — same
pipeline, same error codes — and adds nothing of its own. Instance-based and
session-first (§7.17): no statics, no ambient session, one instance per unit of
work; entity-specific methods come from subclassing. (Deliberately not a
`DbContext` — that is EF's word for the *session* — and not a collection-property
facade.)

## Conformance cases

`conformance/crud-cases/*.json`: lifecycle and concurrency scenarios as ordered
step scripts, replayed by each implementation through its generated CRUD against
a fresh migrated fixture database:

```json
{ "name": "…", "steps": [
    { "op": "insert", "entity": "User", "values": { "name": "Ada", … } },
    { "op": "get", "entity": "User", "key": "$last", "as": "a",
      "expect": { "values": { "name": "Ada" } } },
    { "op": "update", "from": "a", "values": { "name": "Ada Lovelace" } },
    { "op": "delete", "entity": "User", "key": "$last" },
    { "op": "get", "entity": "User", "key": "$last", "expect": { "error": "CRUD-001" } } ] }
```

- `values` are keyed by **column name** and use the conformance value encoding
  (`mapping-rules.md`).
- `key` is a literal or `"$last"` — the most recent insert's key.
- `"as"` snapshots a get result under a name; `"from"` replays an update or
  delete against that captured instance — which is what makes stale-version
  conflicts (`CRUD-010`) expressible as data.
- `expect` is either `values` checked on a get result or an error code; a step
  without an expected error must succeed.

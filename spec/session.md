# Session — one connection, explicit transactions

The session (§7.17) is the unit of database access: it owns **one connection**,
at most **one transaction**, and no ambient state. Every round trip is visible in
user code — no hidden queries, no lazy proxies. Every I/O operation is async and
takes a cancellation token; that surface is the spec-level contract even where a
provider is synchronous underneath (ADR-0003).

## Lifetime

A session opens against a connection string plus options and ends by disposal:

```csharp
await using var db = await Db.OpenAsync(connectionString, options, ct);
```

- The dialect creates the connection (§7.25); the session opens it. A failed
  open leaves nothing behind — the connection is released and the error
  propagates.
- Options are fixed at open: the dialect (required), the mapping configuration
  (naming convention, explicit maps — metadata-model.md), and the type-handler
  registry (mapping-rules.md). The session owns its metadata cache: an entity's
  map loads once per session.
- Disposal rolls back any still-active transaction, then closes the connection.
  Disposal is the only teardown; there is no close-and-reopen.

Everything the session exposes — registry queries and commands, statement
entities, key reads, generated CRUD, criteria queries, metadata DDL — runs on
this one connection and automatically inside the current transaction, if any.

## The registry surface

A query or command is declared **once**, binding SQL to its argument and result
types (§6, §7.5); execution takes the entry, an args value, and a token. The SQL
is inline at the declaration site or an embedded resource, resolved lazily and
cached — a missing resource is `QRY-003` at first use, not at declaration. Every
entry carries a description (the resource path; a prefix of the inline SQL), and
every error about the entry names it.

- **Query** → all rows, fully materialized, in result-set order.
- **QuerySingle** → exactly one row: zero is `QRY-001`, more than one `QRY-002`.
- **QuerySingleOrDefault** → at most one row: zero yields the absent value
  (null; a value-typed result yields its default), more than one `QRY-002`.
- **Stream** → rows delivered one at a time as the caller iterates; nothing is
  buffered.
- **Execute** → a non-query command; returns the affected-row count.

Rows become objects through the one mapping pipeline (mapping-rules.md). The
result shape is validated from the result schema **before the first row**, so
strictness (`MAP-001/002/003`) fires even for an empty result — on streams,
before the first element is produced.

Parameterless SQL takes the canonical empty args value (`EmptyArgs`); with no
properties, parameter strictness is trivially satisfied.

## Statement-backed entities

For a statement-backed type the type *is* the query (ADR-0008/0010): execution
names the result type and passes args only —

```csharp
var days = await db.QueryAsync<DailySales>(new DailySalesArgs(since), ct);
```

- The target must actually be statement-backed; anything else is `QRY-004`.
- The loader already proved declaration ↔ SQL agreement (`PRM-010/011`,
  metadata-model.md). At execution the args are checked against the declaration
  by **type** as well as name: a property whose type differs from the declared
  parameter type is `PRM-012` (name matching case-insensitive, nullable
  wrappers transparent).
- Binding then proceeds against the statement's defining SQL under the same
  rules as registry entries (`PRM-001/002` below).
- Single / SingleOrDefault / Stream variants mirror the registry surface,
  including `QRY-001/002`.

The inverse rule: a source without a named relation (statement, procedure)
refuses select-all, key reads, and criteria queries with `QRY-005` — the
statement API is its only read path.

## Parameters

`@name` placeholders bind from the public readable properties of the args value
(§7.12/§7.13), matched case-insensitively. Placeholders inside string literals
and SQL comments are not placeholders. Both directions are strict, checked at
every execution:

- a placeholder with no matching property → `PRM-001`
- a property no placeholder uses → `PRM-002`

Values cross the boundary through the conversion pipeline (mapping-rules.md);
a conversion error names the query and the placeholder.

**Collections (IN lists).** A collection-typed property (strings and byte
arrays are values, not collections) expands its placeholder into one generated
placeholder per element — `IN (@Ids)` becomes `IN (@Ids_0, …, @Ids_N)` — each
element bound as a parameter. Only placeholder *names* are written into the
SQL; values never are (§2: no SQL from user data by concatenation, ever). An
**empty** collection renders the placeholder as SQL `NULL`: `x IN (NULL)` is
valid and matches no rows — an empty list selects nothing, it never errors.
Every occurrence of the placeholder expands identically.

Expansion is the no-array-parameters strategy (`SupportsArrayParameters`,
§7.12, ADR-0003): SQLite has none, so the reference implementation always
expands. A dialect with native array parameters (ADR-0025 Postgres) binds the
collection as a **single typed-array parameter** with the SQL untouched — the
registry SQL is written dialect-natively (`WHERE id = ANY(@ids)`), every
element passes through the same conversion pipeline, and an **empty**
collection binds a typed empty array (the element's database type still
matters: `bigint = any(text[])` refuses even empty). The observable contract —
always parameterized, empty matches no rows — is identical either way.

## Transactions

```csharp
await using var tx = await db.BeginAsync(ct);
// ... commands enlist automatically ...
await tx.CommitAsync(ct);
```

- One transaction per session: `BeginAsync` while one is active is `TX-001`.
  There is no nesting; savepoints are Level 4 (§7.17).
- Enlistment is automatic and total: from begin until commit or rollback, every
  operation on the session runs inside the transaction. There are no ambient,
  static, or thread-bound transactions — the scope is a value the caller holds.
- Commit is explicit. Rollback happens three ways: an explicit rollback,
  disposing an uncommitted scope, or disposing the session while its
  transaction is active. An uncommitted transaction is never silently
  committed.

## Async and cancellation

Every I/O operation is asynchronous and takes a cancellation token — the
contract (§2) that ports implement in their own idiom, even where the provider
is synchronous underneath (ADR-0003: the SQLite provider is; the async surface
is what keeps one shape across ports and future dialects).

- Cancellation is cooperative: observed when a statement starts, periodically
  between rows during materialization, and per row on streams.
- Cancellation surfaces as the platform's cancellation signal, never as a
  SimpleOrm error code.

## The rest of the session

Key reads, generated CRUD, criteria queries, and metadata DDL live on the same
session and obey the same three rules — one connection, automatic transaction
enlistment, async with cancellation. Their behaviors carry their own codes
(`CRUD-*`, `DDL-*`, `QRY-005/006/007`) in errors.md.

## Conformance

`conformance/cases/*.json` (format: mapping-rules.md) pins the mapping side of
this surface — result-shape strictness and value encoding (`MAP-*`, `VAL-*`
expectations); `conformance/ast/` pins criteria parameter binding order and
values. The parameter strictness codes (`PRM-001/002`) and single-row codes
(`QRY-001/002`) have **no data-driven form yet**: args are native typed values,
so a portable case format needs an args-shape encoding — reserved, the way
`ast/` was before ADR-0020. Until then each implementation proves them in its own
tests (here: DbParameterTests, DbQueryTests). Lifetime and transaction semantics
likewise have no data-driven form.

## Tracking — the identity map and change snapshots (Level 3 M1, ADR-0034 add.1)

Tracking is a **session mode chosen at open**; the default is **untracked**,
which is the Level 0–2 session exactly: every read materializes a fresh
instance, `Update` writes the full row, and nothing below applies.

```csharp
var options = new DbOptions { Dialect = new SqliteDialect(), Tracking = TrackingMode.Tracked };
```

A tracked session keeps, for its lifetime:

- **One instance per (entity type, key)** — the §7.4 identity, compared
  value-wise (decimals numerically, temporals by instant, byte arrays by
  content). Every read path that materializes an entity consults the map:
  registry queries and streams whose result type is an entity, key reads,
  criteria queries, explicit and batch loading, eager loading in every fetch
  mode (join segments included), and `QueryAll`. Plain result types (DTOs,
  scalars), keyless entities (views without a key, statements), and rows
  whose key has a null part are never tracked.
- **A snapshot of each tracked instance's mapped values** as read or last
  written; owned members snapshot flat, like their columns (ADR-0030).

**The tracked instance wins** (ruling 2): a read that finds its key in the
map returns the tracked instance and **does not apply the row's values** —
dirty or not. Loaded navigations stay as they were. `RefreshAsync(entity)`
is the explicit overwrite: it re-reads the row by key, applies every mapped
value onto the instance, and resets the snapshot; a missing row is
`CRUD-001`. `Refresh` works in both modes.

**Questions** — `IsTracked(entity)` (never throws); `ChangesOf(entity)`,
the mapped property names whose value differs from the snapshot (dotted for
owned members), and `IsDirty(entity)`; both are `UOW-001` for an instance
the session does not track, including every instance in an untracked
session.

**Writes keep the map honest:**

- `Insert` registers the instance with its generated key and a fresh
  snapshot.
- `Update` on a **tracked** instance writes **only the dirty columns**
  through the column-list path (ADR-0028) — the version bump and check
  apply as always — and **writes nothing at all when the instance is
  clean** (no statement, no version bump). On an instance the session does
  not know (user-constructed, never inserted here) it writes the full row as
  at Level 1, then tracks it. `UpdateOnly` writes the listed columns as
  always; both refresh the snapshot afterwards.
- `Delete` forgets the key; the deleted instance is no longer tracked.
- A write from an instance that is **not the session's instance for its
  key** — another tracked instance holds the key, or a tracked instance's
  key was changed — is `UOW-002` before any SQL. A key is identity; it never
  updates.

Nothing here adds a round trip: tracking only decides which instance a read
returns and which columns an update writes. Detach, merge, the unit of
work, and cascades are later milestones (§7c).

### Conformance: `tracking-cases/`

Each case runs against the seeded fixture database in the declared mode and
replays a step script against named instances:

```json
{ "name": "…", "comment": "…", "tracking": "tracked",
  "steps": [
    { "get":    { "entity": "User", "key": 1, "as": "a" } },
    { "set":    { "ref": "a", "property": "Name", "value": "Ada Lovelace" } },
    { "expect": { "dirty": { "ref": "a", "properties": ["Name"] } } },
    { "update": { "ref": "a", "expectWritten": ["Name"] } },
    { "get":    { "entity": "User", "key": 1, "as": "b" } },
    { "expect": { "same": ["a", "b"] } } ] }
```

Steps: `get` (by `key`, a list for composite keys, or `keyOf` another
ref), `new` (a user-constructed instance from `values`), `set`, `insert`,
`update` (with `expectWritten`: the property list the column-list update
was asked to write, `[]` for no statement, `"all"` for a full-row update —
observed through the dialect seam, where every implementation renders
SQL), `updateOnly`, `delete` (by `ref` or by `entity` + `key`), `refresh`,
`load` (a navigation of a ref). Expectations: `same`/`notSame` (reference
identity of two refs; a ref may hop one navigation, `t.User`, or index a
loaded collection, `a.Transactions[0]`), `dirty`, `value` (`ref`,
`property`, `equals`), `tracked` (`ref`, `is`). Any step may carry
`expectError` with the code it must throw. Property names are spelled as
the reference declares them; a port maps them through its own declaration
convention in the runner, as the other runners do.

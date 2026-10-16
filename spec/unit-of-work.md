# The unit of work — schedule, then flush (Level 3 M2, ADR-0034 §7c)

The unit of work is the second half of tracking (session.md "Tracking"): a
**tracked** session schedules inserts and removals, knows which tracked
instances are dirty, and writes all of it in one explicit `Flush` — in
foreign-key order, with generated keys flowing into dependents, inside one
transaction. Nothing is written until `Flush`; nothing loads to decide what to
write. An untracked session has no snapshots to flush from: every unit-of-work
call there is `UOW-003`.

```csharp
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
var order = new Transaction { Amount = 5m, Status = TransactionStatus.Pending, CreatedAtUtc = now };
SetUser(order, user);            // the navigation; the FK follows at flush

db.Persist(order);               // schedules the insert (and, declared cascades aside, nothing else)
db.Persist(user);
var summary = await db.FlushAsync(ct);   // insert users, insert transactions — user.Id flowed into order.UserId
```

## The calls

- **`Persist(entity)`** schedules an insert for an instance the session does
  not track. A tracked instance is already managed — its changes flush as
  updates — so persisting it schedules nothing. Persisting an instance
  scheduled for removal is `UOW-006`. The instance must be table-backed and
  keyed (`CRUD-003`, `CRUD-002`).
- **`Remove(entity)`** schedules a delete for a **tracked** instance; an
  instance the session does not track is `UOW-001` (it knows no row for it —
  read it first, or delete by key immediately). Removing an instance
  scheduled for insert **cancels the insert** (and the inserts its persist
  cascade scheduled) — nothing was written, so nothing is deleted.
- **`Flush()`** writes and returns a summary (inserted, updated, deleted
  counts). Afterwards the schedule is empty; inserted instances are tracked
  with their generated keys, updated ones re-snapshotted for the written
  columns, deleted ones forgotten — exactly as the immediate operations do,
  because **every statement is the same generated CRUD** (crud.md,
  ADR-0028): the identity gate (`UOW-002`), the version rules (`CRUD-010`),
  and `CRUD-004` apply unchanged.
- `HasPendingWork` tells whether anything is scheduled; dirty tracked
  instances flush regardless of it.

Immediate `Insert`/`Update`/`Delete` keep working in a tracked session beside
the unit of work.

## Order of writes

1. **Inserts**, each after every scheduled parent it references — through a
   non-null many-to-one navigation, or as a cascaded child of its owner.
   Among independent instances: **types in foreign-key order** (a type after
   every type its foreign keys reference: its many-to-one targets, and the
   owners whose one-to-many/one-to-one point at it), then **schedule order**.
   A cycle among the scheduled instances is `UOW-004`, before any SQL.
2. **Updates**: every dirty tracked instance not scheduled for removal, as a
   column-list update of its dirty columns (session.md), types in the same
   order, then identity-map registration order. Clean instances write
   nothing.
3. **Deletes**, children first: a parent after every scheduled child that
   references it; types in reversed foreign-key order, then schedule order.

The order is observable — `flush-cases/` pins it statement by statement —
and deterministic for the same schedule.

**Keys flow before each insert**: every non-null many-to-one navigation
copies its target's key into the FK properties (the navigation is
authoritative inside the unit of work), and a child a persist cascade
reached takes its owner's key into the FK the relationship declares — so a
database-generated parent key reaches its children with no code of yours.

## Transactions

`Flush` runs inside the session's transaction when one is active and leaves
it open; otherwise it begins one, commits it after the last statement, and
rolls it back on failure so nothing applied. A failed flush **keeps the
schedule** (fix and flush again) and, per session.md, does not touch the
identity map or the snapshots — `Refresh` is the way back for instances the
database no longer agrees with.

## Cascades — declared, never inferred

`[Cascade(CascadeOperations.Persist | Remove)]` on a navigation
(metadata-model.md, exported as `"cascade": ["persist", "remove"]`):

- **Persist** cascades `Persist` to the navigation's targets — a single
  reference, or every member of an initialized collection — and records the
  key each child takes from its owner at flush. It never loads: a
  never-loaded collection of a tracked instance cascades nothing.
- **Remove** cascades `Remove` to the targets. A loaded collection removes
  its members; a **never-loaded collection is `UOW-005`** — nothing loads
  implicitly, so load it or remove the children yourself. A null singular
  navigation cascades nothing (null is also what "unloaded" looks like; the
  database's foreign key is the authority that refuses an orphaning delete).
- A `many_to_many` navigation cannot cascade: its link rows are entities the
  unit of work persists and removes explicitly (`MAP-025` at declaration).

The fixture: `Transaction.Details` cascades both.

## Batching

A flush issues one statement per row through the session's connection. A
provider batch (C# `DbBatch` on `net6+`) is an execution detail a port may
add; it changes nothing observable — the statement sequence, the generated
keys, and the per-row version checks stay as specified. The Level 3 exit
benchmark decides whether the reference adds it.

## Conformance: `flush-cases/`

The tracking-case step vocabulary (session.md) plus: `persist` and `remove`
(`ref`); `flush` with `expectStatements` — the ordered list the dialect was
asked to render, each `{ "op": "insert" | "update" | "delete", "entity",
"columns"? }` (`columns` for updates: the property list, or `"all"`) — and
`expectSummary` (`inserted`, `updated`, `deleted`); `new` may carry
`navigations` (`{ "User": "u", "Details": ["d1", "d2"] }` — refs to earlier
instances); `begin`, `commit`, `rollback` for the session transaction;
expectations `pending` (`HasPendingWork`), `sameValue` (two `ref.Property`
paths with equal values), and `count` (`entity`, `equals` — the relation's
row count, read outside the session so a rolled-back flush is visible).

```json
{ "name": "…", "tracking": "tracked",
  "steps": [
    { "new": { "entity": "User", "as": "u", "values": { "Name": "Linus", "Email": "linus@example.com", "CreatedAtUtc": "…" } } },
    { "new": { "entity": "Transaction", "as": "t", "values": { "Status": "Pending", "Amount": "5", "CreatedAtUtc": "…" }, "navigations": { "User": "u" } } },
    { "persist": { "ref": "t" } },
    { "persist": { "ref": "u" } },
    { "flush": { "expectStatements": [ { "op": "insert", "entity": "User" }, { "op": "insert", "entity": "Transaction" } ],
                 "expectSummary": { "inserted": 2, "updated": 0, "deleted": 0 } } },
    { "expect": { "sameValue": ["t.UserId", "u.Id"] } } ] }
```

## Clarifications (the PHP and Go M2 ports asked; ADR-0036)

- **A failed flush undoes its in-memory effects.** Every statement that ran
  before the failure is reverted in memory — an insert's key and tracking, an
  update's snapshot and version, a delete's tracking — so the session is as it
  was before the flush and **the whole schedule flushes again as it was**.
  The database is rolled back when the flush owned the transaction; inside a
  caller's transaction the rows that ran are the caller's to roll back (a
  caller who commits after a failed flush has rows the session does not
  know — `Refresh` is the way back, as after any rollback). (The reference
  left memory half-applied until ADR-0036; the Go port's undo journal became
  the rule.)
- **Persist of a tracked instance schedules nothing for itself but still
  cascades** over its initialized navigations — a new child in a loaded
  collection is scheduled by persisting its loaded owner. Persisting an
  instance already scheduled is a no-op.
- **Order of refusals on `Persist`**: the instance's own shape first
  (`CRUD-003`, `CRUD-002`), then `UOW-006`, then the cascade, each target
  checked the same way; **a refusal anywhere schedules nothing** (plan, then
  commit). `Remove` likewise: shape, `UOW-001`, then the cascade (`UOW-005`,
  `UOW-001` for a target the session neither tracks nor has scheduled).
- **Cancelling a scheduled insert** cancels, transitively, the inserts its
  persist cascade scheduled — except a child that was **persisted directly by
  name**, or one another scheduled owner still points at. Those stay
  scheduled; cancel them yourself. Removing a scheduled insert runs no
  remove cascade (nothing was written).
- **Schedule order is pre-order**: the owner, then its cascade targets in
  navigation order, depth first; an instance already scheduled keeps its
  earlier position. A many-to-one persist cascade schedules the referenced
  parent the same way.
- **`UOW-005` is for a never-loaded collection only.** A singular navigation
  cannot be told apart from "unloaded" (null is both), so it cascades
  nothing; a loaded-but-empty collection cascades nothing and refuses
  nothing. Removing an instance already scheduled for removal is a no-op.
- **Dependencies are navigations, never FK values**: an instance edge exists
  only through a non-null many-to-one navigation to a scheduled instance, or
  a cascade link; a self-referencing edge is ignored. Ties: the lowest (type
  rank, schedule order) among the ready instances goes next.
- **Type rank** is computed over the flush's types from the relationship
  metadata (a type after its many-to-one targets and after the owners whose
  one-to-many/one-to-one point at it); **types in a dependency cycle share a
  rank** and the schedule decides inside it.
- **Registration order** is the latest registration: an instance
  re-registered (deleted and inserted again, or a key reused) takes a new
  position.
- **"Dirty" for the update phase** means a column an update would write: an
  instance whose only differences are key, version, or generated values is
  clean. `Updated` counts statements. A dirty tracked instance of a read-only
  source is **not skipped**: its update refuses `CRUD-003` and the flush
  fails — never a silently ignored change.
- **Work done immediately is dropped at flush**: a scheduled insert already
  written by `Insert` (it is tracked now), or a scheduled removal already
  deleted by `Delete` (it is not), is done, not work — no duplicate
  statement, no refusal.
- **An empty flush** — nothing scheduled, nothing dirty — returns the zero
  summary and begins no transaction.
- **Keys flow only before inserts**, copying the target's key values as they
  are (a null or zero part included — the database foreign key is the
  authority); a dirty tracked instance's navigation is not copied into its
  FK before its update, a disagreement there is `CRUD-004` as on an
  immediate update. The navigation consistency check (`CRUD-004`) runs
  inside each flushed insert, after the keys flowed.
- **Deletes use the entity form**: identity-gated, version-checked
  (`CRUD-010`). An untracked instance whose key another tracked instance
  holds is `UOW-002` at flush, from the identity gate inside the insert.
- **`HasPendingWork` is a question, not a unit-of-work call**: false on an
  untracked session, never `UOW-003`; it counts the schedule only — dirty
  tracked instances flush regardless.
- **`MAP-025` covers every misplaced cascade**: a column, an ignored
  property, an owned navigation or an owned type's member, a many-to-many
  navigation, and a declaration naming no operation.
- **Conformance**: `expectStatements` compares the ordered sequence; an
  update's `columns` compare as the property list in metadata order (a
  runner may compare as a set, as for `expectWritten`); `entity` is the
  short type name as the reference declares it.

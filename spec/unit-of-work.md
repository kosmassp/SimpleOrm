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

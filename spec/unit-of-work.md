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
database no longer agrees with. Under `Batching = Auto` a failure inside the
caller's transaction may leave rows of statements *after* the failing one in
its message ("Batching" below): roll the transaction back.

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

A flush is **one call** in the sense of session.md "Batching": under `Batching =
Auto` its statements — the same ones, in the order above — go out in as few
messages as the plan allows. The plan's dependency rule is what keeps keys
flowing: an insert whose parent (its many-to-one navigation, or the cascade owner
it takes its key from) is inserted in the current message with a key the database
generates starts the next message, after the parent's key came back and flowed
into it. A graph of parents and children with generated keys therefore flushes
in as many messages as it has levels, not one round trip per row; updates and
deletes join the message in progress unless a rule cuts it (an update whose
many-to-one navigation points at an instance just inserted in it, the size cap).

Nothing else changes with the mode (session.md "Batching", Clarifications 1–9,
state the few edges where a failing call reports differently): the statement
order, the generated keys, the per-row version checks (`CRUD-010` per statement), the summary, and the undo of a
failed flush are as specified above — `flush-cases/` pin them and every runner
runs them in both modes; `batch-cases/` pin the grouping. A failed batched flush
is the failure of session.md "Batching": everything it did in memory is undone,
its own transaction rolls back, and in a caller's transaction it may leave rows
beyond the failing statement.

Until ADR-0044 the reference did not batch (ADR-0039: its SQLite provider has no
batch API and the 100-row flush measured within the cost of the same inserts one
by one). The gain is the round trip, which SQLite does not have; ADR-0044
measures it on PostgreSQL, and measures the prepared-command reuse that is
SQLite's realization.

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
  caller's transaction the rows that ran (under `Auto`, possibly some after the
  failing statement, session.md "Batching") are the caller's to roll back (a
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
- **Type rank** is computed over the flush's types **and every type reachable
  from them through relationships** (a scheduled pair related only through a
  third, unscheduled type still ranks in dependency order), from the relationship
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

## Detach and merge (Level 3 M3)

The simple rules, no deep graph merge; both need a tracked session
(`UOW-003`).

- **`Detach(entity)`** makes the session forget the instance: it is no
  longer the session's instance for its key (a later read materializes a
  fresh one), and anything scheduled for it is cancelled — an insert with
  the inserts its persist cascade scheduled, a removal dropped. The instance
  keeps its values. An instance the session neither tracks nor has scheduled
  is `UOW-001`.
- **`Merge(detached)`** brings a detached copy's state back and **returns the
  session's instance** for its key: the tracked one when the key is known,
  otherwise the row read now (one visible round trip; no row is `CRUD-001`).
  Every mapped non-key value of the copy is written onto that instance —
  the version included, so a stale copy still fails its next update's
  version check (`CRUD-010`) — which becomes dirty where the values
  differed. Navigations are not merged. The copy itself stays untracked. An
  instance without a complete key is `CRUD-002`: new instances are
  persisted, not merged. Merging the session's own instance is a no-op that
  returns it.

Conformance: `merge-cases/`, the same step vocabulary plus `detach` (`ref`)
and `merge` (`ref`, `as` — the returned instance).

**Clarifications on detach and merge** (the PHP and Go M3 ports asked; ADR-0037):

- **Detach of a tracked owner cancels nothing it scheduled**: children its
  persist cascade scheduled stay scheduled and still take its key at flush.
  Detach of an instance scheduled for removal drops that removal only; the
  removals its remove cascade scheduled stay. Detach of a scheduled insert
  cancels it with what its persist cascade scheduled, by the cancel rule.
- **Merge needs a table-backed, keyed entity** (`CRUD-003`, `CRUD-002`),
  decided at merge time — a view's copy cannot be merged. The session's own
  instance merges as a no-op before any key check. A scheduled-but-untracked
  instance is an ordinary copy; a tracked target scheduled for removal still
  takes the copy's values. Audit columns merge like any other non-key value;
  owned members are copied member by member; nothing is aliased.
- **"Unset key"** for merge: null, a database-generated key at its default, or
  an empty client-generated GUID — a natural key's default value is a key.

## Clarifications (the Level 3 exit audit; ADR-0039)

The exit's proof was a design for a Python port — a synchronous driver, no
static types, decorators and plain attributes — written from `spec/` and
`conformance/` alone. Its questions, answered against the reference:

- **Two meanings of "dirty".** `ChangesOf`/`IsDirty` report every mapped
  property whose database-bound value differs from the snapshot — the
  `[UpdatedAt]` column included. The **write trigger** — `Update` of a tracked
  instance, the flush's update phase — ignores key, version, generated
  columns, **and the `[UpdatedAt]` column**: an instance differing only there
  is clean and writes nothing. A changed `[CreatedAt]` is an ordinary column
  and writes.
- **A value the converter refuses** fails the question that asks for it:
  `ChangesOf`/`IsDirty` compute the database-bound form, so an unconvertible
  current value raises the converter's own code (`MAP-030`, `MAP-031`,
  `VAL-020`) — the error the write would have raised. Nothing is "dirty" that
  the database could not receive.
- **Assigning a navigation in user code.** The library writes navigations
  when it loads; user code writes them through the entity's own means — a
  constructor, a method of the entity (`SetUser(order, user)` in the example
  above is such a method), an initializer — never through a public setter
  (`MAP-011`). Where the language has no setter to forbid (an attribute, a
  field), the assignment is plain and the port documents the intent. A
  runner's `navigations` key uses whatever the fixture provides.
- **Type rank is a depth, never a sequence.** A type that references no other
  type — through its many-to-one navigations, or by being the target of
  another type's one-to-many/one-to-one — has rank 0; otherwise its rank is
  one more than the highest rank among the types it references; the members
  of a cycle share one rank; the graph spans every type reachable from the
  flush's types; many-to-many links contribute no edge (the link entity ranks
  through its own many-to-one navigations). Rank is a property of the
  metadata alone: unrelated types tie and their instances **interleave in
  schedule order** (role, user, role — never grouped by type). Inserts take
  the lowest (rank, schedule order) among the ready instances; deletes the
  highest rank first, then schedule order; the update phase orders by rank,
  then registration order. Pinned by `unrelated_types_interleave_in_schedule_order`
  (the reference numbered components in discovery order until the exit; both
  ports had computed the depth).
- **Delete edges mirror insert edges.** A scheduled parent waits for every
  scheduled child that references it (the child's non-null many-to-one
  navigation); a scheduled owner waits for the scheduled members of its loaded
  collection or one-to-one navigation. A cycle among deletes is `UOW-004`
  before any SQL, exactly as for inserts.
- **The summary counts intents**, not renderings: `Deleted` counts the
  removals the flush carried out — a soft delete is a removal, never an
  update; `Updated` counts the update phase's statements.
- **`UOW-003` is first**: an untracked session refuses `Persist`, `Remove`,
  `Detach`, `Merge`, and `Flush` before looking at the argument.
- **Arguments.** A null argument is the language's argument error. Any other
  object is loaded as an entity: a type the loaders refuse fails with the
  loader's code; a keyless or read-only type with `CRUD-002`/`CRUD-003`.
  Identity is keyed by the instance's exact runtime type.
- **Any failure inside a flush** — a provider error, a refusal, a cancellation
  — rolls back the flush's own transaction, undoes the in-memory effects,
  keeps the schedule, and propagates unchanged: provider errors are never
  wrapped in a SimpleOrm code, cancellation stays the platform's signal.
- **Disposing a session with scheduled work drops it silently.** Nothing is
  ever flushed implicitly.
- **What does I/O.** `Flush`, `Merge`, `Refresh` — and every read, load, and
  immediate write — are round trips; `Persist`, `Remove`, `Detach`,
  `IsTracked`, `ChangesOf`, `IsDirty`, `HasPendingWork` are pure. "Async with
  a cancellation token" is the shape in languages that have it; a synchronous
  port blocks and uses its platform's interruption idiom. The contract is that
  every round trip is a visible call.
- **An instance scheduled for removal leaves the update phase**: its unsaved
  edits are discarded with it; a soft delete writes the flag and the stamp
  only.
- **The map holds strong references** for the session's lifetime; a session
  is a unit of work, not a process-wide cache — a long-lived one reading many
  rows is a long-lived cache, so dispose it or `Detach`. Per-instance
  bookkeeping lives in the session, never on the instance.
- **Merge and refresh copy an owned navigation as a whole**: null when the
  source's navigation is null — for refresh, when the row's members are all
  null — otherwise an owned object carrying the source's members, created when
  the target had none. Nothing is aliased: the owned object, byte arrays, and
  objects held in JSON columns are the target's own copies, never shared
  between the two instances. The step scripts observe the navigation itself:
  `value` may name an owned navigation, with `equals` `null` or `{}`. Pinned by
  `merge_and_refresh_apply_the_owned_rule`.
- **"Never loaded" belongs to an instance materialized from a row** and is
  cleared only by a load (`Load`, `LoadEach`, `Include`); insert, update,
  merge, refresh, and detach leave every navigation as it is. An instance
  constructed by user code holds whatever collection it holds — loaded, even
  when empty. `UOW-005` names the first state only. Singular navigations are
  never guarded — null is both "unloaded" and "none" — even where the
  language could intercept the read.
- **Delete is one operation in two forms**, by key and by entity. A language
  dispatches on the argument (a mapped entity instance is the entity form,
  anything else a key) or names the forms separately (Go `Delete` /
  `DeleteEntity`); the soft-delete and `CRUD-012` rules follow the form, not
  the name.
- **Declarations given as tokens** (strings, tags): an unknown, blank, or
  repeated token is `MAP-023` — the code exists for every port whose
  declarations are not type-checked, not only Go; a cascade declaration
  naming no operation is `MAP-025`. The export writes `cascade` in the fixed
  order `persist`, `remove`, whatever order the declaration used, as the
  **last key of the relationship object**.

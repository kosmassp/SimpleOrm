# The unit of work — schedule, then flush (Level 3 M2, ADR-0034 §7c)

The unit of work is the second half of tracking (session.md "Tracking"): a
**tracked** session schedules inserts and removals, knows which tracked
instances are dirty, and writes all of it in one explicit `Flush` — in
foreign-key order, with generated keys flowing into dependents, inside one
transaction. Nothing is written until `Flush`; nothing loads to decide what to
write. An untracked session has no snapshots to flush from: every unit-of-work
call there — `Persist`, `Remove`, `Detach`, `Merge`, `Flush` — is `UOW-003`,
decided first, before the argument is looked at (ADR-0039).

```csharp
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
var order = new Transaction { Amount = 5m, Status = TransactionStatus.Pending, CreatedAtUtc = now };
SetUser(order, user);            // the navigation; the FK follows at flush
                                 // (SetUser stands for the entity's own means of assigning it, below)

db.Persist(order);               // schedules the insert (and, declared cascades aside, nothing else)
db.Persist(user);
var summary = await db.FlushAsync(ct);   // insert users, insert transactions — user.Id flowed into order.UserId
```

**Assigning a navigation in user code.** The library writes navigations when
it loads; user code writes them through the entity's own means — a
constructor, a method of the entity (`SetUser` above), an initializer — never
through a public setter (`MAP-011`). Where the language has no setter to forbid
(an attribute, a field), the assignment is plain and the port documents the
intent. A runner's `navigations` key uses whatever the fixture provides
(ADR-0039).

## The calls

- **`Persist(entity)`** schedules an insert for an instance the session does
  not track, then cascades (below). Persisting an instance already scheduled
  is a no-op. A tracked instance is already managed — its changes flush as
  updates — so persisting it schedules nothing for itself, but it **still
  cascades** over its initialized navigations: a new child in a loaded
  collection is scheduled by persisting its loaded owner (ADR-0036).
  Persisting an instance scheduled for removal is `UOW-006`. Refusals come in
  order — the instance's own shape (`CRUD-003` read-only, `CRUD-002` keyless),
  then `UOW-006`, then the cascade, each target checked the same way — and
  **a refusal anywhere schedules nothing**: the call plans, then commits
  (ADR-0036).
- **Schedule order is pre-order**: the owner, then its cascade targets in
  navigation order, depth first; an instance already scheduled keeps its
  earlier position. A many-to-one persist cascade schedules the referenced
  parent the same way (ADR-0036).
- **`Remove(entity)`** schedules a delete for a **tracked** instance; an
  instance the session does not track is `UOW-001` (it knows no row for it —
  read it first, or delete by key immediately). Refusals: shape, `UOW-001`,
  then the cascade (`UOW-005`, `UOW-001` for a target the session neither
  tracks nor has scheduled); a refusal schedules nothing. Removing an instance
  already scheduled for removal is a no-op (ADR-0036).
- **Removing an instance scheduled for insert cancels the insert** — nothing
  was written, so nothing is deleted, and no remove cascade runs. The cancel
  is transitive over the inserts its persist cascade scheduled, **except** a
  child that was persisted directly by name, or one another scheduled owner
  still points at: those stay scheduled; cancel them yourself (ADR-0036).
- **`Flush()`** writes and returns a summary (inserted, updated, deleted
  counts). Afterwards the schedule is empty; inserted instances are tracked
  with their generated keys, updated ones re-snapshotted for the written
  columns, deleted ones forgotten — exactly as the immediate operations do,
  because **every statement is the same generated CRUD** (crud.md,
  ADR-0028): the identity gate (`UOW-002`), the version rules (`CRUD-010`),
  and `CRUD-004` apply unchanged.
  - **The summary counts intents**, not renderings: `Deleted` counts the
    removals the flush carried out — a soft delete is a removal, never an
    update; `Updated` counts the update phase's statements (ADR-0039).
  - **An empty flush** — nothing scheduled, nothing dirty — returns the zero
    summary and begins no transaction (ADR-0036).
  - **Work done immediately is dropped at flush**: a scheduled insert already
    written by `Insert` (it is tracked now), or a scheduled removal already
    deleted by `Delete` (it is not), is done, not work — no duplicate
    statement, no refusal (ADR-0036).
- **`HasPendingWork`** tells whether anything is scheduled. It is a question,
  not a unit-of-work call: false on an untracked session, never `UOW-003`; it
  counts the schedule only — dirty tracked instances flush regardless
  (ADR-0036).

Immediate `Insert`/`Update`/`Delete` keep working in a tracked session beside
the unit of work.

### Arguments, I/O, and lifetime

- **Arguments.** A null argument is the language's argument error (C#
  `ArgumentNullException`, PHP `TypeError`) — for the unit-of-work calls and
  for a hook-bearing write alike; Go, which cannot refuse a nil pointer by
  type, reports `UOW-001` for `Detach` and `CRUD-002` for `Merge` (ADR-0037).
  Any other object is loaded as an entity: a type the loaders refuse fails with
  the loader's code; a keyless or read-only type with `CRUD-002`/`CRUD-003`.
  Identity is keyed by the instance's exact runtime type (ADR-0039).
- **Delete is one operation in two forms**, by key and by entity. A language
  dispatches on the argument (a mapped entity instance is the entity form,
  anything else a key) or names the forms separately (Go `Delete` /
  `DeleteEntity`); the soft-delete and `CRUD-012` rules follow the form, not
  the name (ADR-0039).
- **What does I/O.** `Flush`, `Merge`, `Refresh` — and every read, load, and
  immediate write — are round trips; `Persist`, `Remove`, `Detach`,
  `IsTracked`, `ChangesOf`, `IsDirty`, `HasPendingWork` are pure. "Async with
  a cancellation token" is the shape in languages that have it; a synchronous
  port blocks and uses its platform's interruption idiom. The contract is that
  every round trip is a visible call (ADR-0039).
- **The map holds strong references** for the session's lifetime; a session
  is a unit of work, not a process-wide cache — a long-lived one reading many
  rows is a long-lived cache, so dispose it or `Detach`. Per-instance
  bookkeeping lives in the session, never on the instance (ADR-0039).
- **Disposing a session with scheduled work drops it silently.** Nothing is
  ever flushed implicitly (ADR-0039).

## Order of writes

1. **Inserts**, each after every scheduled parent it references — through a
   non-null many-to-one navigation, or as a cascaded child of its owner.
   Among the ready instances the lowest (type rank, schedule order) goes
   next. A cycle among the scheduled instances is `UOW-004`, before any SQL.
2. **Updates**: every dirty tracked instance not scheduled for removal, as a
   column-list update of its dirty columns (session.md), ordered by type rank,
   then identity-map registration order. Clean instances write nothing.
3. **Deletes**, children first: a scheduled parent waits for every scheduled
   child that references it (the child's non-null many-to-one navigation), and
   a scheduled owner waits for the scheduled members of its loaded collection
   or one-to-one navigation — the mirror of the insert edges; then the highest
   type rank first, then schedule order. A cycle among deletes is `UOW-004`
   before any SQL, exactly as for inserts (ADR-0039).

The order is observable — `flush-cases/` pin it statement by statement —
and deterministic for the same schedule.

**Dependencies are navigations, never FK values**: an instance edge exists
only through a non-null many-to-one navigation to a scheduled instance, or a
cascade link; a self-referencing edge is ignored (ADR-0036).

**Type rank is a depth, never a sequence** (ADR-0039). It is a property of the
relationship metadata alone, computed over the flush's types **and every type
reachable from them through relationships** — a scheduled pair related only
through a third, unscheduled type still ranks in dependency order (ADR-0036).
A type that references no other type — through its many-to-one navigations, or
by being the target of another type's one-to-many/one-to-one — has rank 0;
otherwise its rank is one more than the highest rank among the types it
references; **the members of a dependency cycle share one rank** and the
schedule decides inside it; many-to-many links contribute no edge (the link
entity ranks through its own many-to-one navigations). Unrelated types tie and
their instances **interleave in schedule order** (role, user, role — never
grouped by type), pinned by `unrelated_types_interleave_in_schedule_order`
(the reference numbered components in discovery order until ADR-0039; both
ports had computed the depth).

**Registration order** is the latest registration: an instance re-registered
(deleted and inserted again, or a key reused) takes a new position (ADR-0036).

**"Dirty" has two meanings** (ADR-0036, ADR-0039). `ChangesOf`/`IsDirty`
report every mapped property whose database-bound value differs from the
snapshot — the `[UpdatedAt]` column included. The **write trigger** — `Update`
of a tracked instance, the flush's update phase — counts only a column an
update would write: it ignores key, version, and generated columns **and the
`[UpdatedAt]` column**, so an instance differing only there is clean and
writes nothing. A changed `[CreatedAt]` is an ordinary column and writes.
**A value the converter refuses** fails the question that asks for it:
`ChangesOf`/`IsDirty` compute the database-bound form, so an unconvertible
current value raises the converter's own code (`MAP-030`, `MAP-031`,
`VAL-020`) — the error the write would have raised; nothing is "dirty" that
the database could not receive (ADR-0039).

A dirty tracked instance of a read-only source is **not skipped**: its update
refuses `CRUD-003` and the flush fails — never a silently ignored change. An
instance scheduled for removal leaves the update phase: its unsaved edits are
discarded with it, and a soft delete writes the flag and the stamp only
(ADR-0036, ADR-0039).

**Keys flow only before inserts**: every non-null many-to-one navigation
copies its target's key into the FK properties (the navigation is
authoritative inside the unit of work), and a child a persist cascade reached
takes its owner's key into the FK the relationship declares — so a
database-generated parent key reaches its children with no code of yours. The
key values are copied as they are (a null or zero part included — the
database foreign key is the authority), and the navigation consistency check
(`CRUD-004`) runs inside each flushed insert, after the keys flowed. A dirty
tracked instance's navigation is not copied into its FK before its update; a
disagreement there is `CRUD-004` as on an immediate update (ADR-0036).

**Deletes use the entity form**: identity-gated, version-checked
(`CRUD-010`). An untracked instance whose key another tracked instance holds
is `UOW-002` at flush, from the identity gate inside the insert (ADR-0036).

## Transactions

`Flush` runs inside the session's transaction when one is active and leaves
it open; otherwise it begins one, commits it after the last statement, and
rolls it back on failure so nothing applied.

**Any failure inside a flush** — a provider error, a refusal, a cancellation
— rolls back the flush's own transaction, **undoes its in-memory effects**,
keeps the schedule, and propagates unchanged: provider errors are never
wrapped in a SimpleOrm code, cancellation stays the platform's signal
(ADR-0039). Undoing means every statement that ran before the failure is
reverted in memory — an insert's key and tracking, an update's snapshot and
version, a delete's tracking, and the hook values of hooks.md — so the session
is as it was before the flush and **the whole schedule flushes again as it
was** (fix and flush again). (The reference left memory half-applied until
ADR-0036; the Go port's undo journal became the rule.) Inside a caller's
transaction the rows that ran — under `Batching = Auto` possibly some *after*
the failing statement in its message ("Batching" below) — are the caller's to
roll back; a caller who commits after a failed flush has rows the session does
not know, and `Refresh` is the way back, as after any rollback.

## Cascades — declared, never inferred

`[Cascade(CascadeOperations.Persist | Remove)]` on a navigation
(metadata-model.md, exported as `"cascade": ["persist", "remove"]` — always in
the fixed order `persist`, `remove` whatever order the declaration used, as
the **last key of the relationship object**, ADR-0039):

- **Persist** cascades `Persist` to the navigation's targets — a single
  reference, or every member of an initialized collection — and records the
  key each child takes from its owner at flush. It never loads: a
  never-loaded collection of a tracked instance cascades nothing.
- **Remove** cascades `Remove` to the targets. A loaded collection removes
  its members, and a loaded-but-empty collection cascades nothing and refuses
  nothing; a **never-loaded collection is `UOW-005`** — nothing loads
  implicitly, so load it or remove the children yourself. A null singular
  navigation cascades nothing (null is both "unloaded" and "none"; the
  database's foreign key is the authority that refuses an orphaning delete)
  (ADR-0036).
- **"Never loaded" belongs to an instance materialized from a row** and is
  cleared only by a load (`Load`, `LoadEach`, `Include`); insert, update,
  merge, refresh, and detach leave every navigation as it is. An instance
  constructed by user code holds whatever collection it holds — loaded, even
  when empty. `UOW-005` names the first state only. Singular navigations are
  never guarded, even where the language could intercept the read (ADR-0039).
- A `many_to_many` navigation cannot cascade: its link rows are entities the
  unit of work persists and removes explicitly (`MAP-025` at declaration).
- **`MAP-025` covers every misplaced cascade**: a column, an ignored
  property, an owned navigation or an owned type's member, a many-to-many
  navigation, and a declaration naming no operation (ADR-0036). Where
  declarations are given as tokens (strings, tags), an unknown, blank, or
  repeated token is `MAP-023` — the code exists for every port whose
  declarations are not type-checked, not only Go (ADR-0039).

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

Nothing else changes with the mode (session.md "Batching", its plan and
**Failure** paragraphs, state the few edges where a failing call reports
differently): the statement
order, the generated keys, the per-row version checks (`CRUD-010` per statement), the summary, and the undo of a
failed flush are as specified above — `flush-cases/` pin them and every runner
runs them in both modes; `batch-cases/` pin the grouping. A failed batched flush
is the failure of session.md "Batching": everything it did in memory is undone,
its own transaction rolls back, and in a caller's transaction it may leave rows
beyond the failing statement.

The gain is the round trip: ADR-0044 measures it on PostgreSQL, and measures
the prepared-command reuse that is SQLite's realization (before ADR-0044 the
reference did not batch — ADR-0039: its SQLite provider has no batch API).

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

`expectStatements` compares the ordered sequence; an update's `columns`
compare as the property list in metadata order (a runner may compare as a
set, as for `expectWritten`); `entity` is the short type name as the
reference declares it (ADR-0036).

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

## Detach and merge (Level 3 M3)

The simple rules, no deep graph merge; both need a tracked session
(`UOW-003`).

- **`Detach(entity)`** makes the session forget the instance: it is no
  longer the session's instance for its key (a later read materializes a
  fresh one), and the instance keeps its values. An instance the session
  neither tracks nor has scheduled is `UOW-001`. What is cancelled is only
  what is scheduled **for the instance itself** (ADR-0037):
  - a scheduled insert is cancelled with the inserts its persist cascade
    scheduled, by the cancel rule of "The calls";
  - a scheduled removal is dropped — that removal only; the removals its
    remove cascade scheduled stay;
  - a tracked owner's detach cancels nothing it scheduled: children its
    persist cascade scheduled stay scheduled and still take its key at flush.
- **`Merge(detached)`** brings a detached copy's state back and **returns the
  session's instance** for its key: the tracked one when the key is known,
  otherwise the row read now (one visible round trip; no row is `CRUD-001`).
  Every mapped non-key value of the copy is written onto that instance —
  the version included, so a stale copy still fails its next update's
  version check (`CRUD-010`) — which becomes dirty where the values
  differed. Audit columns merge like any other non-key value. Navigations are
  not merged. The copy itself stays untracked. Merging the session's own
  instance is a no-op that returns it, decided before any key check.
  - Merge needs a table-backed, keyed entity (`CRUD-003`, `CRUD-002`),
    decided at merge time — a view's copy cannot be merged. An instance with
    an **unset key** is `CRUD-002`: new instances are persisted, not merged.
    "Unset key" is null, a database-generated key at its default (`0`), or an
    empty client-generated GUID — a natural key's default value is a key
    (ADR-0037; hooks.md "Unset").
  - A scheduled-but-untracked instance is an ordinary copy; a tracked target
    scheduled for removal still takes the copy's values (ADR-0037).
  - A copy's **uninitialized** property (PHP: a typed property never
    assigned) is not merged — there is no value to copy; everything else is
    copied as is (ADR-0037).
  - **An owned navigation is copied as a whole** — by merge and by refresh:
    null when the source's navigation is null (for refresh, when the row's
    members are all null), otherwise an owned object carrying the source's
    members, created when the target had none. Nothing is aliased: the owned
    object, byte arrays, and objects held in JSON columns are the target's own
    copies, never shared between the two instances (ADR-0039).

Conformance: `merge-cases/`, the same step vocabulary plus `detach` (`ref`)
and `merge` (`ref`, `as` — the returned instance). The step scripts observe an
owned navigation itself: `value` may name an owned navigation, with `equals`
`null` or `{}`; pinned by `merge_and_refresh_apply_the_owned_rule` (ADR-0039).

## Where the Clarifications went

The three lists were never numbered; by entry title.

| Clarification | → section |
|---|---|
| A failed flush undoes its in-memory effects (ADR-0036) | "Transactions" |
| Persist of a tracked instance … still cascades (ADR-0036) | "The calls" |
| Order of refusals on `Persist` (ADR-0036) | "The calls" |
| Cancelling a scheduled insert (ADR-0036) | "The calls" |
| Schedule order is pre-order (ADR-0036) | "The calls" |
| `UOW-005` is for a never-loaded collection only (ADR-0036) | "Cascades — declared, never inferred"; the no-op removal in "The calls" |
| Dependencies are navigations, never FK values (ADR-0036) | "Order of writes" |
| Type rank (ADR-0036) | "Order of writes" |
| Registration order (ADR-0036) | "Order of writes" |
| "Dirty" for the update phase (ADR-0036) | "Order of writes" |
| Work done immediately is dropped at flush (ADR-0036) | "The calls" |
| An empty flush (ADR-0036) | "The calls" |
| Keys flow only before inserts (ADR-0036) | "Order of writes" |
| Deletes use the entity form (ADR-0036) | "Order of writes" |
| `HasPendingWork` is a question (ADR-0036) | "The calls" |
| `MAP-025` covers every misplaced cascade (ADR-0036) | "Cascades — declared, never inferred" |
| Conformance (ADR-0036) | "Conformance: `flush-cases/`" |
| Detach of a tracked owner cancels nothing it scheduled (ADR-0037) | "Detach and merge" |
| Merge needs a table-backed, keyed entity (ADR-0037) | "Detach and merge" — "owned members are copied member by member" superseded by ADR-0039's whole-object rule |
| "Unset key" for merge (ADR-0037) | "Detach and merge" |
| Two meanings of "dirty" (ADR-0039) | "Order of writes" |
| A value the converter refuses (ADR-0039) | "Order of writes" |
| Assigning a navigation in user code (ADR-0039) | the opening section, after the example |
| Type rank is a depth, never a sequence (ADR-0039) | "Order of writes" |
| Delete edges mirror insert edges (ADR-0039) | "Order of writes" |
| The summary counts intents (ADR-0039) | "The calls" |
| `UOW-003` is first (ADR-0039) | the opening section |
| Arguments (ADR-0039) | "Arguments, I/O, and lifetime" |
| Any failure inside a flush (ADR-0039) | "Transactions" |
| Disposing a session with scheduled work (ADR-0039) | "Arguments, I/O, and lifetime" |
| What does I/O (ADR-0039) | "Arguments, I/O, and lifetime" |
| An instance scheduled for removal leaves the update phase (ADR-0039) | "Order of writes" |
| The map holds strong references (ADR-0039) | "Arguments, I/O, and lifetime" |
| Merge and refresh copy an owned navigation as a whole (ADR-0039) | "Detach and merge" |
| "Never loaded" belongs to an instance materialized from a row (ADR-0039) | "Cascades — declared, never inferred" |
| Delete is one operation in two forms (ADR-0039) | "Arguments, I/O, and lifetime" |
| Declarations given as tokens (ADR-0039) | "Cascades — declared, never inferred" |
| hooks.md: A null/nil entity (ADR-0037) | "Arguments, I/O, and lifetime" |
| hooks.md: A copy's uninitialized property (ADR-0037) | "Detach and merge" |

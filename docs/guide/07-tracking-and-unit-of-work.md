# Tracking and the unit of work

Tracking is a session mode chosen at open. The default is untracked, which is
everything on the previous pages exactly: every read materializes a fresh
instance and `UpdateAsync` writes the full row. A tracked session adds an
identity map, change snapshots, and a unit of work that schedules writes and
flushes them in one ordered step. The rules are in
[`spec/session.md`](../../spec/session.md#tracking--the-identity-map-and-change-snapshots-level-3-m1-adr-0034-add1)
and [`spec/unit-of-work.md`](../../spec/unit-of-work.md).

## Opting in

```csharp
await using var tracked = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect(), Tracking = TrackingMode.Tracked }, ct);
```

In PHP: `new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked)`.
In Go: `orm.Options{Dialect: sqlite.New(), Tracking: orm.TrackingTracked}`.

## The identity map

A tracked session keeps one instance per (entity type, key) for its lifetime,
and consults the map on every read path that materializes an entity: registry
queries, streams, key reads, criteria queries, explicit and eager loading in
every fetch mode. DTOs, keyless entities, and rows with a null key part are
never tracked.

**The tracked instance wins.** A read that finds its key in the map returns the
tracked instance and does not apply the row's values, dirty or not.
`RefreshAsync` is the explicit overwrite.

```csharp
var a = await tracked.GetAsync<User>(7, ct);
var b = await tracked.GetAsync<User>(7, ct);          // the same instance
a.Name = "Ada Lovelace";
tracked.ChangesOf(a);                                 // ["Name"], against the snapshot taken when read
tracked.IsDirty(a);                                   // true
await tracked.UpdateAsync(a, ct);                     // writes only name (column-list path); a clean instance writes nothing
await tracked.RefreshAsync(a, ct);                    // the explicit overwrite from the row; CRUD-001 if the row is gone
tracked.IsTracked(a);                                 // never throws
```

- `ChangesOf` and `IsDirty` are `UOW-001` for an instance the session does not
  track, which includes every instance in an untracked session.
- The snapshot holds the database-bound form of each value, so "dirty" means
  "the database would receive a different value": a JSON member mutated in
  place counts, and two decimals equal in value are clean.
- `Update` on a tracked instance writes only the dirty columns through the
  column-list path, with the version bump and check as always, and writes
  nothing at all when the instance is clean. On an instance the session does
  not know it writes the full row, then tracks it.
- `UpdateOnly` moves the snapshot forward for the written columns only; an
  unwritten change stays dirty.
- `Delete` forgets the key.
- A write from an instance that is not the session's instance for its key
  (another tracked instance holds the key, or a tracked instance's key was
  changed) is `UOW-002` before any SQL. A key is identity; it never updates.
- Navigations are never part of a snapshot. A re-read leaves them as they
  were; an explicit load or include always reassigns the one it names.
- Transactions do not touch the map: a rollback reverts the database, not the
  instances. `RefreshAsync` is the way back.

In Go the pointer is the tracked shape: `orm.Get[*User]`, `orm.From[*User]`,
and `[]*T` navigations resolve through the map; a value-shaped result
(`orm.Get[User]`, `[]User`) is a copy and is never registered. `orm.ChangesOf(db, a)`,
`orm.IsDirty(db, a)`, `orm.Refresh(ctx, db, a)`, `db.IsTracked(a)`. In PHP:
`$db->changesOf($a)` (the port's property spelling: `['name']`), `$db->isDirty($a)`,
`$db->refresh($a)`, `$db->isTracked($a)`.

## The unit of work

A tracked session can schedule inserts and removals and write everything in
one explicit flush. Nothing is written, and nothing loads, until `FlushAsync`.
Every unit-of-work call on an untracked session is `UOW-003`.

```csharp
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
var order = new Transaction { Amount = 5m, Status = TransactionStatus.Pending, CreatedAtUtc = now };
SetUser(order, user);            // the navigation, through the entity's own means; the FK follows at flush

db.Persist(order);               // schedules the insert (and, declared cascades aside, nothing else)
db.Persist(user);
var summary = await db.FlushAsync(ct);   // insert users, insert transactions; user.Id flowed into order.UserId
```

`SetUser` stands for however your model assigns a navigation in user code: a
constructor, an initializer, or a method of the entity, never a public setter.

- `Persist(entity)` schedules an insert for an instance the session does not
  track. A tracked instance is already managed, so persisting it schedules
  nothing for itself (but still cascades over its initialized navigations).
  Persisting an instance scheduled for removal is `UOW-006`.
- `Remove(entity)` schedules a delete for a tracked instance; an instance the
  session does not track is `UOW-001`. Removing an instance scheduled for
  insert cancels the insert.
- `FlushAsync(ct)` writes and returns a `FlushSummary(Inserted, Updated, Deleted)`.
  Afterwards the schedule is empty, inserted instances are tracked with their
  generated keys, and deleted ones are forgotten.
- `HasPendingWork` says whether anything is scheduled (false on an untracked
  session, never an error). Dirty tracked instances flush regardless.

Immediate `InsertAsync`/`UpdateAsync`/`DeleteAsync` keep working beside the
unit of work; work done immediately is simply done at flush time.

In PHP: `$db->persist($order)`, `$db->remove($oldLine)`, `$db->flush()`
returning a `FlushSummary` with `->inserted`, `->updated`, `->deleted`, and
`$db->hasPendingWork()`. In Go: `orm.Persist(db, order)`, `orm.Remove(db, existing)`,
`db.Flush(ctx)` returning `orm.FlushSummary{Inserted, Updated, Deleted}`, and
`db.HasPendingWork()`.

## Order of writes

1. **Inserts**, each after every scheduled parent it references through a
   non-null many-to-one navigation or as a cascaded child. Among independent
   instances: types in foreign-key order, then schedule order. Keys flow before
   each insert: every non-null many-to-one navigation copies its target's key
   into the FK properties, and a cascaded child takes its owner's key, so a
   database-generated parent key reaches its children with no code of yours.
2. **Updates**: every dirty tracked instance not scheduled for removal, as a
   column-list update of its dirty columns.
3. **Deletes**, children first.

A cycle among the scheduled instances is `UOW-004`, detected before any SQL.
The flush runs inside the session's transaction when one is active and leaves
it open; otherwise it begins one, commits after the last statement, and rolls
back on failure. Every statement is the same generated CRUD, so the identity
gate (`UOW-002`), the version rules (`CRUD-010`), and `CRUD-004` apply unchanged.

## A failed flush

A failed flush rolls back its own transaction, undoes its in-memory effects
(an insert's key and tracking, an update's snapshot and version, a delete's
tracking), and keeps the schedule, so the session is as it was and the whole
schedule flushes again after you fix the cause:

```csharp
db.Persist(a);
db.Persist(b);
var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.FlushAsync(CancellationToken.None));
Assert.True(db.HasPendingWork);   // the schedule is kept
```

Inside a caller's transaction the rows that ran are the caller's to roll back.

## Batching

A flush is one call, so under `Batching = Auto` its statements go out in as few
messages as the plan allows (Level 4, ADR-0044). The statements, their order,
the generated keys, and the per-row version checks are exactly what `Off`
issues; only the round trips change. Batching is off by default.

```csharp
await using var db = await Db.OpenAsync(connectionString, new DbOptions
{
    Dialect = new PostgresDialect(),
    Tracking = TrackingMode.Tracked,
    Batching = BatchingMode.Auto,          // Off by default
    MaxStatementsPerMessage = 100,         // the default
}, ct);

foreach (var user in users) db.Persist(user);
foreach (var order in orders) db.Persist(order);   // cascades its details
await db.FlushAsync(ct);   // users | orders | details: three messages, not one round trip per row
```

**How the flush is grouped.** Walking the statements in order, a statement
starts a new message when it needs something the current message produces: the
same instance is already in it, or its many-to-one navigation (or the cascade
owner it takes its key from) points at an instance inserted in it whose key the
database generates. So a graph with generated keys flushes in as many messages
as it has levels: parents, then children (the children's message also carries
the updates and deletes), each level's keys flowing into the next. The cap
`MaxStatementsPerMessage`, and on SQL Server 2098 bound parameters, also cut.

**Nothing waits.** There is no timer and no background queue: `Auto` groups only
what one call already holds (a flush, a batch send). A lone `InsertAsync` runs
exactly as before.

**Failure.** The first statement that fails its check fails the flush, as
before, and everything the flush did in memory is undone. Because a zero-row
update is no error to the database, the statements after it in the same message
may already have run: the flush's own transaction rolls them back; inside your
transaction, roll it back yourself (you must anyway after a failed flush).

`db.BatchingInEffect` says whether a message really goes out as one round trip:
true on SQL Server and PostgreSQL with C# on `net10.0` (`SqlBatch`,
`NpgsqlBatch`), false on SQLite (no network round trip exists; `Auto` instead
reuses one prepared command per statement text) and on `netstandard2.0`. The
per-dialect table is on [Dialects](12-dialects.md#statement-batching).

In PHP: `new DbOptions(..., batching: BatchingMode::Auto)`. In Go:
`orm.Options{..., Batching: orm.BatchingAuto}`.

## The explicit batch

For immediate writes and registry commands, a batch queues the work and one
visible call sends it:

```csharp
await using var batch = db.Batch();                 // queuing does no I/O
batch.Insert(order);
batch.UpdateOnly(user, [nameof(User.DisplayName)]);
batch.Execute(Commands.MarkShipped, new MarkShippedArgs(Id: 7));
batch.Delete<Role>(3);
var result = await batch.SendAsync(ct);             // result.Affected: one count per operation; result.Messages
```

- Each operation behaves exactly as its immediate call would at that point:
  the same checks, the same statement, the same effects on the instance
  (tracking, hooks, versions). Keys do not flow between queued operations, as
  they do not between immediate calls; use the unit of work for that.
- The send always runs in a transaction (yours, or one it begins and commits),
  so a failure applies nothing and undoes every in-memory effect.
- A sent or disposed batch refuses more work (`TX-003`); disposing an unsent
  batch drops it silently.
- Under `Off` the batch still works, one statement per message, so switching
  the option never needs a code change.
- Reads are not batched (deferred).

In PHP:

```php
$batch = $db->batch();
$batch->insert($order);
$batch->updateOnly($user, ['displayName']);
$batch->execute(Commands::markShipped(), new MarkShippedArgs(7));
$batch->delete(Role::class, 3);
$result = $batch->send();          // $batch->dispose() drops an unsent batch
```

In Go, the queue operations are package functions (generic operations cannot be
methods), and `Discard` is the analog of disposal:

```go
b := db.Batch()
_ = orm.BatchInsert(b, &order)
_ = orm.BatchUpdateOnly(b, &user, "DisplayName")
_ = orm.BatchExecute(b, MarkShipped, MarkShippedArgs{ID: 7})
_ = orm.BatchDelete[Role](b, int64(3))
result, err := b.Send(ctx)         // result.Affected, result.Messages
```

## Cascades are declared

`[Cascade(CascadeOperations.Persist | CascadeOperations.Remove)]` (or `All`)
on a many-to-one, one-to-one, or one-to-many navigation; anywhere else is
`MAP-025`, and a many-to-many cannot cascade because its link rows are
entities you persist explicitly.

```csharp
[OneToMany(nameof(TransactionDetail.TransactionId))]
[Cascade(CascadeOperations.All)]
public IReadOnlyList<TransactionDetail> Details { get; private set; } = [];
```

- **Persist** cascades to the navigation's targets (a single reference, or
  every member of an initialized collection) and records the key each child
  takes from its owner at flush. It never loads: a never-loaded collection
  cascades nothing.
- **Remove** cascades to the targets. A loaded collection removes its members;
  a never-loaded collection is `UOW-005`: nothing loads implicitly, so load it
  or remove the children yourself. A null singular navigation cascades nothing.

In PHP: `#[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]`.
In Go: `orm:"one_to_many=TransactionID,cascade=persist+remove"`.

## Detach and merge

```csharp
tracked.Detach(order);                                // no longer the session's instance; scheduled work cancelled
var managed = await tracked.MergeAsync(detachedCopy, ct);   // the tracked instance (or the row read now) takes the copy's
                                                      // values, version included; a stale copy still fails CRUD-010
```

- `Detach(entity)` makes the session forget the instance and cancels what was
  scheduled for it. The instance keeps its values. An instance the session
  neither tracks nor has scheduled is `UOW-001`.
- `MergeAsync(detached, ct)` brings a detached copy's state back and returns
  the session's instance for its key: the tracked one, or the row read now
  (`CRUD-001` when there is none). Every mapped non-key value is copied, the
  version included; navigations are not merged; the copy stays untracked. A
  copy without a complete key is `CRUD-002` (new instances are persisted, not
  merged).

In PHP: `$db->detach($entity)`, `$managed = $db->merge($detached)`. In Go:
`orm.Detach(db, &e)`, `managed, err := orm.Merge(ctx, db, &copy)`.

## When not to use tracking

A session is a unit of work, not a process-wide cache. The map holds strong
references for the session's lifetime, and a flush sweeps every tracked
instance (about 100 microseconds per 1000 instances; linear, as in every
snapshot-tracking ORM). A long-lived tracked session reading many rows is a
long-lived cache: dispose it, or `Detach` what you no longer need. Tracking
costs an identity lookup per row read and a snapshot per row registered (a
1000-row warm read runs at 1.09 times the untracked cost); for bulk reads and
reports, open an untracked session. Disposing a session with scheduled work
drops it silently: nothing is ever flushed implicitly.

## The `UOW-` codes

| Code | Meaning | Fix |
|---|---|---|
| `UOW-001` | A tracking question, `Remove`, or `Detach` on an instance the session does not track; any tracking question on an untracked session | Read the instance through this session first, or open with tracking |
| `UOW-002` | The instance is not the session's instance for its key: another holds it, or the key was changed | Write through the tracked instance; never change a key (delete and insert instead) |
| `UOW-003` | `Persist`/`Remove`/`Flush`/`Detach`/`Merge` on an untracked session | Open with `TrackingMode.Tracked` |
| `UOW-004` | The scheduled instances reference each other in a cycle | Break the cycle: insert one side, then set the other's FK and update |
| `UOW-005` | A remove cascades over a never-loaded collection | Load it first, or remove the children explicitly |
| `UOW-006` | `Persist` of an instance scheduled for removal | Decide: cancel the removal by detaching, or do not persist |

## Pitfalls

- `UOW-003` is checked first, before the argument: an untracked session refuses
  every unit-of-work call.
- A clean tracked instance's `UpdateAsync` issues no statement and does not
  bump the version. If you expected a touch, set a column.
- `[UpdatedAt]` never makes an instance dirty on its own, and key, version, and
  generated differences are clean (`UOW-002` for a changed key).
- `ChangesOf` on an untracked session is `UOW-001`, not an empty list.
- A re-read of a tracked key still maps the row, so conversion errors (`MAP-031`,
  `VAL-020`) still fire; only the values are not applied.
- Setting a navigation in user code does not set the FK outside a flush: an
  immediate `UpdateAsync` with a disagreeing navigation is `CRUD-004`.
- Under `Batching = Auto`, a flush that fails inside your transaction may have
  run statements after the failing one: roll the transaction back rather than
  committing what is left.

# CRUD and concurrency

Insert, update, delete, and read-by-key are generated from the `EntityMap`.
Every generated statement lists its columns explicitly and binds every value as
a parameter. Only table-backed entities are writable; a write against a view,
statement, or procedure refuses with `CRUD-003`. The exact rules are in
[`spec/crud.md`](../../spec/crud.md).

## Insert

`InsertAsync` writes every mapped non-generated column. It returns nothing; the
generated key lands on the entity.

```csharp
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
await db.InsertAsync(user, ct);        // user.Id is set
```

Key strategies, decided by the metadata:

| Strategy | Declaration | On insert |
|---|---|---|
| database-generated | single `[Key] [Generated]` integer | `RETURNING` (SQLite, PostgreSQL) or `scope_identity()` (SQL Server); the key is written back |
| client GUID | single `[Key]` `Guid` | an empty GUID gets a new one; a caller-set GUID is kept |
| natural or composite | one or more `[Key]` columns | values come from the entity as is |

In PHP: `$db->insert($user)`. In Go: `orm.Insert(ctx, db, &user)` (a pointer, so
the key can be written back).

## Read by key

`GetAsync` throws `CRUD-001` when the row is missing; `GetOrDefaultAsync`
returns `null`. Composite keys pass a tuple in key order. The key is validated
against the `EntityMap` at call time: a wrong arity, order, or type is
`CRUD-002` naming the part. Safe integer widening is allowed
(`GetAsync<User>(7)` works against a `long` key).

```csharp
var user  = await db.GetAsync<User>(7, ct);                        // CRUD-001 if missing
var maybe = await db.GetOrDefaultAsync<User>(7, ct);               // null if missing
var link  = await db.GetAsync<UserRole>((userId, roleId), ct);     // composite key tuple
var all   = await db.QueryAllAsync<User>(ct);                      // every row, ordered by key
```

Key reads work on tables and keyed views; a statement or procedure refuses
with `QRY-005`.

In PHP the key parts are a list: `$db->get(User::class, 7)`,
`$db->get(UserRole::class, [$ada->id, $admin->id])`, `$db->getOrDefault(User::class, 999)`,
`$db->queryAll(Role::class)`. In Go: `orm.Get[User](ctx, db, 7)`,
`orm.Get[UserRole](ctx, db, []any{userID, roleID})`, `orm.GetOrDefault[User]`
(returns `*User`, nil when absent), `orm.QueryAll[User]`.

## Update: the full row

`UpdateAsync` writes every mapped non-key, non-version, non-generated column by
key. Without a version column, zero rows affected is `CRUD-001`: silently
updating nothing is a bug, not a no-op.

```csharp
user.Name = "Ada Lovelace";
await db.UpdateAsync(user, ct);
```

(In a tracked session the same call writes only the dirty columns; see
[Tracking](07-tracking-and-unit-of-work.md).)

## Update by column list

`UpdateOnlyAsync` is a separately named operation that writes only the listed
properties, with the same key and version rules. The list holds property
names, not column names. Validation happens before anything is written:

- a name that is not a mapped property is `CRUD-005`;
- a key, version, or database-generated property is `CRUD-006`;
- an empty list or a repeated name is `CRUD-007`.

```csharp
await db.UpdateOnlyAsync(user, [nameof(User.Name)], ct);
await db.UpdateOnlyAsync(profile, ["Address"], ct);     // an owned navigation stands for all its members
```

Concurrency stays row-level: two callers updating disjoint columns of the same
versioned row still conflict. In PHP: `$db->updateOnly($entity, ['name'])`
(the port's own property spelling). In Go: `orm.UpdateOnly(ctx, db, &order, "Status")`.

## Delete: by key or by entity

One operation, dispatched on the argument. A key deletes by key (validated
like a key read, `CRUD-001` on zero rows). The entity gives the version-checked
form when a version column is mapped.

```csharp
await db.DeleteAsync<User>(user.Id, ct);       // by key
await db.DeleteAsync<Transaction>(tx, ct);     // by entity: WHERE id = @id AND version = @version
```

In PHP the entity type comes first: `$db->delete(TransactionDetail::class, $detail->id)`
and `$db->delete(Transaction::class, $transaction)`. Go names the two forms:
`orm.Delete[Order](ctx, db, 7)` and `orm.DeleteEntity(ctx, db, &order)`.

On an entity with a `[SoftDelete]` flag, every delete becomes the flag-setting
update; see [Hooks](08-hooks.md).

## Optimistic concurrency

Map an integer column with `[Version]` and every update and entity-form delete
becomes version-checked. The SET renders `version = version + 1` (the database
computes it); the WHERE adds `version = @current` bound from the entity; zero
rows affected throws `ConcurrencyException` (`CRUD-010`), its own exception
type so callers can catch the reload-and-retry case specifically. On success
the entity's in-memory version is incremented, so sequential updates on the
same instance keep working.

```csharp
public sealed class TransactionRepository(Db db) : Repository<Transaction>(db)
{
    public async Task SetStatusAsync(long id, TransactionStatus status, DateTime nowUtc, CancellationToken ct)
    {
        var transaction = await GetAsync(id, ct);
        transaction.Status = status;
        transaction.UpdatedAtUtc = nowUtc;
        await UpdateAsync(transaction, ct);       // CRUD-010 if another writer got there first
    }
}
```

A registry command that updates a versioned row by hand must bump the version
itself (`version = version + 1`), as the PHP sample's `cancelStaleTransactions`
does; generated writes never trust a hand-written count.

## Navigation and foreign key consistency

The FK property is what is written; a navigation is never written. Before an
insert or update, every non-null many-to-one navigation must agree with its
declared FK properties, compared pairwise in key order. A disagreement throws
`CRUD-004` naming the navigation and the FK property instead of writing. The
column-list update checks only the navigations of the listed properties.

## The SQL Server affected-row rule

On SQL Server a trigger's rows inflate the driver's affected-row count, which
would let a stale write pass the version check as a success. The SQL Server
dialect therefore reports the count itself (`ReportsAffectedRowsBySelect`): the
generated UPDATE and DELETE renderings become
`set nocount on; <statement>; select @@rowcount; set nocount off`, and the
session judges that scalar. Registry commands keep the driver's count. Nothing
changes in your code; `CRUD-010` and `CRUD-001` mean the same thing on every
dialect. See [Dialects](12-dialects.md).

## Generated DDL for dev and test

`CreateTableAsync<T>`, `CreateViewAsync<T>`, and `CreateTriggersAsync<T>`
render an object from its metadata (`IF NOT EXISTS` or replace semantics) for
tests and throwaway databases. Versioned [migrations](09-migrations.md) remain
the schema-evolution path. The wrong source kind is `DDL-001`; a materialized
view on a dialect without them is `DDL-002`.

```csharp
await db.CreateTableAsync<User>(ct);
await db.CreateViewAsync<UserTransactionTotal>(ct);
await db.CreateTriggersAsync<Role>(ct);
```

## The `CRUD-` codes in plain words

| Code | What happened | What to do |
|---|---|---|
| `CRUD-001` | No row for the key: `GetAsync`, or an unversioned update or delete affected zero rows | Use `GetOrDefaultAsync` when absence is normal; otherwise the row is gone |
| `CRUD-002` | The key shape is wrong (arity, order, type), or the entity defines no key | Pass the tuple in key order with the declared types |
| `CRUD-003` | A write against a view, statement, or procedure | Write to the table-backed entity |
| `CRUD-004` | A `[ManyToOne]` navigation's key disagrees with the FK property | Set the FK property; navigations are never written |
| `CRUD-005` | `UpdateOnlyAsync` lists a name that is not a mapped property | Use property names, not column names |
| `CRUD-006` | `UpdateOnlyAsync` lists a key, version, or generated property | Remove it; updates never write those |
| `CRUD-007` | `UpdateOnlyAsync` list is empty or repeats a name | Fix the list |
| `CRUD-010` | Optimistic concurrency conflict: the row's version changed | Reload, reapply the change, retry |
| `CRUD-012` | A by-key soft delete on a versioned entity | Pass the entity (the version-checked form) |

## Pitfalls

- `CRUD-001` on an update means the row was deleted under you; on a delete it
  means a double delete. Neither is silent.
- `CRUD-010` is a `ConcurrencyException`, not a `SimpleOrmException`; catch it
  by type.
- `CRUD-004` when you set a navigation in user code and forgot the FK. Set the
  FK property; the unit of work is the only place a navigation drives the FK.
- `CRUD-006` when a column list names `Version` or the generated `Id`.
- `CRUD-003` when a `[View]` entity is passed to `InsertAsync`. Views are
  read-only by definition.
- `VAL-020` on insert when a `DateTime` has `Kind == Unspecified`. The sample's
  tests use `new DateTime(2026, 8, 28, 9, 30, 0, DateTimeKind.Utc)`.

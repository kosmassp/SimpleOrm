# Queries and commands

Hand-written SQL is first-class. You declare it once in a registry, next to its
argument type and result type, and the session executes it through the same
mapping pipeline that generated reads use. The exact rules are in
[`spec/session.md`](../../spec/session.md) and
[`spec/mapping-rules.md`](../../spec/mapping-rules.md).

## The registry

A registry is a static class of `Query<TArgs, TResult>` and `Command<TArgs>`
fields. `Query.Inline(...)` carries the SQL at the declaration site;
`Query.Embedded("path.sql")` reads an embedded resource under `Sql/` at first
use (`QRY-003` if missing). Both forms validate identically. The samples use
inline only.

```csharp
public static class Queries
{
    public static readonly Query<EmptyArgs, User> AllUsers = Query.Inline(
        "select id, name, email, display_name, created_at, updated_at from users order by id");

    public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    public static readonly Query<EmailArgs, User> UserByEmailEmbedded =
        Query.Embedded("Users/GetUserByEmail.sql");
}

public sealed record EmailArgs(string Email);
```

A command is the same declaration with no result type:

```csharp
Command<EmptyArgs> clear = Query.Inline("delete from users");
await db.ExecuteAsync(clear, EmptyArgs.Value, ct);
```

`EmptyArgs.Value` is the canonical args value for SQL with no placeholders.

In PHP a registry is a class of public static methods, since PHP cannot
construct objects in static initializers; SchemaGuard enumerates the public
static methods returning `Query` or `Command`:

```php
final class Queries
{
    public static function roleUsage(): Query
    {
        return Query::inline(EmptyArgs::class, RoleUsage::class, <<<'SQL'
            -- notnull: user_count
            select r.role_name,
                   count(ur.user_id) as user_count
            from roles r
            left join user_roles ur on ur.role_id = r.id
            group by r.id, r.role_name
            order by r.role_name
            SQL);
    }
}

final class Commands
{
    public static function cancelStaleTransactions(): Command
    {
        return Command::inline(CancelStaleArgs::class, <<<'SQL'
            update transactions
            set status = 'Cancelled',
                updated_at = @nowUtc,
                version = version + 1
            where status = 'Pending'
              and created_at < @before
            SQL);
    }
}
```

In Go an entry is a package-level value; the entry types are
`orm.QueryEntry[A, R]` and `orm.CommandEntry[A]`, and an embedded form reads an
`embed.FS`:

```go
type TransactionsByUserArgs struct{ UserID int64 }

var TransactionsByUser = orm.Inline[TransactionsByUserArgs, Transaction](
    "select id, user_id, status, amount, version, note, created_at, updated_at " +
        "from transactions where user_id = @UserID order by id")

type MarkTransactionStatusArgs struct {
    ID        int64
    Status    TransactionStatus
    UpdatedAt time.Time
}

var MarkTransactionStatus = orm.InlineCommand[MarkTransactionStatusArgs](
    "update transactions set status = @Status, updated_at = @UpdatedAt where id = @ID")
```

## Parameters

`@Name` placeholders bind from the public readable properties of the args
value, matched case-insensitively. Placeholders inside string literals and SQL
comments are not placeholders. Both directions are strict on every execution:

- a placeholder with no matching property is `PRM-001`;
- a property no placeholder uses is `PRM-002`.

Values cross the boundary through the conversion table and type handlers, so a
`DateTime` argument must be UTC (`VAL-020`) and an enum binds by name.

## IN lists

A collection-typed property (strings and byte arrays are values, not
collections) expands its placeholder into one generated placeholder per
element, always parameterized. An empty collection matches no rows and never
errors.

```csharp
private sealed record IdsArgs(IReadOnlyList<long> Ids);

Query<IdsArgs, long> count = Query.Inline("select count(id) from users where id in (@Ids)");
var picked = await db.QuerySingleAsync(count, new IdsArgs([all[0].Id, all[2].Id]), ct);   // IN (@Ids_0, @Ids_1)
Assert.Equal(0L, await db.QuerySingleAsync(count, new IdsArgs([]), ct));                // IN (NULL): no rows
```

On PostgreSQL the dialect declares `SupportsArrayParameters`, so the SQL is
written natively and the collection binds as one typed array parameter:

```sql
select id, name, email, display_name, created_at, updated_at from users where id = any(@Ids)
```

The observable contract is the same on every dialect: always parameterized,
empty matches no rows. Registry SQL is dialect-specific by nature; keep one
registry per dialect you target, or use criteria queries, which render per
dialect.

## Executing

| Method | Result | Codes |
|---|---|---|
| `QueryAsync(q, args, ct)` | every row, materialized, in result order | |
| `QuerySingleAsync(q, args, ct)` | exactly one row | `QRY-001` zero rows, `QRY-002` more than one |
| `QuerySingleOrDefaultAsync(q, args, ct)` | one row or `null` (a value type's default) | `QRY-002` more than one |
| `StreamAsync(q, args, ct)` | `IAsyncEnumerable<T>`, nothing buffered | |
| `ExecuteAsync(cmd, args, ct)` | the affected-row count | |

```csharp
var users = await db.QueryAsync(Queries.AllUsers, EmptyArgs.Value, ct);
var count = await db.QuerySingleAsync(Queries.CountUsers, EmptyArgs.Value, ct);

await foreach (var row in db.StreamAsync(Queries.AllUsers, EmptyArgs.Value, ct)) { ... }

Command<EmptyArgs> clear = Query.Inline("delete from user_roles");

await using (var tx = await db.BeginAsync(ct))
{
    await db.ExecuteAsync(clear, EmptyArgs.Value, ct);
    await tx.CommitAsync(ct);          // disposing without commit rolls back
}
```

Every operation runs on the session's one connection and inside its current
transaction, if any. `BeginAsync` while a transaction is active is `TX-001`.

In PHP the methods are synchronous and `stream()` returns a `Generator`:

```php
$usage = $db->query(Queries::roleUsage(), EmptyArgs::value());
foreach ($db->stream(Queries::roleUsage(), EmptyArgs::value()) as $row) { ... }

$scope = $db->begin();
$affected = $db->execute(Commands::cancelStaleTransactions(), new CancelStaleArgs($before, $now));
$scope->commit();      // or $scope->rollback()
```

In Go, execution functions take the context, the session, the entry, and the
args; `Stream` is an `iter.Seq2`:

```go
users, err := orm.Query(ctx, db, UsersByEmail, UsersByEmailArgs{Email: "ada@example.com"})
user, err := orm.QuerySingle(ctx, db, UsersByEmail, UsersByEmailArgs{Email: "ada@example.com"})
maybe, err := orm.QuerySingleOrDefault(ctx, db, UsersByEmail, args)   // *User, nil when absent
for row, err := range orm.Stream(ctx, db, AllUsers, orm.EmptyArgs{}) { ... }

tx, err := db.Begin(ctx)
defer tx.Rollback(ctx)
n, err := orm.Execute(ctx, db, MarkTransactionStatus, MarkTransactionStatusArgs{ID: 7, Status: sample.Completed, UpdatedAt: now})
err = tx.Commit(ctx)
```

## Scalars, DTOs, and entities

The result type decides how rows map:

- A **scalar** (`long`, `string`, `decimal`, ...) reads the first column.
- A **DTO** (a record or class with no mapping metadata) matches columns to
  constructor parameters and settable properties case- and
  underscore-insensitively (`created_at` matches `CreatedAt`). The constructor
  with the most matching parameters wins; a tie is `MAP-003`. `required`
  members that end up unbound are `MAP-002`.
- An **entity** (a type with mapping metadata) must match its mapped columns
  exactly, both ways. `[Column]` overrides apply to hand-written SQL too, so
  `select ... created_at ...` fills `CreatedAtUtc` on the sample's `BaseModel`.

```csharp
public sealed record UserEmailRow(long Id, string Email);

Query<EmptyArgs, UserEmailRow> emailRows = Query.Inline("select id, email from users order by id");
var rows = await db.QueryAsync(emailRows, EmptyArgs.Value, ct);
```

A partial selection of an entity is a DTO, not a half-filled entity.
Strictness applies before the first row, so an empty result with the wrong
shape still fails.

## Nested results with JSON

The database builds the children as one JSON column, and a registered JSON
handler deserializes it. One round trip, no in-memory reshaping. Register the
handler on the options first (see
[Mapping entities](02-mapping-entities.md#json-columns-and-type-handlers)).

```csharp
public sealed record DetailLine(string Description, int Quantity, decimal UnitPrice);
public sealed record TransactionWithDetails(long Id, decimal Amount, List<DetailLine> Details);

private static readonly Query<TransactionsByUserArgs, TransactionWithDetails> WithDetails = Query.Inline(
    """
    select t.id,
           t.amount,
           (select json_group_array(json_object(
                    'description', d.description,
                    'quantity', d.quantity,
                    'unit_price', d.unit_price))
            from transaction_details d
            where d.transaction_id = t.id) as details
    from transactions t
    where t.user_id = @UserId
    order by t.id
    """);

public sealed record TransactionsByUserArgs(long UserId);
```

Zero children yield an empty list, never null. The JSON handler reads
snake_case keys and numbers from strings, so the SQL needs no casting. For
graphs of entities, [relationship loading](06-relationships-and-loading.md) is
the alternative; JSON nesting stays the single-round-trip option for arbitrary
shapes.

## Statement entities

For a read model that is always produced by one query, a `[Statement]` entity
is the query: no registry entry, executed by type, args checked against the
declared parameter contract. See
[Mapping entities](02-mapping-entities.md#views-statements-and-procedures).

```csharp
var days = await db.QueryAsync<DailySales>(new DailySalesArgs(since), ct);
```

## Repositories

`Repository<TEntity>` wraps one injected session with the generic surface
(insert, update, delete, get, get-all, criteria find and query) and adds
nothing of its own. Subclass it for entity-specific reads:

```csharp
public sealed class UserRepository(Db db) : Repository<User>(db)
{
    public Task<User> GetByEmailAsync(string email, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(User.Email), email)).SingleAsync(ct);

    public Task<IReadOnlyList<User>> GetByIdsAsync(IReadOnlyList<long> ids, CancellationToken ct)
        => Query().Where(Criteria.In(nameof(User.Id), ids)).OrderBy(nameof(User.Id)).ToListAsync(ct);
}
```

In Go, embed `orm.Repository[T]` (`orm.NewRepository[T](db)`) in your own type.
The PHP port has no library repository; the PHP sample carries one in
`src/Repositories/Repository.php`.

## Pitfalls

- `PRM-001` / `PRM-002`: placeholder and args property mismatch. Rename one
  side; both directions are checked.
- `MAP-001` / `MAP-002`: an entity result with an extra or missing column.
  List exactly the mapped columns, or map the result to a DTO.
- `MAP-003`: two DTO constructors match the same number of columns. Keep one
  public constructor.
- `QRY-001` / `QRY-002`: wrong row count for a single-row method.
- `QRY-003`: `Query.Embedded` could not find the resource. Check the resource
  path and that the file is an embedded resource under `Sql/`.
- `QRY-004`: `QueryAsync<T>(args, ct)` on a type that is not statement-backed.
- `VAL-021`: `select *` in registered SQL. The guard refuses it; list columns.
- `VAL-020`: `current_timestamp` or `datetime('now')` in registered SQL, or a
  non-UTC `DateTime` argument.
- A collection property that is not a collection in the SQL: `IN (@Ids)` is the
  expansion site; `= @Ids` against a collection is a provider error on SQLite.

# Relationships and loading

Relationships are declared on the model and loaded on request. Nothing loads
implicitly: there are no lazy proxies, a navigation never fires SQL on access,
and reading an unloaded collection of a database-read entity throws rather
than returning an empty list. The rules are in
[`spec/loading.md`](../../spec/loading.md) and the declaration half in
[`spec/metadata-model.md`](../../spec/metadata-model.md#json-export).

## Declaring the four cardinalities

A navigation is transient: never a column, never written, and the library is
its only writer, so it has no public setter (`MAP-011`). Foreign keys are
declared as lists of property names, one per key part in key order, so
composite keys work the same way.

```csharp
[Table("transactions")]
public sealed class Transaction : BaseModel
{
    [Column]
    [ForeignKey(typeof(User))]
    public long UserId { get; set; }

    [ManyToOne(nameof(UserId))]                       // FK on this class
    public User? User { get; private set; }

    [OneToMany(nameof(TransactionDetail.TransactionId))]   // FK on the target
    [Cascade(CascadeOperations.All)]
    public IReadOnlyList<TransactionDetail> Details { get; private set; } = [];
}

[Table("users")]
public sealed class User : BaseModel
{
    [OneToMany(nameof(Transaction.UserId))]
    public IReadOnlyList<Transaction> Transactions { get; private set; } = [];

    [ManyToMany(typeof(UserRole))]                    // link declared, never inferred
    public IReadOnlyList<Role> Roles { get; private set; } = [];

    [OneToOne(nameof(UserProfile.UserId))]            // singular inverse; the unique index on
    public UserProfile? Profile { get; private set; } // the target FK is what makes it 1:1
}

[Table("user_roles")]
public sealed class UserRole : BaseModel
{
    [Key] [Column] [ForeignKey(typeof(User))] public long UserId { get; set; }
    [Key] [Column] [ForeignKey(typeof(Role))] public long RoleId { get; set; }

    [ManyToOne(nameof(UserId))] public User? User { get; private set; }
    [ManyToOne(nameof(RoleId))] public Role? Role { get; private set; }
}
```

- `[ManyToOne(fkProperties...)]`: the FK properties live on this entity
  (`MAP-016` when unknown or when the count disagrees with the target key's arity).
- `[OneToMany(targetFkProperties...)]` and `[OneToOne(targetFkProperties...)]`:
  the FK lives on the target (`MAP-021`). A one-to-many needs an
  `IEnumerable<T>` of an entity type; a one-to-one needs a single reference
  (`MAP-020`).
- `[ManyToMany(typeof(Link))]`: the link entity's `[ForeignKey]` declarations
  identify which link properties reference each side (`MAP-022`).
- A composite-key target lists every part: `[ManyToOne(nameof(UserId), nameof(RoleId))]`.
- `[Cascade]` is for the unit of work; see
  [Tracking and unit of work](07-tracking-and-unit-of-work.md).

Polymorphic relations and "through" traversals are ruled out permanently.

In PHP the target class is the attribute's first argument (arrays carry no
element type) and navigations are `public private(set)`:

```php
#[OneToMany(Transaction::class, 'userId')]
public private(set) array $transactions = [];

#[ManyToMany(Role::class, through: UserRole::class)]
public private(set) array $roles = [];

#[OneToOne('userId')]
public private(set) ?UserProfile $profile = null;

#[ManyToOne('userId')]
public private(set) ?User $user = null;
```

In Go the tag names the FK fields and the descriptor carries typed references:

```go
type User struct {
    Transactions []*Transaction `orm:"one_to_many=UserID"`
    Roles        []*Role        `orm:"many_to_many"`
    Profile      *UserProfile   `orm:"one_to_one=UserID"`
}

func (User) Entity() orm.EntityDef {
    return orm.EntityDef{
        Source:     orm.Table("users"),
        ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[UserRole]("Roles")},
    }
}
```

## Unloaded is not empty

An entity read from the database carries foreign keys proving related rows
may exist, so its collection navigations throw `REL-004` on any access until
loaded. A loaded-but-empty collection reads as empty. Entities you construct
yourself keep their initializers. Singular navigations stay `null` until
loaded; after loading, `null` means a null FK or a dead link (the FK points at
no row), which loads as `null` and never as an error.

In PHP the mapper `unset()`s the collection navigations of a database-read
entity; with the opt-in trait `SimpleOrm\Session\Navigations` on the class the
read throws `REL-004`, and without it PHP's own `Error` ("must not be accessed
before initialization"). In Go a slice read cannot be intercepted: a
never-loaded collection is a nil slice and a loaded-but-empty one is an empty
non-nil slice, so `REL-004` is unreachable there.

## Explicit and batch loading

Name the entity (or entities), the navigation by property name, and a token.
The batch form issues one query per navigation per call, never one per entity,
chunked only past 500 owners. Many-to-many is exactly two queries: link rows,
then targets.

```csharp
await db.LoadAsync(tx, nameof(Transaction.User), ct);          // one entity, one query
await db.LoadEachAsync(users, nameof(User.Transactions), ct);  // N entities, still one query
await db.LoadEachAsync(users, nameof(User.Roles), ct);         // many-to-many: two (link, targets)
```

- Collections come back ordered by target key, compared as values (`10` after `2`).
- Owners sharing a many-to-one target within one call share the same instance.
- A one-to-one that matches more than one row is `REL-002`: the unique index
  on the target FK is what makes a 1:1, and drift is refused rather than resolved.
- Loading overwrites the navigation with fresh state; entities the call did
  not name are untouched.

In PHP: `$db->load($transaction, 'user')` and `$db->loadEach($users, 'transactions')`.
In Go: `orm.Load(ctx, db, &order, "User")` and `orm.LoadEach(ctx, db, orders, "Details")`
over `[]*T`, so the loaded state lands on your instances.

## Eager loading with the query

`Include(navigations...)` on the criteria chain loads the named navigations
with the query; `Fetch(mode)` chooses how. Every mode loads an identical graph
and differs only in round trips and data shape.

```csharp
var page = await db.Query<User>()
    .Where(Criteria.Ge("CreatedAtUtc", since))
    .Include(nameof(User.Transactions), nameof(User.Profile))
    .Fetch(FetchMode.SubSelect)
    .OrderBy("Id").Limit(20)
    .ToListAsync(ct);
```

| Mode | Queries | Pick it when |
|---|---|---|
| `MultiQuery` (default) | root, then one batched key-list query per navigation | the owner set is small or the paging is simple; chunks past 500 owners |
| `SubSelect` | root, then one query per navigation filtering `in (select ... from the root query)` | the owner set is large on SQLite: no parameter list, no chunking; a paged root gains a key tiebreak so both evaluations pick the same rows |
| `Join` | one SELECT with LEFT JOINs | fewest round trips; to-one navigations only, or one collection without paging |

Join mode refuses what would be wrong rather than guessing: a collection
include with `Limit`/`Offset` is `REL-005` (the join multiplies root rows, and
in-memory paging is never acceptable), more than one collection include is
`REL-006` (never a silent Cartesian product), and a keyless root or target is
`REL-003`.

The README's [performance table](../../README.md#performance) measured 1000
owners with 3 children each on SQLite: SubSelect 3.8 ms, Join 5.0 ms with about
1.5× SubSelect's allocation, MultiQuery 6.7 ms (ADR-0048). On SQLite the
parameter-heavy `IN` list is what hurts, so SubSelect is the fastest mode for
large owner sets there. Join pays for its segment readers.

Includes are single-level; deeper graphs load explicitly from the loaded
entities. Non-included collections keep the `REL-004` guard.

In PHP: `$db->from(User::class)->include('transactions', 'profile')->fetch(FetchMode::SubSelect)->toList()`.
In Go: `orm.From[User](db).Include("Transactions", "Profile").Fetch(orm.FetchSubSelect).List(ctx)`
with `orm.FetchMultiQuery` and `orm.FetchJoin` beside it.

## JSON nesting as the alternative

For an arbitrary shape in one round trip, the `json_group_array` pattern on
[Queries and commands](03-queries-and-commands.md#nested-results-with-json)
remains the single-query option. Relationship loading gives you entities with
identity; JSON nesting gives you a DTO graph.

## Pitfalls

- `REL-004`: you read `user.Transactions` on an entity from the database
  without loading it. Include it or call `LoadAsync`/`LoadEachAsync`.
- `REL-001`: the navigation name is not a declared navigation (it is
  case-sensitive, like the declaration). The error lists what is declared.
- `REL-002`: a one-to-one matched more than one row. Add the unique index on
  the target FK; the database is what makes it 1:1.
- `REL-003`: a shape the loader could not validate at declaration, such as a
  target FK that is not a mapped column.
- `REL-005` / `REL-006`: join mode with a paged collection, or with two
  collections. Use `MultiQuery` or `SubSelect`.
- `MAP-011`: a navigation with a public setter. Make it `private set`.
- `MAP-016` / `MAP-021` / `MAP-022`: FK lists that do not match the key's
  arity, or name unknown properties. Composite keys need one entry per part.
- `CRUD-004`: you set a navigation and the FK disagrees. The FK is what is
  written outside the unit of work.

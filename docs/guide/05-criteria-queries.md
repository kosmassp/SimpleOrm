# Criteria queries

A criteria query is an abstract syntax tree built as data and rendered to SQL
by the dialect. Front-ends never emit SQL text; identifiers come from the
metadata and every value binds as a parameter. Criteria name properties, not
columns, and cover filtering, sorting, and paging of one entity. The contract is
[`spec/query-ast.md`](../../spec/query-ast.md).

## The chain

```csharp
var users = await db.Query<User>()
    .Where(Criteria.Or(Criteria.Eq("Id", 1), Criteria.In("Name", "Ada", "Grace")),
           Criteria.Ge("CreatedAtUtc", since))        // Where args are ANDed
    .OrderBy("Name").Limit(20)
    .ToListAsync(ct);

var user = await db.Query<User>()
    .Where(Criteria.Eq(nameof(User.Email), "obrien@example.com"))
    .SingleAsync(ct);                                  // QRY-001 zero rows, QRY-002 more than one
```

- `Where(params Criteria[])` takes several criteria at once; multiple
  arguments and repeated calls append to one implicitly ANDed list.
- `OrderBy(property, SortOrder.Asc | Desc)` appends an ordering.
- `Limit(n)` and `Offset(n)` page; a negative value is `QRY-008` (dialects
  disagree on what it means, so the arithmetic bug is refused).
- Terminals: `ToListAsync(ct)`, `SingleAsync(ct)`, `SingleOrDefaultAsync(ct)`.
  The single-row terminals carry `QRY-001`/`QRY-002` like the registry.
- `Include(...)` and `Fetch(...)` are eager loading; see
  [Relationships](06-relationships-and-loading.md).

Criteria queries need a named relation: tables and views. A statement or
procedure entity refuses with `QRY-005` before any rendering.

In PHP the chain starts with `from()` and the property names are the port's own
(`camelCase`, dotted for owned members):

```php
$page = $db->from(Transaction::class)->orderBy('createdAtUtc')->limit(2)->offset(1)->toList();
$admin = $db->from(Role::class)->where(Criteria::eq('name', 'admin'))->single();
```

In Go the chain starts with `orm.From[T](db)` and ends with `List`, `Single`,
or `SingleOrDefault`:

```go
recent, err := orm.From[Transaction](db).
    Where(orm.Or(orm.Eq("Status", "Pending"), orm.In("ID", ids...)), orm.Ge("CreatedAtUtc", since)).
    OrderBy("CreatedAtUtc", orm.Desc).Limit(20).
    List(ctx)
```

## The vocabulary

| C# | PHP | Go | Renders |
|---|---|---|---|
| `Criteria.Eq(p, v)` / `Ne(p, v)` | `Criteria::eq` / `ne` | `orm.Eq` / `orm.Ne` | `=` / `<>`; a null value renders `is [not] null` |
| `Gt`, `Ge`, `Lt`, `Le` | `gt`, `ge`, `lt`, `le` | `orm.Gt`, ... | `>`, `>=`, `<`, `<=`; null is `QRY-007` |
| `Like(p, pattern)` | `like` | `orm.Like` | `like`; you supply `%` and `_` |
| `In(p, values)` | `in(p, array)`, `inOne(p, string)` | `orm.In[T](p, values...)` | `in (...)`; empty renders `1 = 0` |
| `IsNull(p)` / `IsNotNull(p)` | `isNull` / `isNotNull` | `orm.IsNull` / `orm.IsNotNull` | `is null` / `is not null` |
| `And(...)` / `Or(...)` | `and` / `or` | `orm.And` / `orm.Or` | parenthesized composite; empty renders `1 = 1` / `1 = 0` |
| `Not(c)` | `not` | `orm.Not` | `not (...)` |

`Criteria.In` has three C# overloads: a typed `IEnumerable<T>`, `params object?[]`,
and a single `string` (so a lone string is one value, not a list of chars).
Values bind with the compared property's conversion, so an enum against an
`[EnumAsInt]` column binds as its number.

The kitchen sink from the conformance suite, rendered for SQLite:

```
select id, name, email, display_name, created_at, updated_at from users
where ((id = @c0 or name in (@c1, @c2)) and created_at >= @c3)
order by name desc, id limit @c4 offset @c5
```

Note the explicit column list in metadata order and the `@c0...` placeholders in
render order. On SQL Server the same tree brackets every identifier and renders
`offset ... rows fetch next ... rows only`; on PostgreSQL it double-quotes.

## Property resolution

Every property name resolves against the source entity at render time: an
exact-case match wins; otherwise a case-insensitive match applies, unless it
fits more than one property, which refuses (`QRY-006` names the candidates).
An unknown or unmapped property is `QRY-006` too; a name is never passed
through to the database as an identifier. Owned members use the dotted path:

```csharp
var inParis = await db.Query<UserProfile>().Where(Criteria.Eq("Address.City", "Paris")).ToListAsync(ct);
```

`nameof(User.Email)` is the C# idiom for spelling a property safely.

## Null semantics

A null in the criteria tree never silently invokes three-valued logic:

- `Eq(p, null)` renders `is null` and `Ne(p, null)` renders `is not null`,
  never `= NULL` (which matches nothing).
- An ordered comparison or `Like` with null is `QRY-007`; use `IsNull`/`IsNotNull`.
- A null element inside an `In` list is `QRY-007`; write `Or(In(...), IsNull(...))`.
- An empty `In` list matches no rows, visibly: it renders `1 = 0`.

NULL column data still follows standard SQL: `Ne("Note", "x")` excludes rows
where `note` is NULL, which is what `Or(Ne(...), IsNull(...))` is for.

## Dynamic composition

The tree is data, so optional filters from a UI compose by building the
predicate list. There is no string assembly and no conditional SQL fragment.

```csharp
var where = new List<Criteria>();
if (name is not null) where.Add(Criteria.Like("Name", name + "%"));
if (statuses.Count > 0) where.Add(Criteria.In("Status", statuses));

var rows = await db.Query<Transaction>().Where(where.ToArray())
    .OrderBy("CreatedAtUtc", SortOrder.Desc).Limit(20).ToListAsync(ct);
```

An empty list is a select-all. Sorting from a UI is `OrderBy(columnName, order)`
where the name is validated against the metadata (`QRY-006` for anything else),
so user input can never reach the SQL as an identifier.

## Joins and projection

At Level 2 the AST gained projection, LEFT JOINs, and subquery membership
(`in_select`). These serve eager loading: `Include(...).Fetch(FetchMode.Join)`
builds joins, `Fetch(FetchMode.SubSelect)` builds subquery membership. The
chain does not expose joins or projection directly, and filtering on related
data through criteria is not part of the API. `Criteria.InSelect(properties, subquery)`
exists for building a membership predicate from a `SelectAst` you construct
yourself; the usual way to express a cross-entity filter is a `[Statement]`
entity or a registry query in real SQL.

GROUP BY does not exist in the AST and is not planned: aggregations are written
as real SQL. Criteria stay a row-filter, sort, and page language, never a full
SQL replacement.

## Why there is no LINQ

A typed lambda front-end is approved as per-language sugar but is not part of
the portable contract, and a LINQ provider stays out. The reasons are
practical: LINQ providers silently accept expressions they cannot translate or
evaluate them client-side, and every port would need its own; the string-based
criteria chain is what the spec defines, renders identically on every dialect,
and is pinned by `conformance/ast/`. For anything criteria cannot express,
write the SQL.

## Performance

The renderer costs about 7% over the equivalent inline SQL on a 100-row page
(see the README's [performance table](../../README.md#performance)). Renderings
are pure and cheap; the cost that matters is the query plan, which is yours to
shape with indexes.

## Pitfalls

- `QRY-006`: a misspelled or unmapped property, or an ambiguous
  case-insensitive match. Use `nameof(...)`.
- `QRY-007`: `Gt`/`Ge`/`Lt`/`Le`/`Like` with null, or a null inside `In`. Use
  `IsNull`/`IsNotNull`.
- `QRY-008`: a negative limit or offset.
- `QRY-005`: `db.Query<DailySales>()` on a statement entity. Execute it by type.
- `QRY-001` / `QRY-002`: `SingleAsync` found zero or more than one row.
- An empty `In` list returns nothing, by design. Check the list before adding
  the criterion if "no filter" is what you mean.
- `Criteria.In("Name", "Ada")` is one value thanks to the string overload;
  `Criteria.In("Name", (object)someString)` goes through `params object?[]`
  and is also one value. Only an `IEnumerable<T>` is a list.

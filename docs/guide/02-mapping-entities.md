# Mapping entities

An entity is a class with mapping metadata. The loaders turn that metadata into
an `EntityMap`, and every other part of the library (mapping, CRUD, criteria,
validation, migrations, the export) reads the `EntityMap` and nothing else. Three
loaders exist, in precedence order: an explicit `EntityMapBuilder<T>`
registration, the attribute loader, and the convention loader. This page covers
the attribute loader, which is what the samples use. The exact rules are in
[`spec/metadata-model.md`](../../spec/metadata-model.md) and
[`spec/mapping-rules.md`](../../spec/mapping-rules.md).

## Opt-in columns

A property is mapped only when it carries `[Column]`. Bare `[Column]` derives
the column name from the property name through the naming convention;
`[Column("name")]` binds explicitly. A public settable property carrying none of
`[Column]`, `[Ignore]`, or a relationship attribute is a loader error (`MAP-010`).
Two properties mapping to the same column is `MAP-018`.

```csharp
[Table("transactions")]
[Index(nameof(UserId))]
[Index(nameof(Status), nameof(CreatedAtUtc), SortOrder.Desc, Name = "ix_transactions_status_created")]
public sealed class Transaction : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    [ForeignKey(typeof(User))]
    public long UserId { get; set; }

    [Column]
    public TransactionStatus Status { get; set; }

    [Column]
    public decimal Amount { get; set; }

    [Version]
    [Column]
    public long Version { get; set; }

    [Column]
    public string? Note { get; set; }
}
```

- `[Table("transactions")]` names the relation. A class carries exactly one of
  `[Table]`, `[View]`, `[MaterializedView]`, `[Statement]`, `[Procedure]`
  (`MAP-012` otherwise).
- `[Key]` marks a key part; composite keys carry `[Key]` on each part in key
  order. A table with no key is `MAP-019`.
- `[Generated]` marks a database-generated key. It must be a single integer key
  (`MAP-019` for a composite or non-integer generated key).
- `[Version]` marks the optimistic-concurrency column (integer, tables only).
  See [CRUD and concurrency](04-crud-and-concurrency.md).
- `[Index(...)]` is class-level and repeatable: property names, an optional
  `SortOrder.Desc` after a property, `Unique = true`, `Name = "..."`. Indexes
  are consumed by generated DDL, snapshots, and `diff`, and matched
  structurally, never by name.
- `[ForeignKey(typeof(User))]` is declaration-only metadata that the
  relationship loader and the unit of work read; see
  [Relationships](06-relationships-and-loading.md).

In PHP the attributes have the same names; nullability comes from the typed
property, and `#[Index]` takes one array of tokens:

```php
#[Table('transactions')]
#[Index(['userId'])]
#[Index(['status', 'createdAtUtc', SortOrder::Desc], name: 'ix_transactions_status_created')]
final class Transaction extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(User::class)]
    public int $userId;

    #[Column]
    public TransactionStatus $status = TransactionStatus::Pending;

    #[Column]
    public Decimal $amount;

    #[Version]
    #[Column]
    public int $version = 0;
}
```

In Go the `orm` tag carries per-field facts and the descriptor the rest:

```go
type Transaction struct {
    BaseModel
    ID      int64             `orm:"column,key,generated"`
    UserID  int64             `orm:"column"`
    Status  TransactionStatus `orm:"column"`
    Amount  orm.Decimal       `orm:"column"`
    Version int64             `orm:"column,version"`
    Note    *string           `orm:"column"`
}

func (Transaction) Entity() orm.EntityDef {
    return orm.EntityDef{
        Source:      orm.Table("transactions"),
        ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID")},
        Indexes: []orm.IndexDef{
            orm.Index("UserID"),
            orm.Index("Status", "CreatedAtUtc", orm.Desc).Named("ix_transactions_status_created"),
        },
    }
}
```

## The naming convention

Derived names are snake_case: `UserId` becomes `user_id`, `APIKey` becomes
`api_key`, `CreatedAtUtc` becomes `created_at_utc`. That last one is why the
sample's `BaseModel` writes `[Column("created_at")]` explicitly: the property
name signals UTC, the column name does not. Derived table names are the
snake_case type name, never pluralized. A different convention plugs in through
`DbOptions.Mapping.NamingConvention` (`INamingConvention`). The normative
vectors are in [`spec/metadata-model.md`](../../spec/metadata-model.md#naming-convention).

## Nullability is strict

A nullable column must map to a nullable property, and a NULL read into a
non-nullable value type is `MAP-031`. [SchemaGuard](11-schema-guard.md) checks
the declared nullability of every table column against the property (`VAL-010`).
In C# that is the nullable reference and value types (`string?`, `long?`); in
PHP the `?` type; in Go a pointer field (`*string`, `*int64`, `*time.Time`, and
`*[]byte` for a nullable bytes column).

## The fixed type table

Conversion is a fixed table plus a handler registry; nothing is guessed. A type
with no rule is `MAP-030`; a value the rule cannot convert is `MAP-031`.

| C# | PHP | Go | SQLite storage |
|---|---|---|---|
| `int`, `long`, `short` | `int` (`ColumnType::Int32`/`Int16` to narrow) | `int64`, `int32`, `int16` | INTEGER |
| `decimal` | `SimpleOrm\Types\Decimal` | `orm.Decimal` | TEXT |
| `double`, `float` | `float` | `float64`, `float32` | REAL |
| `bool` | `bool` | `bool` | INTEGER 0/1 |
| `string` | `string` | `string` | TEXT |
| `Guid` | `string` + `ColumnType::Guid` | `orm.GUID` | TEXT |
| `byte[]` | `string` + `ColumnType::Bytes` | `[]byte` | BLOB |
| `DateTime` (UTC), `DateTimeOffset` | `DateTimeImmutable` (UTC) | `time.Time` | TEXT, ISO-8601 |
| `DateOnly`, `TimeOnly` (net10.0 only) | `DateTimeImmutable` + `ColumnType::Date`/`Time` | `time.Time` + `type=date`/`type=time` | TEXT, ISO-8601 |
| enum | pure enum | a type implementing `orm.Enum` | TEXT by name |

Enums store as TEXT by name and read case-insensitively; an unknown name is
`MAP-031`. `[EnumAsInt]` (PHP `#[EnumAsInt]`, Go `enum_int`) stores the integer
instead:

```csharp
[Column]
[EnumAsInt]
public TransactionStatus Status { get; set; }
```

On SQL Server and PostgreSQL the storage types differ (`nvarchar`,
`datetimeoffset`, `timestamp with time zone`, ...); see [Dialects](12-dialects.md).

## Dates are UTC

The convention is ISO-8601 UTC text with a trailing `Z` on SQLite, and a
marker-carrying type on the other dialects. Both directions are strict
(`VAL-020`):

- Writing: a `DateTime` with `Kind == Utc` stores as is; `Local` converts to UTC
  first; `Unspecified` is refused.
- Reading: a stored value must end with `Z` or an explicit `+hh:mm` offset.
  Anything else is refused rather than guessed. Values with an offset normalize
  to UTC, so a read `DateTime` always has `Kind == Utc`.

The sample's `BaseModel` shows the idiom: UTC-signaling property names, explicit
column names.

```csharp
public abstract class BaseModel
{
    [Column("created_at")]
    [CreatedAt]
    public DateTime CreatedAtUtc { get; set; }

    [Column("updated_at")]
    [UpdatedAt]
    public DateTime? UpdatedAtUtc { get; set; }
}
```

`[CreatedAt]` and `[UpdatedAt]` are hooks; see [Hooks](08-hooks.md). SchemaGuard
also lints registered SQL for `current_timestamp` and `datetime('now')`, which
store a datetime without a marker (`VAL-020`).

## JSON columns and type handlers

`ITypeHandler<T>` converts one type in both directions and always wins over the
fixed table. The built-in JSON handler deserializes a TEXT column holding JSON
into any registered type, with snake_case keys and numbers readable from
strings. Register handlers on `DbOptions.TypeHandlers` before opening:

```csharp
public sealed record DetailLine(string Description, int Quantity, decimal UnitPrice);

var options = new DbOptions { Dialect = new SqliteDialect() };
options.TypeHandlers.Json<List<DetailLine>>();
await using var db = await Db.OpenAsync(fixture.ConnectionString, options, CancellationToken.None);
```

A custom handler implements two methods: `T Parse(object databaseValue)` (never
receives NULL) and `object Format(T value)` (returns a type the provider stores
natively), registered with `options.TypeHandlers.Register(handler)`. The nested
result SQL pattern that pairs with the JSON handler is on
[Queries and commands](03-queries-and-commands.md#nested-results-with-json).

In PHP: `$handlers->register(new JsonTypeHandler(DetailLine::class, list: true))`
and `new DbOptions(new SqliteDialect(), typeHandlers: $handlers)`; the DTO's
`array` member needs a fully qualified `@param list<\App\DetailLine>` docblock.
In Go: `orm.RegisterJSON[[]DetailLine](registry)` and
`orm.RegisterHandler[T](registry, handler)`.

## Owned types

An owned type is a value object stored as columns of its owner's table. Mark
the class `[Owned]` and the navigation `[Owned]`; the members flatten into the
owner's columns under a prefix (the navigation's convention name plus `_`,
overridable with `[Owned(Prefix = "...")]`). The `EntityMap` stays flat: criteria,
column lists, and indexes use the dotted path `Address.City`.

```csharp
[Owned]
public sealed class Address
{
    [Column] public string Street { get; set; } = string.Empty;
    [Column] public string City { get; set; } = string.Empty;
    [Column] public string? PostalCode { get; set; }
}

[Table("user_profiles")]
public sealed class UserProfile : BaseModel
{
    [Key] [Generated] [Column] public long Id { get; set; }
    [Column] [ForeignKey(typeof(User))] public long UserId { get; set; }
    [Column] public string? Bio { get; set; }
    [Owned] public Address? Address { get; set; }     // address_street, address_city, address_postal_code
}

var inParis = await db.Query<UserProfile>().Where(Criteria.Eq("Address.City", "Paris")).ToListAsync(ct);
await db.UpdateOnlyAsync(profile, ["Address"], ct);  // the navigation name stands for all its members
```

A nullable navigation makes every member column nullable and an all-NULL row
reads back as `null`. An owned type may carry only `[Column]`, `[Ignore]`, and
`[EnumAsInt]` members: a key, version, relationship, or nested owned type on it
is `MAP-024`. In PHP the same attributes apply; in Go the type implements
`orm.OwnedType` with an empty method and the field carries `orm:"owned"`.

## Views, statements, and procedures

Every non-table source is self-contained: the defining SQL lives in the
attribute, so the class is the whole declaration.

A **view** is read-only and may declare `[Key]` so that `GetAsync` works, but
never `[Generated]` or `[Version]` (`MAP-013`) and never `[Index]` (`MAP-014`).
`db.CreateViewAsync<T>(ct)` renders it for dev and test databases.

```csharp
[View("user_transaction_totals", """
    select u.id              as user_id,
           u.name            as user_name,
           count(t.id)       as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at) as last_transaction_at
    from users u
    left join transactions t on t.user_id = u.id
    group by u.id, u.name
    """)]
public sealed class UserTransactionTotal
{
    [Key] [Column] public long UserId { get; set; }
    [Column] public required string UserName { get; set; }
    [Column] public int TransactionCount { get; set; }
    [Column] public decimal TotalAmount { get; set; }
    [Column("last_transaction_at")] public DateTime? LastTransactionAtUtc { get; set; }
}
```

A **statement** is a query that is its own result type: inline SQL plus a
declared parameter list as `(name, type)` pairs. It is keyless and read-only
(`[Key]` on it is `MAP-013`), and it executes by type, with the args checked
against the declaration by name (`PRM-010`/`PRM-011` at load) and type
(`PRM-012` at execution).

```csharp
[Statement("""
    -- notnull: sales_date, transaction_count, total_amount
    select date(created_at) as sales_date,
           count(id)        as transaction_count,
           sum(amount)      as total_amount
    from transactions
    where created_at >= @since
    group by date(created_at)
    order by sales_date desc
    """,
    "since", typeof(DateTime))]
public sealed class DailySales
{
    [Column] public DateOnly SalesDate { get; set; }
    [Column] public int TransactionCount { get; set; }
    [Column] public decimal TotalAmount { get; set; }
}

public sealed record DailySalesArgs(DateTime Since);

var days = await db.QueryAsync<DailySales>(new DailySalesArgs(since), ct);
```

The `-- notnull:` comment tells SchemaGuard that these expression columns are
never NULL, so non-nullable properties pass `VAL-010`. Because a statement has
no named relation, key reads and criteria queries on it refuse with `QRY-005`.

In PHP the parameter list is a map (`parameters: ['since' => ColumnType::DateTime]`)
and execution is `$db->statement(DailySales::class, new DailySalesArgs($since))`.
In Go the source is `orm.Statement(sql, orm.Param[time.Time]("since"))` in the
descriptor and execution is `orm.QueryStatement[DailySales](ctx, db, DailySalesArgs{Since: since})`
(with `QueryStatementSingle`, `QueryStatementSingleOrDefault`, and
`StreamStatement` beside it).

A **materialized view** (`[MaterializedView]`, may carry `[Index]`) and a
**procedure** (`[Procedure]`) are declaration-only on SQLite, which has neither;
they come alive on PostgreSQL (materialized views) and are otherwise dormant.

## The manual builder

For a type you cannot or will not annotate, register an `EntityMapBuilder<T>`
on the mapping options. An explicit registration wins over attributes and
conventions; only the configured properties are mapped.

```csharp
var builder = new EntityMapBuilder<Legacy>().ToTable("legacy_items");
builder.Property(x => x.Id).Column("legacy_id").Key().Generated();
builder.Property(x => x.DisplayName).Column("display_name");

var options = new MappingOptions().Register(builder);
var db = await Db.OpenAsync(cs, new DbOptions { Dialect = new SqliteDialect(), Mapping = options }, ct);
```

The property configuration also offers `Version()`, `CreatedAt()`,
`UpdatedAt()`, `SoftDelete()`, and `EnumAsInt()`, and the builder links
triggers with `Trigger<TTrigger>()`. The same validation rules apply as for
attributes.

A type with no mapping attributes at all goes through the convention loader:
every public settable property maps by convention and a property named `Id` is
the key.

## Base classes are never entities

A base class shares members only. Its `[Column]`, `[Key]`, `[Version]`, hook,
and rename declarations are inherited by every concrete subclass as if declared
there; identity comes from the concrete class alone. Declare base classes
`abstract`, as the sample's `BaseModel` is. Reading, writing, querying,
exporting, or registering through a base class throws `MAP-028`, and so does a
mapped class whose base carries `[Table]` (or any relation source, or `[Index]`).
Single-table and table-per-type inheritance are ruled out permanently.

Rows of several kinds in one table are modeled with what exists. The README's
["Variants"](../../README.md#variants-no-inheritance-mapping-adr-0041) section
shows both patterns in full:

- **Owned variants with a kind column**: one table, a `Kind` enum, one nullable
  `[Owned]` navigation per variant; the caller switches on `Kind`.
- **A write model plus read models**: one table-backed entity owns the writes;
  one keyed `[View]` or `[Statement]` per variant is its read-only projection.

## Exporting the metadata

`export-metadata` writes each entity's `EntityMap` as JSON, in the format of
[`spec/metadata-model.md`](../../spec/metadata-model.md#json-export). It is the
conformance artifact every implementation must reproduce, and a quick way to
see exactly what the loader understood.

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- export-metadata --assembly App.dll --out build/metadata
```

From code: `EntityMapJson.Export(map, loader)`.

## Pitfalls

- `MAP-010`: an unmarked public settable property. Add `[Column]`, `[Ignore]`,
  or a relationship attribute.
- `MAP-018`: two properties on one column, often an owned prefix colliding with
  an existing column.
- `MAP-019`: no key on a table, `[Generated]` on a composite or non-integer
  key, or `[Generated]`/`[Version]` without `[Column]`.
- `MAP-013` / `MAP-014`: `[Generated]`, `[Version]`, or `[Index]` on a view or
  statement, or `[Key]` on a statement.
- `MAP-024`: an owned type that carries a key, a relationship, or a nested
  owned type, or a navigation without a setter.
- `MAP-028`: a read through an abstract base class, or a subclass of a class
  that carries `[Table]`.
- `MAP-030` / `MAP-031`: a type with no conversion rule, or a value the rule
  refuses (unknown enum name, overflow, NULL into a non-nullable value type).
- `VAL-020`: `Kind == Unspecified` on write, a markerless value on read.

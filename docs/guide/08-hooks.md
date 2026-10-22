# Hooks

Hooks are declared metadata the session acts on, never registered callbacks. A
declaration on a column says what the library writes there and when. Three
exist, they apply to every write path (the immediate operations and the unit
of work alike), and they are exported in the metadata only when declared. The
rules are in [`spec/hooks.md`](../../spec/hooks.md).

## The three declarations

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

[Table("roles")]
public sealed class Role : BaseModel
{
    [Key] [Generated] [Column] public long Id { get; set; }

    [Column("role_name")]
    public required string Name { get; set; }

    [Column("is_deleted")]
    [SoftDelete]
    public bool IsDeleted { get; set; }
}
```

| Hook | Column type | What the session does |
|---|---|---|
| `[CreatedAt]` | `DateTime` or `DateTimeOffset`, nullable or not | On insert, fills the column with the clock when its value is unset (null or the type's default instant); keeps a value the caller set |
| `[UpdatedAt]` | `DateTime` or `DateTimeOffset`, nullable or not | Stamps the clock on every write that changes a row: a full-row update, a column-list update (the column joins the SET even when not listed), a tracked dirty update, a soft delete; never on insert; never on a clean update |
| `[SoftDelete]` | non-nullable `bool` | Every delete becomes the column-list update that sets the flag true, with the `[UpdatedAt]` stamp and the version rules of an update |

Each carries `[Column]`; none may be a key, version, or generated column; a
property carries at most one hook; at most one property per role per entity;
hooks are valid on tables only. Every violation is `MAP-026`.

In PHP: `#[CreatedAt]`, `#[UpdatedAt]`, `#[SoftDelete]` on `DateTimeImmutable`
and `bool` properties. In Go: the tag options `created_at`, `updated_at`,
`soft_delete` beside `column`:

```go
type BaseModel struct {
    CreatedAtUtc time.Time  `orm:"column=created_at,created_at"`
    UpdatedAtUtc *time.Time `orm:"column=updated_at,updated_at"`
}

type Role struct {
    BaseModel
    ID        int64 `orm:"column,key,generated"`
    Name      string `orm:"column=role_name"`
    IsDeleted bool  `orm:"column,soft_delete"`
}
```

## The session clock

`DbOptions.Clock` is the instant the hooks write: a function returning UTC
(`DateTimeKind.Utc`; any other kind is `VAL-020` at the write that would use
it), the wall clock by default. Fix it in tests for deterministic timestamps:

```csharp
var options = new DbOptions
{
    Dialect = new SqliteDialect(),
    Clock = () => new DateTime(2026, 8, 29, 0, 0, 0, DateTimeKind.Utc),
};
```

The clock is read once per statement that fills or stamps, so a multi-row
flush may stamp different instants unless the clock is fixed.

In PHP: `new DbOptions(new SqliteDialect(), clock: fn () => new DateTimeImmutable('2026-08-29T00:00:00Z'))`;
a `DateTimeImmutable` in a zone other than `UTC`, `Z`, or `+00:00` is `VAL-020`.
In Go: `orm.Options{Clock: func() time.Time { ... }}`; a zero-offset location
passes, any other offset is `VAL-020`.

## Soft delete in practice

On an entity declaring the flag, the delete calls you already know change
their rendering, not their shape:

```csharp
await db.DeleteAsync<Role>(role, ct);       // entity form: update roles set is_deleted = 1, updated_at = @now where id = @id
await db.DeleteAsync<Role>(role.Id, ct);    // by key: the same update by key
```

- The entity form (and a flushed `Remove`) is version-checked like any update
  (`CRUD-010`), and the instance is forgotten afterwards like any deleted row;
  its flag and stamp are set on it.
- The by-key form sets the flag by key. On a **versioned** entity it refuses
  (`CRUD-012`) rather than bump a version it does not know; pass the entity.
- A zero-row result is `CRUD-001`, as for a hard delete.
- A failed write leaves the instance as it was: the flag, the stamp, and an
  insert's `[CreatedAt]` fill go back to their previous values.

**Reads never filter on the flag.** A key read returns the flagged row, a
criteria query returns it, loading and the identity map see it like any row.
There is no hidden WHERE; write the criterion yourself:

```csharp
var live = await db.Query<Role>().Where(Criteria.Eq(nameof(Role.IsDeleted), false)).ToListAsync(ct);
```

In PHP: `Criteria::eq('isDeleted', false)`. In Go: `orm.Eq("IsDeleted", false)`.

## Hooks and tracking

`ChangesOf`/`IsDirty` report every mapped property whose database-bound value
differs from the snapshot, the `[UpdatedAt]` column included. The write
trigger ignores key, version, generated columns, and the `[UpdatedAt]` column:
an instance differing only there is clean and writes nothing. A changed
`[CreatedAt]` is an ordinary column and writes.

Hooks run after the gates: `CRUD-005`/`CRUD-004`/`UOW-002` outrank a
`VAL-020` from the clock, and `CRUD-012` is decided before the key validates.

## The fixture's migration

The soft-delete column was added by the sample's V0011 as a NOT NULL column
with a default, so existing rows read as live:

```csharp
public sealed class V0011_AddIsDeleted : TableMigration<global::SimpleOrm.Sample.Models.Role>
{
    public override void Action(TableActions actions)
    {
        actions.AddColumn("is_deleted", "INTEGER", nullable: false, defaultSql: "0");
    }
}
```

## Pitfalls

- `MAP-026`: a hook on a non-temporal property, `[SoftDelete]` on a nullable or
  non-`bool` property, a hook on a key, version, or generated column or without
  `[Column]`, two hooks on one property, two properties with the same role, a
  hook on a navigation or an owned member, or any hook on a view or statement.
- `CRUD-012`: `DeleteAsync<T>(key, ct)` on a soft-delete entity with a
  `[Version]` column. Pass the entity.
- `VAL-020`: a clock returning `DateTimeKind.Local` or `Unspecified`. The clock
  must read UTC outright; nothing converts there.
- A soft-deleted row still comes back from `GetAsync` and every query. Filter
  explicitly, everywhere it matters.
- `[CreatedAt]` keeps a value you set; if you want the clock, leave the
  property at its default.
- `[UpdatedAt]` is not stamped on insert; a nullable column stays null until
  the first update.

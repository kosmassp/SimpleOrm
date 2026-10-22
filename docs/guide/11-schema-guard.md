# Schema guard

`SchemaGuard.ValidateAsync` validates everything your assembly declares against
the real database the session is connected to. It runs once at startup and in a
test that calls the same code. It fails fast (no warn-only mode) and always
produces the complete report: every violation across every source, in one
`SchemaValidationException`, never first-error-only. Nothing validated is
executed with effect. The rules are in
[`spec/validation-rules.md`](../../spec/validation-rules.md).

## Calling it

```csharp
await SchemaGuard.ValidateAsync(db, typeof(Queries).Assembly, ct);
```

An overload takes the migrations namespace when migrations live in a
sub-namespace: `SchemaGuard.ValidateAsync(db, assembly, "App.Migrations", ct)`.
`SchemaGuard.ValidateTypesAsync(db, types, ct)` validates an explicit list of
types instead of scanning.

In PHP there is no assembly, so you pass the class list, the migration set, and
the snapshots:

```php
$classes = ClassScanner::classes($sourceDir, 'SimpleOrm\Sample');
$set = MigrationSet::fromDirectory($migrationsDir, 'SimpleOrm\Sample\Migrations');
$snapshots = SnapshotSet::fromDirectory($migrationsDir);
SchemaGuard::validate($db, $classes, $set, $snapshots);
```

In Go the registry is the application: `err = orm.Validate(ctx, db, app.Registry)`.

## What it checks

**Registry entries**: every public static `Query<TArgs, TResult>` and
`Command<TArgs>` field in the assembly's exported types (PHP: public static
methods returning `Query`/`Command`; Go: the registry's `Entries`). Each is
prepared, never executed, and checked:

| Code | Rule |
|---|---|
| `VAL-001` | the SQL fails to prepare |
| `PRM-001` / `PRM-002` | placeholders and args properties, both directions, statically |
| `MAP-001` / `MAP-002` / `MAP-003` | result columns and the result type, exactly, through the one mapping pipeline |
| `VAL-010` | a nullable column mapped to a non-nullable member |
| `VAL-011` | a column's declared type incompatible with the member type, and no handler registered |
| `VAL-020` | lint: `current_timestamp` or `datetime('now')` store a datetime without a UTC marker |
| `VAL-021` | lint: `select *` (`count(*)` is not a star selection) |

**Entities**: every exported concrete type carrying mapping metadata that is not
an owned type and that no other scanned type extends. Tables check existence
(`VAL-012`), columns (`VAL-013`), declared types (`VAL-011`), and nullability
(`VAL-010`), honoring `[EnumAsInt]` and registered handlers. Views check
existence and columns only. Statement entities validate their SQL like a query.
Sources the dialect cannot host (materialized views and procedures on SQLite)
are skipped. A concrete sourceless class that another scanned type extends is a
base class, not an entity, and is reported once as `MAP-028` naming the
extenders. A loader failure on a result type is a report row, never an
escaping exception.

**Migrations**: the assembly's versions against the database's recorded
history: pending versions are `MIG-030` (a pending `[Draft]` counts), drifted
checksums `MIG-010`, history unknown to the code `MIG-011`.

## Expression columns and the `-- notnull:` hint

A result column traced to a base table column is checked against that column's
declared type and nullability (a primary key counts as NOT NULL). An expression
column, such as `count(id)` or `coalesce(sum(amount), 0)`, has unknowable
nullability, so its member must be nullable unless the SQL carries the override
comment:

```sql
-- notnull: sales_date, transaction_count, total_amount
select date(created_at) as sales_date,
       count(id)        as transaction_count,
       sum(amount)      as total_amount
from transactions
where created_at >= @since
group by date(created_at)
```

The comment is case-insensitive, comma-separated, and repeatable, and it is
the first line of the sample's `DailySales` statement. The check inherits an
engine imprecision: a NOT NULL base column can still yield NULL through an
outer join, and the origin-based check trusts the base constraint. Declare the
member nullable when a `left join` can produce NULL.

## STRICT tables on SQLite

SQLite enforces declared types only on STRICT tables, so `VAL-011` is
meaningful there only when your tables are STRICT. Everything the library
generates is: `CreateTableAsync` emits `) STRICT`, and the sample's migrations
write it by hand:

```sql
create table if not exists users (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    email       TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT
) STRICT
```

Keep your own `create table` statements STRICT too, or the declared-type check
has nothing to hold on to.

## Reading the report

The exception carries one `ValidationError` per violation with `Code`,
`Source` (the registry field, entity, or migration), and `Message`; its
`ToString()` is `"{Code} {Source}: {Message}"`.

```csharp
try
{
    await SchemaGuard.ValidateAsync(db, typeof(User).Assembly, ct);
}
catch (SchemaValidationException exception)
{
    foreach (var error in exception.Errors)
    {
        Console.Error.WriteLine(error);       // e.g. "VAL-010 Queries.RoleUsage: column user_count ..."
    }
    throw;
}
```

In a test, assert on the codes rather than the messages; codes are the
contract, messages may change:

```csharp
var exception = await Assert.ThrowsAsync<SchemaValidationException>(
    () => SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None));
Assert.Contains(exception.Errors, e => e.Code == "MIG-030");
```

The CLI's `validate` command prints the same report and exits non-zero:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- validate --assembly App.dll --db app.db
```

## In a test

The sample's own tests run the guard against a migrated temp-file database so
that the registry and entities are proven against the schema the migrations
produce:

```csharp
await using var db = await TestDb.OpenAsync(fixture);
await SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None);   // no throw
```

See [Testing your app](14-testing-your-app.md) for the fixture.

## Pitfalls

- `VAL-010` on an aggregate: the member must be nullable or the SQL needs
  `-- notnull: col`.
- `VAL-011` on a non-STRICT SQLite table: the declared type is whatever the
  `create table` said; make the table STRICT and declare real types.
- `VAL-012` / `VAL-013`: the table or column does not exist. Usually a pending
  migration (`MIG-030` is reported beside it) or a `[Column("...")]` typo.
- `VAL-021`: `select *` in a registry entry. List the columns; entity results
  are exact anyway.
- `VAL-020`: `datetime('now')` in a command. Bind a UTC `DateTime` argument
  instead, or use a `[CreatedAt]`/`[UpdatedAt]` hook.
- `MIG-030`: the application never migrates itself. Run the CLI, then start.
- `MAP-028`: a concrete class without `[Table]` that another entity extends.
  Make the base class `abstract`.
- `PRM-002` on an args property the SQL never uses: remove the property or use
  it; the guard checks both directions statically, before any execution.

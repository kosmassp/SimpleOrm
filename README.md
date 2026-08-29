# SimpleOrm

A SQL-first micro-ORM for .NET, SQLite first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects
(PostgreSQL, MySQL, SQL Server).

**Status: Level 1, milestone 7 (CRUD + concurrency) done.** See
[CLAUDE.md](CLAUDE.md) for the full project brief and
[docs/decisions.md](docs/decisions.md) for the decision log.

## Usage

Reads are typed — key lookups, criteria queries (the AST as data, ADR-0012), and
`[Statement]` entities; the inline registry (`Query.Inline`/`Query.Embedded`,
ADR-0009/0010) remains the escape hatch for what those can't express:

```csharp
await using var db = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect() }, ct);

var user  = await db.GetAsync<User>(7, ct);                        // CRUD-001 if missing
var link  = await db.GetAsync<UserRole>((userId, roleId), ct);     // composite key tuple

var users = await db.Query<User>()                                 // criteria: no SQL, no per-table query
    .Where(Criteria.Or(Criteria.Eq("Id", 1), Criteria.In("Name", "Ada", "Grace")),
           Criteria.Ge("CreatedAtUtc", since))                     // Where args are ANDed
    .OrderBy("Name").Limit(20)
    .ToListAsync(ct);

await db.InsertAsync(user, ct);        // generated; key written back
user.Name = "Ada Lovelace";
await db.UpdateAsync(user, ct);        // full row by key
await db.DeleteAsync<User>(user.Id, ct);

// Optimistic concurrency ([Version] column): stale writes throw CRUD-010
tx.Amount = 12m;
await db.UpdateAsync(tx, ct);          // SET ... version = version + 1 WHERE id = @id AND version = @old
                                       // zero rows → ConcurrencyException; tx.Version bumped on success

await using (var tx = await db.BeginAsync(ct))
{
    await db.ExecuteAsync(Commands.InsertUser, new("Ada", "ada@example.com", now), ct);
    await tx.CommitAsync(ct);          // disposing without commit rolls back
}

// Preferred for custom reads (ADR-0010): a [Statement] entity IS its query —
// no registry entry, executed by type, args checked against the declared contract.
var days = await db.QueryAsync<DailySales>(new DailySalesArgs(since), ct);

// Generated from metadata (ADR-0011): schema (dev/test utility) and inserts —
// no handwritten DDL or insert commands. RETURNING writes the key back.
await db.CreateTableAsync<User>(ct);
await db.CreateViewAsync<UserTransactionTotal>(ct);
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
await db.InsertAsync(user, ct);        // user.Id is set
```

Rules that always hold: parameters bind from the args record's properties, both ways
strictly (`PRM-001`/`PRM-002`); `IN (@ids)` expands a collection property to
generated placeholders, always parameterized (an empty list matches no rows); dates
are ISO-8601 UTC `TEXT` — reading an unmarked datetime or writing
`Kind == Unspecified` fails with `VAL-020`; entity results map through their
`EntityMap`, so `[Column]` overrides apply to hand-written SQL too, and result
shape mismatches throw `MAP-001`/`MAP-002` (checked before the first row, even for
empty results). Conversion is the fixed table plus `ITypeHandler<T>` — nothing is
guessed (`MAP-030`/`MAP-031`).

### Nested results (JSON, §7.10)

The database builds the children; a registered JSON handler deserializes them:

```csharp
options.TypeHandlers.Json<List<DetailLine>>();   // snake_case keys, numbers from strings
```

```sql
select t.id, t.amount,
       (select json_group_array(json_object(
                'description', d.description,
                'quantity',    d.quantity,
                'unit_price',  d.unit_price))
        from transaction_details d
        where d.transaction_id = t.id) as details
from transactions t
```

Zero children yield an empty list, never null. Custom types register an
`ITypeHandler<T>` on `DbOptions.TypeHandlers` — the only path for types outside the
fixed conversion table.

### Migrations (versioned code, per object)

Root versions under `Migrations/` are the recorded units; per-object steps hold the
changes. Table actions always execute rename → add → remove; every action takes
optional `Pre`/`Post` data hooks:

```csharp
public sealed class V0002 : MigrationVersion
{
    public override void Compose(VersionBuilder v) => v.Apply<Table.User.V0002_AddDisplayName>();
}

public sealed class V0002_AddDisplayName : TableMigration<User>
{
    public override void Action(TableActions a)
    {
        a.RenameColumn("name", "full_name");
        a.AddColumn("display_name", "TEXT").Post("update users set display_name = full_name");
    }

    public override void Down(TableActions a) => a.Sql("…");
}
```

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- migrate --assembly App.dll --db app.db --namespace App.Migrations
```

Checksums (SHA-256 of rendered SQL) catch drift (`MIG-010`); on SQLite the whole
run is one `BEGIN IMMEDIATE` transaction — a failed run applies nothing. The app
never migrates at startup; `status`, `migrate down --to`, `baseline`, `validate`,
and `export-metadata` round out the CLI.

### Validation (SchemaGuard)

Once at startup, and in a test calling the same code:

```csharp
await SchemaGuard.ValidateAsync(db, typeof(Commands).Assembly, ct);
```

Every registered query/command is prepared (never executed) and checked against the
real schema — parameters both ways, result shape exactly, declared-type and
nullability per column (`VAL-010`/`011`), `SELECT *` and non-UTC-timestamp lints
(`VAL-020`/`021`) — entities are checked against their relations
(`VAL-012`/`013`), and migrations must be applied, matching, and known
(`MIG-030`/`010`/`011`). Expression columns require nullable members unless the
SQL carries `-- notnull: col`. One exception carries the complete report.

## Layout

```
spec/          language-neutral spec (grows per milestone)
conformance/   executable definition: JSON cases every implementation must pass
dotnet/        C# reference implementation
  src/SimpleOrm/           core (netstandard2.0 + net10.0, depends only on System.Data.Common)
  src/SimpleOrm.Sqlite/    SQLite dialect (Microsoft.Data.Sqlite)
  src/SimpleOrm.Cli/       CLI (stub until milestone 5)
  samples/SimpleOrm.Sample/ sample entity models used by tests and conformance
  tests/SimpleOrm.Tests/   integration tests against real SQLite databases
docs/decisions.md          ADR log
```

## Building and testing

Requires only the .NET 10 SDK — no database server, no Docker. Tests create a
temp-file SQLite database per fixture and delete it afterwards; the native SQLite
library ships with `Microsoft.Data.Sqlite`.

```
dotnet build dotnet/SimpleOrm.sln
dotnet test dotnet/SimpleOrm.sln
```

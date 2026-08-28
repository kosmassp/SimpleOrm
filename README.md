# SimpleOrm

A SQL-first micro-ORM for .NET, SQLite first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects
(PostgreSQL, MySQL, SQL Server).

**Status: Level 1, milestone 4 (strict mapping + types) done.** See
[CLAUDE.md](CLAUDE.md) for the full project brief and
[docs/decisions.md](docs/decisions.md) for the decision log.

## Usage

Declare queries once in a registry — SQL inline, next to its args and result types
(ADR-0009; `Query.Embedded("path.sql")` stays available for teams that prefer
`.sql` embedded resources) — then run them on a session:

```csharp
public static class Queries
{
    public static readonly Query<UserByEmailArgs, User> UserByEmail = Query.Inline(
        """
        select id, name, email, created_at, updated_at
        from users
        where email = @Email
        """);
}
public sealed record UserByEmailArgs(string Email);

await using var db = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect() }, ct);

var user  = await db.QuerySingleAsync(Queries.UserByEmail, new("ada@example.com"), ct);
var count = await db.QuerySingleAsync(Queries.CountUsers, EmptyArgs.Value, ct);
await foreach (var row in db.StreamAsync(Queries.AllUsers, EmptyArgs.Value, ct)) { ... }

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

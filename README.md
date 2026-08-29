# SimpleOrm

A SQL-first micro-ORM for .NET, SQLite first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects
(PostgreSQL, MySQL, SQL Server).

**Status: all eight Level 1 milestones complete.** See
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

// Or layer repositories over the session (ADR-0016) — the generic surface ships
// in the library; subclass to add entity-specific criteria reads:
public sealed class UserRepository(Db db) : Repository<User>(db)
{
    public Task<User> GetByEmailAsync(string email, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(User.Email), email)).SingleAsync(ct);
}

// Generated from metadata (ADR-0011): schema (dev/test utility) and inserts —
// no handwritten DDL or insert commands. RETURNING writes the key back.
await db.CreateTableAsync<User>(ct);
await db.CreateViewAsync<UserTransactionTotal>(ct);
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = now };
await db.InsertAsync(user, ct);        // user.Id is set
```

Relationships are **declared** on the model — all four classic cardinalities since
Level 2 milestone 1 (ADR-0005/0019) — and stay declaration-only until loading
lands (L2 M3). Nothing loads implicitly: a navigation is empty/null until
requested (explicitly, or eagerly with the query), never populated on access, and
never publicly settable (the library is a navigation's only writer). Polymorphic
and "through" relations are out permanently:

```csharp
[ManyToOne(nameof(UserId))]                 // FK on this class
public User? User { get; private set; }

[OneToOne(nameof(UserProfile.UserId))]      // singular inverse; the unique index on the
public UserProfile? Profile { get; private set; }   // target FK is what makes it 1:1

[OneToMany(nameof(Transaction.UserId))]     // FK on the target
public IReadOnlyList<Transaction> Transactions { get; private set; } = [];

[ManyToMany(typeof(UserRole))]              // link declared, never inferred;
public IReadOnlyList<Role> Roles { get; private set; } = [];   // resolved via its [ForeignKey]s

[ManyToOne(nameof(UserId), nameof(RoleId))] // composite-key target: FK list in key order
public UserRole? Grant { get; private set; }
```

Relationships load **explicitly** (ADR-0021) — nothing loads implicitly, and
reading an unloaded navigation never fires SQL:

```csharp
await db.LoadAsync(tx, nameof(Transaction.User), ct);          // one entity, one query
await db.LoadEachAsync(users, nameof(User.Transactions), ct);  // N entities, still one query
await db.LoadEachAsync(users, nameof(User.Roles), ct);         // many-to-many: two (link, targets)

var page = await db.Query<User>()                              // eager (ADR-0022): root query +
    .Include(nameof(User.Transactions), nameof(User.Profile))  // one batch load per navigation —
    .OrderBy("Id").Limit(20).ToListAsync(ct);                  // paging stays correct, no join fan-out
```

Criteria queries are an **AST rendered by the dialect** (ADR-0020) — front-ends
never emit SQL text — with strict null semantics: `Criteria.Eq(p, null)` renders
`is null` (never `= NULL`, which silently matches nothing), `Ne(p, null)` renders
`is not null`, and an ordered comparison with null or a null inside an IN list is
refused (`QRY-007`) instead of silently matching nothing.

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
}
```

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- migrate --assembly App.dll --db app.db --namespace App.Migrations
```

Checksums (SHA-256 of rendered SQL) catch drift (`MIG-010`); on SQLite the whole
run is one `BEGIN IMMEDIATE` transaction — a failed run applies nothing. The app
never migrates at startup; `status`, `migrate down --to`, `baseline`, `validate`,
and `export-metadata` round out the CLI.

**Nobody writes `Down()`** (ADR-0018): `migrate down` derives each rollback from
the versioned snapshots — typed renames invert data-preservingly, removed columns
come back, added ones drop, indexes revert, a view's previous definition is
restored. Embed the snapshots so rollbacks work deployed:

```xml
<EmbeddedResource Include="Migrations\**\*.schema.json" />
```

`Down()` stays available as a manual override, and `PreDown`/`PostDown` hooks
carry the data work the schema history can't know (a seed step deletes its rows
in `PreDown`, for example). No snapshot and no override refuses honestly
(`MIG-020`).

### Generating migrations (ADR-0017)

The model is the final truth; the committed `V000N.schema.json` snapshots are the
recorded past; `diff` turns the difference into the next migration — ordinary
source with literal SQL, no database needed. Tables diff by columns; views (and,
on capable dialects, materialized views) diff by their normalized DDL:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- diff --assembly App.dll --out App/Migrations --namespace App.Migrations --name AddNote
```

Renames are declared, never inferred (`--rename users.name=full_name`); removals
need `--allow-remove` (`DDL-003`); type/nullability changes and NOT NULL additions
are refused as `DDL-004` — write those by hand with
`AddColumn(name, type, nullable: false, defaultSql: …)`. The everyday loop:

1. change the model → `diff` → review the generated `V000N` → build → `migrate`
2. `snapshot --out App/Migrations` to record the new shape (commit it)

`shadow` rebuilds the snapshots from history instead — it replays every version
into a throwaway database and introspects after each one, which both regenerates
lost files and proves the snapshots match the migrations. When history is too long
to replay, `--from V000N` trusts version N (baseline restored from the committed
snapshots, nothing below N verified) and regenerates only `--to V000M`:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- shadow --assembly App.dll --out App/Migrations --from V0007 --to V0009
```

A generated view change step opens with `ExpectDefinition(<previous ddl>)`:
because views get patched directly in the database during urgencies, the step
refuses to apply over a definition that was changed outside the code (`MIG-012`) —
the whole run rolls back and the hotfix survives for review. `migrate --force`
recreates the view from the code and prints what drifted.

`migrate --force` also syncs any remaining live-schema gap to the model after
migrations run: additive fixes apply immediately, deletions only with
`--allow-delete` (`DDL-003`), and anything inexpressible is reported (`DDL-004`),
never guessed. Index comparison — in both `diff` and the sync — is structural
(unique flag + ordered columns and directions), never by name: an index the DBA
already added under another name counts as implemented and is left alone.

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

## Performance

Compiled expression-tree mappers with typed-getter fast paths. BenchmarkDotNet vs
Dapper and raw `Microsoft.Data.Sqlite` (net10.0, `dotnet/benchmarks/`): mapping
1000 rows to entities runs **~3% faster than Dapper with 22% less allocation**
(matching the raw reader's allocation floor); single-row reads are within 2.5%.
Numbers are machine-specific — run `dotnet run -c Release` in the benchmarks
project for yours.

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

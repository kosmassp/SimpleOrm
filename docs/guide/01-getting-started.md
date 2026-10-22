# Getting started

This page takes you from an empty project to one mapped entity, one registered
query, and one insert, in each of the three implementations. The sample
projects under `dotnet/samples/SimpleOrm.Sample`, `php/samples/SimpleOrm.Sample`,
and `go/orm/sample` are the complete versions of what is sketched here.

## Referencing the library

Nothing is published to NuGet, Packagist, or as a tagged Go module at the time
of writing. Every implementation is consumed from this repository by project
reference.

**C#.** Reference the core and one dialect package from your project file. The
core targets `netstandard2.0` and `net10.0`; the dialect packages do too.

```xml
<ItemGroup>
  <ProjectReference Include="..\..\src\SimpleOrm\SimpleOrm.csproj" />
  <ProjectReference Include="..\..\src\SimpleOrm.Sqlite\SimpleOrm.Sqlite.csproj" />
</ItemGroup>
```

Replace `SimpleOrm.Sqlite` with `SimpleOrm.SqlServer` or `SimpleOrm.Postgres`
for another dialect (see [Dialects](12-dialects.md)).

**PHP.** Add a Composer path repository pointing at `php/` and require
`simpleorm/simpleorm`. PHP 8.4 and `pdo_sqlite` are required; the other PDO
drivers only when you use their dialects.

```json
{
  "require": { "php": ">=8.4", "ext-pdo": "*", "simpleorm/simpleorm": "@dev" },
  "repositories": [ { "type": "path", "url": "../.." } ],
  "autoload": { "psr-4": { "SimpleOrm\\Sample\\": "src/" } }
}
```

**Go.** Import `github.com/kosmassp/SimpleOrm/go/orm` and one dialect package
(`.../orm/sqlite`, `.../orm/sqlserver`, `.../orm/postgres`), with a `replace`
directive in your `go.mod` pointing at the `go/` directory of this repository
until the module is tagged. Go 1.25 or later.

## Opening a session

A session owns one connection and at most one transaction. Options are fixed at
open: the dialect (required), the mapping configuration, type handlers, the
tracking mode, and the clock.

```csharp
using SimpleOrm;
using SimpleOrm.Sqlite;

await using var db = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect() }, ct);
```

Disposing the session rolls back any active transaction and closes the
connection. There is no close-and-reopen.

In PHP:

```php
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;

$db = Db::open('app.db', new DbOptions(new SqliteDialect()));
// ... later
$db->close();
```

In Go:

```go
import (
    "github.com/kosmassp/SimpleOrm/go/orm"
    "github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New()})
if err != nil { return err }
defer db.Close()
```

## The first entity

Mapping is opt-in: a property is mapped only when it carries `[Column]`. A
public settable property with no mapping attribute at all is a loader error
(`MAP-010`), so nothing is mapped by accident. Here is the sample's `User`,
trimmed to its columns:

```csharp
[Table("users")]
[Index(nameof(Email), Unique = true)]
public sealed class User
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public required string Name { get; set; }

    [Column]
    public required string Email { get; set; }

    [Column]
    public string? DisplayName { get; set; }

    [Column("created_at")]
    public DateTime CreatedAtUtc { get; set; }
}
```

Bare `[Column]` derives the column name by the naming convention (`DisplayName`
becomes `display_name`); `[Column("created_at")]` binds explicitly. `[Key]`
plus `[Generated]` is a database-generated integer key. Every detail is on
[Mapping entities](02-mapping-entities.md).

In PHP, attributes carry the same names and typed properties carry nullability:

```php
#[Table('users')]
#[Index(['email'], unique: true)]
final class User
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public string $email;

    #[Column]
    public ?string $displayName = null;
}
```

In Go, per-field facts live in the `orm` struct tag and class-level facts in an
`Entity()` descriptor; a pointer field is a nullable column:

```go
type User struct {
    ID          int64   `orm:"column,key,generated"`
    Name        string  `orm:"column"`
    Email       string  `orm:"column"`
    DisplayName *string `orm:"column"`
}

func (User) Entity() orm.EntityDef {
    return orm.EntityDef{
        Source:  orm.Table("users"),
        Indexes: []orm.IndexDef{orm.Index("Email").Unique()},
    }
}
```

## Creating the schema

For a throwaway or test database, the schema can be generated from the
metadata. Versioned migrations are the path for a real database (see
[Migrations](09-migrations.md)); `CreateTableAsync` is a dev/test utility.

```csharp
await db.CreateTableAsync<User>(ct);          // create table if not exists ... STRICT, plus the declared indexes
```

In Go: `orm.CreateTable[User](ctx, db)`. In PHP: `$db->createTable(User::class)`.

## The first query

SQL is declared once in a registry that binds it to an args type and a result
type. Parameters are `@Name` placeholders bound from the args record's
properties. The result type is an entity or any plain DTO.

```csharp
public static class Queries
{
    public static readonly Query<EmailArgs, User> UserByEmail = Query.Inline(
        "select id, name, email, display_name, created_at, updated_at from users where email = @Email");
}

public sealed record EmailArgs(string Email);

var user = await db.QuerySingleAsync(Queries.UserByEmail, new EmailArgs("ada@example.com"), ct);
```

Note the explicit column list. An entity result must match its mapped columns
exactly both ways (`MAP-001`, `MAP-002`), and `select *` is a lint error in
[SchemaGuard](11-schema-guard.md) (`VAL-021`).

In PHP, a registry is a class of public static methods returning `Query`:

```php
final class Queries
{
    public static function userByEmail(): Query
    {
        return Query::inline(EmailArgs::class, User::class,
            'select id, name, email, display_name, created_at, updated_at from users where email = @email');
    }
}

$user = $db->querySingle(Queries::userByEmail(), new EmailArgs('ada@example.com'));
```

In Go, an entry is a package-level value and execution is a package function:

```go
type UsersByEmailArgs struct{ Email string }

var UsersByEmail = orm.Inline[UsersByEmailArgs, User](
    "select id, name, email, display_name, created_at, updated_at from users where email = @Email")

user, err := orm.QuerySingle(ctx, db, UsersByEmail, UsersByEmailArgs{Email: "ada@example.com"})
```

For reads by key and simple filters you do not need a registry entry at all:
`db.GetAsync<User>(7, ct)` and `db.Query<User>().Where(...)` are generated from
the metadata. See [CRUD](04-crud-and-concurrency.md) and
[Criteria queries](05-criteria-queries.md).

## The first insert

Inserts are generated from the metadata with an explicit column list. The
generated key is written back onto the entity; the method returns nothing.

```csharp
var user = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = DateTime.UtcNow };
await db.InsertAsync(user, ct);
// user.Id is now set
```

The `DateTime` must have `Kind == Utc` (or `Local`, which converts); an
`Unspecified` kind is refused with `VAL-020`. See
[Mapping entities](02-mapping-entities.md#dates-are-utc).

In PHP:

```php
$ada = new User();
$ada->name = 'Ada';
$ada->email = 'ada@example.com';
$ada->createdAtUtc = new DateTimeImmutable('2026-09-08T12:00:00', new DateTimeZone('UTC'));
$db->insert($ada);
```

In Go, pass a pointer so the key lands on your instance:

```go
user := &User{Name: "Ada", Email: "ada@example.com"}
err = orm.Insert(ctx, db, user)
```

## Validate at startup

Once the registry and entities exist, run the schema guard once at startup. It
prepares every registered statement without executing it and compares the real
schema against your declarations, collecting every violation into one report.

```csharp
await SchemaGuard.ValidateAsync(db, typeof(Queries).Assembly, ct);
```

Details and the full list of checks are on [Schema guard](11-schema-guard.md).

## The temp-file test pattern

Tests run against a real SQLite file, created per fixture and deleted
afterwards. There is no server, no Docker, and no mocked connection anywhere.
The C# test fixture from `dotnet/tests/SimpleOrm.Tests/SqliteFixture.cs`:

```csharp
public sealed class SqliteFixture : IAsyncLifetime
{
    private readonly string _databasePath =
        Path.Combine(Path.GetTempPath(), $"simpleorm_test_{Guid.NewGuid():N}.db");

    public string ConnectionString => $"Data Source={_databasePath}";

    public Task InitializeAsync() => Task.CompletedTask;

    public Task DisposeAsync()
    {
        // Pooled connections keep the file handle open; clear them before deleting.
        SqliteConnection.ClearAllPools();
        File.Delete(_databasePath);
        return Task.CompletedTask;
    }
}
```

The PHP and Go equivalents, and the live legs for SQL Server and PostgreSQL,
are on [Testing your app](14-testing-your-app.md).

## Pitfalls

- `MAP-010`: a public settable property without `[Column]`, `[Ignore]`, or a
  relationship attribute. Mapping is opt-in; mark every property.
- `MAP-001` / `MAP-002`: the SQL's column list and the entity's mapped
  properties disagree. Entity results are exact both ways; use a DTO for a
  partial selection.
- `PRM-001` / `PRM-002`: a placeholder without an args property, or an args
  property no placeholder uses. Both directions are checked on every execution.
- `VAL-020`: a `DateTime` with `Kind == Unspecified` on write, or a stored
  value without a `Z` or offset on read. Store UTC, always.
- `QRY-001` / `QRY-002`: `QuerySingleAsync` found zero or more than one row.
  Use `QuerySingleOrDefaultAsync` when zero is legitimate.
- `TX-001`: `BeginAsync` while a transaction is already active. One
  transaction per session, no nesting.

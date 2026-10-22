# Testing your app

Tests run against a real database file: a temp-file SQLite database created
per fixture and deleted afterwards. There is no server to start, no Docker,
and no mocked connection anywhere in the library's own suites; the same
pattern serves your application. The live legs for SQL Server and PostgreSQL
run against a real server and skip as a group when none answers.

## The temp-file pattern in C#

The fixture from `dotnet/tests/SimpleOrm.Tests/SqliteFixture.cs`, shared by a
collection so the file is created once per test class:

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

[CollectionDefinition(Name)]
public sealed class SqliteCollection : ICollectionFixture<SqliteFixture>
{
    public const string Name = "sqlite";
}
```

A helper opens the session, applies the application's migrations (the
production way), and clears the rows:

```csharp
internal static class TestDb
{
    public static readonly DbOptions Options = new() { Dialect = new SqliteDialect() };

    public static readonly DateTime SeedTime = new(2026, 8, 28, 9, 30, 0, DateTimeKind.Utc);

    public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    public static async Task<User> InsertUserAsync(Db db, string name, string email, CancellationToken ct = default)
    {
        var user = new User { Name = name, Email = email, CreatedAtUtc = SeedTime };
        await db.InsertAsync(user, ct);
        return user;
    }

    public static async Task<Db> OpenAsync(SqliteFixture fixture, CancellationToken ct = default)
    {
        var db = await Db.OpenAsync(fixture.ConnectionString, Options, ct);

        // The schema arrives the production way: the sample's versioned migrations.
        await new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations").MigrateAsync(ct);

        foreach (var table in new[] { "transaction_details", "transactions", "user_roles", "user_profiles", "roles", "users" })
        {
            Command<EmptyArgs> clear = Query.Inline("delete from " + table);
            await db.ExecuteAsync(clear, EmptyArgs.Value, ct);
        }

        return db;
    }
}

[Collection(SqliteCollection.Name)]
public sealed class DbTransactionTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Committed_transaction_persists()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        await using (var tx = await db.BeginAsync(CancellationToken.None))
        {
            await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
            await tx.CommitAsync(CancellationToken.None);
        }

        var count = await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal(1L, count);
    }
}
```

(The `"delete from " + table` concatenation is a fixed list of table names in
test code, never user data.) For a throwaway schema without migrations,
`CreateTableAsync<T>`, `CreateViewAsync<T>`, and `CreateTriggersAsync<T>`
render it from the metadata.

### `Pooling=False`

`Microsoft.Data.Sqlite` pools connections, and a pooled connection keeps the
file handle open. Either call `SqliteConnection.ClearAllPools()` before deleting
the file (the fixture above) or open with pooling off when a test deletes and
recreates its file within one run:

```csharp
await using var db = await Db.OpenAsync($"Data Source={path};Pooling=False", TestDb.Options, CancellationToken.None);
```

## The temp-file pattern in PHP

`php/tests/Support/TempDatabase.php`: a bare path is a SQLite connection
string, and the delete is best-effort because Windows can hold a lock briefly
after SQLite closes its handle.

```php
final class TempDatabase
{
    public static function create(): self
    {
        $path = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_php_' . bin2hex(random_bytes(8)) . '.db';
        return new self($path);
    }

    public function connectionString(): string
    {
        return $this->path;
    }

    public function delete(): void
    {
        set_error_handler(static fn (): bool => true);
        try {
            foreach ([$this->path, $this->path . '-wal', $this->path . '-shm', $this->path . '-journal'] as $file) {
                if (is_file($file)) {
                    unlink($file);
                }
            }
        } finally {
            restore_error_handler();
        }
    }
}
```

Open with `Db::open($temp->connectionString(), new DbOptions(new SqliteDialect()))`,
migrate with `MigrationRunner` over `MigrationSet::fromDirectory(...)`, and
`$db->close()` before deleting. `composer test` runs the suite with
`pdo_sqlite` only.

## The temp-file pattern in Go

`go/orm/internal/testsupport/testsupport.go` registers the cleanup with
`t.Cleanup`:

```go
func TempDatabase(t testing.TB) string {
    t.Helper()
    path := filepath.Join(os.TempDir(), "simpleorm_go_"+randomHex(t, 8)+".db")
    t.Cleanup(func() {
        // Best effort: Windows can briefly hold a delete lock after the driver closes its handle.
        for _, file := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
            _ = os.Remove(file)
        }
    })
    return path
}
```

```go
func TestClose_RollsBackAnActiveTransaction(t *testing.T) {
    ctx := context.Background()
    path := testsupport.TempDatabase(t)

    db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
    if err != nil {
        t.Fatal(err)
    }
    if err := orm.CreateTable[sample.User](ctx, db); err != nil {
        t.Fatal(err)
    }
    ...
}
```

Run with `go test -p 2 ./...` on a machine where the file locks contend (the
repository's own runs need `-p 2`).

## SchemaGuard in a test

Run the guard against the migrated test database, so the registry and the
entities are proven against the schema the migrations produce, and so that a
pending migration fails the build:

```csharp
[Fact]
public async Task Sample_registry_and_entities_validate()
{
    await using var db = await TestDb.OpenAsync(fixture);
    await SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None);   // no throw
}
```

Assert on codes, never messages, when a failure is the expectation:

```csharp
var exception = await Assert.ThrowsAsync<SchemaValidationException>(
    () => SchemaGuard.ValidateAsync(db, typeof(User).Assembly, CancellationToken.None));
Assert.Contains(exception.Errors, e => e.Code == "MIG-030");
```

## A fixed clock

Hooks write `DbOptions.Clock`; fix it for deterministic `created_at` and
`updated_at` values. The conformance runners fix it at `2026-08-29T00:00:00Z`.

```csharp
var options = new DbOptions
{
    Dialect = new SqliteDialect(),
    Clock = () => new DateTime(2026, 8, 29, 0, 0, 0, DateTimeKind.Utc),
};
```

PHP: `new DbOptions(new SqliteDialect(), clock: fn () => new DateTimeImmutable('2026-08-29T00:00:00Z'))`.
Go: `orm.Options{Dialect: sqlite.New(), Clock: func() time.Time { return fixed }}`.

Seed timestamps the same way: `DateTime` values with `DateTimeKind.Utc`, never
`DateTime.Now` or a parsed string without a `Z` (`VAL-020`).

## Live legs against SQL Server and PostgreSQL

A live test runs against a real server, creates its own database named
`simpleorm_test_<hex>` on an admin connection, and drops it afterwards. The
whole leg skips when no server answers, so the default test run still needs
only the language toolchain.

| | Environment variable | Default when unset |
|---|---|---|
| PostgreSQL | `SIMPLEORM_POSTGRES` (an admin connection string with CREATEDB rights) | `localhost:5432`, `postgres`/`postgres` |
| SQL Server | `SIMPLEORM_SQLSERVER` (an admin connection string) | the LocalDB default instance where the platform has it |

The forms are each implementation's native one. C#: an ADO.NET connection
string. Go: `sqlserver://user:password@host:1433?database=master` or
`postgres://user:pass@host:5432/postgres`. PHP: a `sqlsrv:` or `odbc:` DSN such
as `odbc:Driver={ODBC Driver 17 for SQL Server};Server=host,1433;Database=master;UID=...;PWD=...`,
or a `pgsql:` DSN. Credentials are never committed; set the variable per
session.

The C# skip-as-a-group mechanism is a `FactAttribute` subclass over a probe
evaluated once per run:

```csharp
public sealed class PostgresFactAttribute : FactAttribute
{
    public PostgresFactAttribute()
    {
        if (!PostgresFixture.Available)
        {
            Skip = "PostgreSQL is not available (set SIMPLEORM_POSTGRES or run a local server on 5432)";
        }
    }
}
```

The fixture itself creates and drops the database from the admin connection:

```csharp
private static readonly string AdminConnectionString =
    Environment.GetEnvironmentVariable("SIMPLEORM_POSTGRES")
    ?? "Host=localhost;Port=5432;Username=postgres;Password=postgres;Database=postgres;Timeout=5";

private readonly string _database = $"simpleorm_test_{Guid.NewGuid():N}";

public string ConnectionString =>
    new NpgsqlConnectionStringBuilder(AdminConnectionString) { Database = _database }.ConnectionString;
```

Running the legs:

```bash
cd dotnet && dotnet test SimpleOrm.sln --filter "FullyQualifiedName~SqlServer"
cd php && composer test-live -- --filter SqlServer
go test -C go -count=1 -p 2 ./orm/sqlserver/ -run Live
```

PHP probes through `Tests\Support\LiveServer` and `skipUnlessAvailable()` in
`setUp`; Go through `testsupport.LiveServer` and `SkipUnlessAvailable(t, ...)`.
On SQL Server the test process must not run in globalization-invariant mode.

## Pitfalls

- The file is still open when you delete it: clear the pools or use
  `Pooling=False`; on Windows a brief delete lock is normal, so make the
  delete best-effort.
- `VAL-020` in seed data from `DateTime.Now` or `new DateTime(2026, 1, 1)`:
  always `DateTimeKind.Utc`.
- `MIG-030` in a SchemaGuard test after adding a migration: the test database
  must run the migrations, as `TestDb.OpenAsync` does.
- A live leg "passing" by skipping: check the test output for the skip count
  when you expect the server to be there.
- Two test classes sharing one fixture file and seeing each other's rows: use
  a collection fixture plus a clear step, or one file per class.
- `go test` without `-p 2` on a contended machine: SQLite lock timeouts show up
  as flaky failures.

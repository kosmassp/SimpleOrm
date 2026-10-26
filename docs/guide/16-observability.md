# Observability: the statement log

When something goes wrong, the first question is "what did it actually send to
the database?" The statement log answers it: one line of text per statement,
written as the statement is sent, to a function you choose. The library brings
no logging dependency; you forward the line to your own logger, print it, or
leave the option unset and nothing is written (the default).

## Seeing the SQL in one line

```csharp
await using var db = await Db.OpenAsync(connectionString, new DbOptions
{
    Dialect = new SqliteDialect(),
    Log = DbLog.Console,            // every statement to standard output
}, ct);

await db.GetAsync<User>(7, ct);
// User.get: select "id", "name", "email", "display_name", "created_at", "updated_at" from "users" where "id" = @k0 [@k0]
```

The line is `<name>: <sql>`, then the parameter names in bind order in square
brackets. The SQL is exactly what the driver receives: after `IN` lists are
expanded, after the dialect's rendering (`returning id` on SQLite and
PostgreSQL, `set nocount on; …; select @@rowcount` on SQL Server). Nothing is
reformatted or shortened, so a multi-line migration prints as the multi-line
statement it is.

## Forwarding to your logger

`Log` is an `Action<string>`, so wiring it to anything is one lambda:

```csharp
// Microsoft.Extensions.Logging
Log = line => logger.LogDebug("{Sql}", line),

// Serilog
Log = line => Serilog.Log.Debug("{Sql}", line),

// a test: collect the lines and assert on them
var lines = new List<string>();
Log = lines.Add,
```

Pick the level yourself: at `Debug` the log is off in production until you turn
the category up, which is the point. The function runs synchronously on the
calling thread; keep it cheap, and know that an exception it throws fails the
call like any other.

## Parameter values are off by default

The line names the parameters but does not show their values, because values
are where personal data lives. Ask for them when you need them:

```csharp
new DbOptions { Dialect = ..., Log = DbLog.Console, LogParameterValues = true }
// User.insert: insert into "users" (...) values (@name, @email, ...) returning "id" [@name = 'Ada', @email = 'ada@example.com', @display_name = null, @created_at = '2026-08-28T09:30:00.0000000Z', @updated_at = null]
```

Values show as they were bound, after conversion: strings single-quoted, a
null as `null`, bytes as `0x0102 (2 bytes)` (the first 16, an ellipsis when
there are more, always the length), an array parameter as `[1, 2, 3]`,
numbers in their invariant form.

## What the name tells you

| You called | The line starts with |
|---|---|
| a registry query or command | its declared name, `Queries.UserByEmail`, once SchemaGuard has enumerated the registry (it does at startup); before that `query` or `command`, or the resource path of an embedded entry |
| `InsertAsync` / `UpdateAsync` / `UpdateOnlyAsync` / `DeleteAsync` | `User.insert`, `User.update`, `User.updateOnly`, `User.delete` — a soft delete is still `User.delete`, as an update |
| `GetAsync`, a criteria query, `LoadAsync`, `Include(...)` in join mode | `User.get`, `User.criteria`, `Role.criteria` (a many-to-many load logs the link entity first), `User.include` |
| `FlushAsync`, `batch.SendAsync` | one line per statement, named as above, in statement order, under `Off` and `Auto` alike |
| `CreateTableAsync` and friends | `User.createTable`, `User.createIndex`, `User.createView`, `User.createTrigger` |
| the migration runner | `migrate` (a shadow run names its replay `shadow`; force-sync's own statements `sync`) |
| `SchemaGuard.ValidateAsync` | `validate Queries.UserByEmail`, `validate User` — the describe call and the introspection it causes |

A statement refused before it is sent (`CRUD-004`, `UOW-002`, `PRM-001`) logs
nothing: no SQL left the session. The line says nothing about the outcome
either: no duration, no row count, no error; those reach you as they always
did. If timing ever matters, a second line after the statement is a small
addition; it is not here yet.

## The CLI

`simpleorm migrate --log ...` (and `migrate down`, `shadow`, `validate`,
`diff --amend --db`) prints every statement the run sends, parameter names
only, to standard output.

## PHP and Go

The option means the same thing in all three implementations and the line
carries the same content; the function and the built-in sink take each
language's shape (see [PHP and Go](15-php-and-go.md#statement-log)):

```php
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), log: DbLog::stdout()));
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), log: fn (string $line) => $logger->debug($line), logParameterValues: true));
```

```go
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Log: orm.ConsoleLog})
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Log: func(line string) { slog.Debug(line) }})
var UserByEmail = orm.Inline[EmailArgs, User]("select …").Named("Queries.UserByEmail")   // Go cannot see the variable's name
```

The exact rule — what the line carries, when it is written, what is never
logged — is [`spec/session.md` "Statement log"](../../spec/session.md#statement-log-level-4-item-4-adr-0045).

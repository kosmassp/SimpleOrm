# PHP and Go

The two ports share no code with the C# reference; they share the spec and
the conformance suite, and they are correct when they pass the same files
unchanged. Every behavior and every error code on the previous pages applies
to them. What differs is the shape of a call, and only where the language
forces it. This page collects those shapes; the complete divergence tables are
[`php/CODING-STANDARD.md` section 10](../../php/CODING-STANDARD.md) and
[`go/CODING-STANDARD.md` section 10](../../go/CODING-STANDARD.md), and each
port's README (`php/README.md`, `go/README.md`) walks the whole surface.

## PHP

**Synchronous.** Every method blocks and takes no cancellation token;
`stream()` returns a `Generator`.

**Attributes.** PHP 8 attributes in `SimpleOrm\Metadata\Attributes` with the
C# names: `#[Table]`, `#[Column]`, `#[Key]`, `#[Generated]`, `#[Version]`,
`#[Index(['a', 'b', SortOrder::Desc], name: ..., unique: ...)]`, `#[Owned]`,
`#[CreatedAt]`, `#[UpdatedAt]`, `#[SoftDelete]`, `#[RenamedFrom]`, `#[Trigger]`.
Because PHP cannot express every neutral type by property type alone,
`#[Column(type: ColumnType::Int32)]` overrides the token (`Date`, `Time`,
`Guid`, `Bytes`, ...). Money is `SimpleOrm\Types\Decimal`; instants are
`DateTimeImmutable` in UTC (another zone is `VAL-020`). A typed property
without a default is required.

**Navigations** are `public private(set)` (a plainly public one is `MAP-011`)
and collection attributes name the target class first:

```php
#[OneToMany(Transaction::class, 'userId')]
public private(set) array $transactions = [];

#[ManyToMany(Role::class, through: UserRole::class)]
public private(set) array $roles = [];
```

**The guard.** The mapper `unset()`s a database-read entity's collection
navigations. With the opt-in trait the read is `REL-004`; without it, PHP's
own `Error`; never a silent `[]`:

```php
final class Transaction extends BaseModel
{
    use Navigations;
    ...
}
```

**The session.**

```php
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked, clock: fn () => $now));

$user  = $db->get(User::class, 7);
$link  = $db->get(UserRole::class, [$userId, $roleId]);        // key parts as a list, in key order
$maybe = $db->getOrDefault(User::class, 999);
$all   = $db->queryAll(Role::class);

$rows = $db->query(Queries::roleUsage(), EmptyArgs::value());
$one  = $db->querySingle(Queries::userByEmail(), new EmailArgs('ada@example.com'));
foreach ($db->stream(Queries::roleUsage(), EmptyArgs::value()) as $row) { ... }
$n    = $db->execute(Commands::cancelStaleTransactions(), new CancelStaleArgs($before, $now));
$days = $db->statement(DailySales::class, new DailySalesArgs($since));   // a [Statement] entity, by type

$db->insert($user);
$db->update($user);
$db->updateOnly($user, ['name']);
$db->delete(TransactionDetail::class, $detail->id);     // by key: the entity type first
$db->delete(Transaction::class, $transaction);          // by entity: version-checked

$page = $db->from(Transaction::class)->where(Criteria::eq('status', TransactionStatus::Pending))
    ->orderBy('createdAtUtc')->limit(2)->offset(1)->toList();

$db->load($transaction, 'user');
$db->loadEach($users, 'transactions');
$users = $db->from(User::class)->include('transactions', 'profile')->fetch(FetchMode::SubSelect)->toList();

$db->persist($order);  $db->remove($oldLine);  $summary = $db->flush();  $db->hasPendingWork();
$db->detach($entity);  $managed = $db->merge($detached);
$db->changesOf($a);    $db->isDirty($a);  $db->isTracked($a);  $db->refresh($a);

$scope = $db->begin();  $scope->commit();   // or $scope->rollback()
$db->close();
```

Property names in criteria, column lists, and `changesOf()` are the port's own
(`name`, `address.city`). The registry's statement-entity methods are
`statement()`, `statementSingle()`, `statementSingleOrDefault()`,
`streamStatement()`, since `query()` is taken by registered queries, and the
single-string `In` is `Criteria::inOne()`. `Criteria::in()` takes an array.

**Registries** are classes of public static methods returning `Query` or
`Command` (`Query::inline(ArgsClass::class, RowClass::class, $sql)`,
`Query::fromFile(...)` for a file). A raw-SQL DTO's nested list needs a fully
qualified `@param list<\App\DetailLine> $details` docblock for the JSON
handler, registered as `new JsonTypeHandler(DetailLine::class, list: true)`.

**Migrations** are discovered from a directory,
`MigrationSet::fromDirectory($dir, 'App\Migrations')`, with snapshots as files
(`SnapshotSet::fromDirectory($dir)`); steps declare `entityClass()` (and
`triggerClass()`); the draft marker is `#[\SimpleOrm\Migrations\Draft]`. The
runner is `new MigrationRunner($db->connection(), $db->options()->dialect, $db->maps(), $set, $snapshots)`
with `migrate()`, `migrateDown(3)`, `status()`.

**SchemaGuard** takes the class list: `SchemaGuard::validate($db, ClassScanner::classes($srcDir, 'App'), $set, $snapshots)`.

**The CLI** takes a PSR-4 root instead of an assembly:

```bash
php bin/simpleorm migrate --src src --namespace 'App' --db app.db
php bin/simpleorm diff --src src --namespace 'App' --name AddNote --draft
php bin/simpleorm shadow --src src --namespace 'App' --from V0001 --to V0003
```

**Dialects**: `SqliteDialect`, `SqlServerDialect` (a `sqlsrv:` or `odbc:` DSN),
`PostgresDialect` (`pgsql:`), all over PDO; bytes bind as `PDO::PARAM_LOB` by
the metadata token. **Tests**: `cd php && composer install && composer test`;
`composer test-live` for the SQL Server and PostgreSQL legs.

## Go

**Context everywhere.** Every I/O function takes a `context.Context` first;
no `Async` suffix; cancellation surfaces as the context's error.

**Struct tags plus a descriptor.** Per-field facts live in the `orm` tag;
class-level facts and typed references in `Entity() orm.EntityDef`:

```go
type Transaction struct {
    BaseModel
    ID      int64             `orm:"column,key,generated"`
    UserID  int64             `orm:"column"`
    User    *User             `orm:"many_to_one=UserID"`
    Status  TransactionStatus `orm:"column"`
    Amount  orm.Decimal       `orm:"column"`
    Version int64             `orm:"column,version"`
    Note    *string           `orm:"column"`
    Details []*TransactionDetail `orm:"one_to_many=TransactionID,cascade=persist+remove"`
}

func (Transaction) Entity() orm.EntityDef {
    return orm.EntityDef{
        Source:      orm.Table("transactions"),
        ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[User]("UserID")},
        Indexes:     []orm.IndexDef{orm.Index("UserID")},
    }
}
```

Tag options: `column`, `column=<name>`, `key`, `generated`, `version`,
`enum_int`, `type=<token>`, `ignore`, `many_to_one=`, `one_to_many=`,
`one_to_one=`, `many_to_many`, `cascade=`, `created_at`, `updated_at`,
`soft_delete`, `renamed_from=`, `owned`. A malformed tag is `MAP-023`. A
pointer field is a nullable column; an embedded struct shares its fields
(`BaseModel`); an enum is a named type implementing `orm.Enum`; money is
`orm.Decimal`; `time.Time` always carries a location, so the write-side
`VAL-020` is unreachable while the read-side one stands.

**Generics as package functions**, because methods cannot have type parameters:

```go
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Tracking: orm.TrackingTracked})
defer db.Close()

user, err := orm.Get[User](ctx, db, 7)
link, err := orm.Get[UserRole](ctx, db, []any{userID, roleID})     // composite key as []any, in key order
maybe, err := orm.GetOrDefault[User](ctx, db, 999)                 // *User, nil when absent
all, err := orm.QueryAll[User](ctx, db)

users, err := orm.Query(ctx, db, UsersByEmail, UsersByEmailArgs{Email: "ada@example.com"})
for row, err := range orm.Stream(ctx, db, AllUsers, orm.EmptyArgs{}) { ... }
n, err := orm.Execute(ctx, db, MarkTransactionStatus, args)
days, err := orm.QueryStatement[DailySales](ctx, db, DailySalesArgs{Since: since})

err = orm.Insert(ctx, db, &user)
err = orm.Update(ctx, db, &user)
err = orm.UpdateOnly(ctx, db, &user, "Name")
err = orm.Delete[User](ctx, db, 7)            // by key
err = orm.DeleteEntity(ctx, db, &order)       // version-checked

recent, err := orm.From[Transaction](db).
    Where(orm.Or(orm.Eq("Status", "Pending"), orm.In("ID", ids...)), orm.Ge("CreatedAtUtc", since)).
    OrderBy("CreatedAtUtc", orm.Desc).Limit(20).
    List(ctx)

err = orm.Load(ctx, db, &order, "User")
err = orm.LoadEach(ctx, db, orders, "Details")
users, err := orm.From[User](db).Include("Transactions", "Profile").Fetch(orm.FetchSubSelect).List(ctx)

err = orm.Persist(db, order);  err = orm.Remove(db, existing);  summary, err := db.Flush(ctx);  db.HasPendingWork()
err = orm.Detach(db, &user);   managed, err := orm.Merge(ctx, db, &copy)
changed, err := orm.ChangesOf(db, a);  dirty, err := orm.IsDirty(db, a);  err = orm.Refresh(ctx, db, a)

tx, err := db.Begin(ctx);  defer tx.Rollback(ctx);  err = tx.Commit(ctx)
```

**The pointer is the tracked shape.** `orm.Get[*User]`, `orm.From[*User]`, and
`[]*T` navigations resolve through the identity map; a value-shaped result is
a copy and is never registered.

**The guard.** A slice read cannot be intercepted, so `REL-004` is
unreachable: a never-loaded collection is a nil slice, a loaded-but-empty one
an empty non-nil slice; `UOW-005` follows the same distinction.

**Errors are values.** `*orm.Error` with `Code`, `Target`, `Message`, `Cause`;
match with `errors.As(err, &ormErr)` or `orm.CodeOf(err)`. `TX-002` wraps the
pgx cached-plan error on PostgreSQL.

**Streaming** is `iter.Seq2[T, error]`: `for row, err := range orm.Stream(...)`.

**Explicit registration.** There is no assembly scan; the registry is a value
the application builds once and hands to SchemaGuard, the runner, and the CLI:

```go
func App() (orm.Registry, error) {
    set, err := Set()      // orm.NewMigrationSet(V0001{}, V0002{}, ...)
    if err != nil {
        return orm.Registry{}, err
    }
    return orm.Registry{
        Entities:   []reflect.Type{orm.TypeOf[sample.User](), orm.TypeOf[sample.Role]()},
        Triggers:   []reflect.Type{orm.TypeOf[sample.RoleAuditTrigger]()},
        Entries:    []orm.Entry{sample.UsersByEmail, sample.MarkTransactionStatus},
        Migrations: set,
        Snapshots:  Snapshots,
    }, nil
}

//go:embed Table/*/*.schema.json View/*/*.schema.json Trigger/*/*.schema.json
var Snapshots embed.FS

err = orm.Validate(ctx, db, registry)
```

**Migrations** are types with the reference's names, one package per object
directory; a step embeds `orm.TableMigration[sample.User]` (or
`orm.ViewMigration`, `orm.TriggerMigration[sample.Role, sample.RoleAuditTrigger]`);
`Down`, `PreDown`, `PostDown` are optional interfaces; the draft marker is a
method `func (V0012) IsDraft() bool { return true }`.

**The CLI is a library** the application's `main` embeds:

```go
func main() { os.Exit(cli.Run(context.Background(), App, os.Args[1:], os.Stdout, os.Stderr)) }
```

```bash
simpleorm migrate --db app.db
simpleorm migrate down --to 3 --db app.db --snapshots ./migrations
simpleorm diff --out ./migrations --name AddNote
```

**Dialects**: `sqlite.New()`, `sqlserver.New()`, `postgres.New()` (with
`postgres.WithCachedPlanPolicy(...)`), each importing one driver; `@name`
placeholders are rewritten to `$n` for pgx by the dialect's connector.
**Tests**: `cd go && go vet ./... && go test -p 2 ./...`; the live legs are
`orm/sqlserver/live_test.go` and `orm/postgres/live_test.go`.

## Statement log

The option means the same in all three implementations ([Observability](16-observability.md));
the function and the sink take each language's shape.

```php
// PHP: a closure, or the built-in sink writing to php://stdout; values off unless asked
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), log: DbLog::stdout()));
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), log: fn (string $line) => $logger->debug($line), logParameterValues: true));
```

PHP learns a registry entry's name (`Queries.userByEmail`) from the SchemaGuard
scan, keyed by the entry's SQL text, since a registry method builds a fresh
`Query` on every call. `bin/simpleorm migrate --log` prints the run's statements.

```go
// Go: a func, or orm.ConsoleLog writing to os.Stdout
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Log: orm.ConsoleLog})
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Log: func(line string) { slog.Debug(line) }, LogParameterValues: true})

// a registry entry has no variable name Go can see: declare one (optional)
var UserByEmail = orm.Inline[EmailArgs, User]("select …").Named("Queries.UserByEmail")

// the tooling takes a pool, not a session: hand it the sink through the context
ctx = migrations.WithStatementLog(ctx, orm.ConsoleLog, false)
```

The CLI's `--log` flag is the same in all three.

## Pitfalls

- PHP: a plainly `public` navigation is `MAP-011`; `private(set)` is the shape.
- PHP: `$db->delete()` takes the entity type first, even for the entity form.
- PHP: property names are the port's own spelling (`'createdAtUtc'`), not the
  C# one, in criteria and column lists.
- PHP: reading an unloaded collection without the `Navigations` trait is a
  PHP `Error`, not `REL-004`; both are refusals.
- Go: `orm.Get[User]` returns a copy in a tracked session; use `orm.Get[*User]`
  when identity matters.
- Go: `OpenRetryDelay: 0` is the one-second default, not "no wait" — that is
  `orm.NoOpenRetryDelay`; `CommandTimeout: 0` is unset, the driver's own behaviour.
- Go: a nil slice navigation means "never loaded"; an empty non-nil slice
  means "loaded and empty". Do not initialize collection fields to `nil` and
  expect a persist cascade to see them.
- Go: `orm.Delete[T]` and `orm.DeleteEntity` are two functions; only the
  second checks the version.
- Go: a malformed struct tag is `MAP-023` at load, so load every entity in a
  test (`orm.Validate` does).
- Both: the registry (Go) or the scanned class list (PHP) must include every
  trigger class and every entity, or the scan rules (`MAP-028`, `MAP-029`)
  have nothing to see.

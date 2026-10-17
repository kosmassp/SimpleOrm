# SimpleOrm — Go port

The Go implementation of the SimpleOrm spec, **Levels 0–3** (CLAUDE.md §12: the
first port covers the mapper, the micro-ORM with generated CRUD, the ADR-0012
criteria core, SchemaGuard, and code migrations with the diff generator). It
shares no code with the C# reference; it shares `../spec/` and `../conformance/`,
and it is correct when it passes the same conformance files unchanged
(ADR-0027).

Requirements: Go 1.25+. The runtime dependencies are one driver per dialect
package — `modernc.org/sqlite` (pure Go, no cgo — the analog of
`Microsoft.Data.Sqlite`/`pdo_sqlite`) for `orm/sqlite`, `github.com/microsoft/go-mssqldb`
for `orm/sqlserver`, `github.com/jackc/pgx/v5` for `orm/postgres`; an application
links only the dialects it imports. Tests use the standard `testing` package
against real temp-file databases; the SQL Server and PostgreSQL legs run against a
real server and skip where none answers (see "Dialects").

```bash
cd go
go vet ./... && go test ./...
```

Layout mirrors the C# assemblies (the public surface) and areas (the internals):

```
orm/                    package orm — the session (Open, Query, Insert, Get, From …), the
                        registry (Inline, Embedded, Registry), SchemaGuard (Validate), and
                        aliases of every contract: the one import application code needs
orm/sqlite              the SQLite dialect (Dialect, New) and the shadow replayer (Shadow)
orm/sqlserver           the SQL Server dialect over go-mssqldb (Dialect, New; sp_describe_first_result_set describer)
orm/postgres            the PostgreSQL dialect over pgx (Dialect, New; the @name → $n connector, Prepare-based describer)
orm/migrations          versions, steps, actions, runner, snapshots, derived downs, generator,
                        diff/amend, force sync (application code reaches it through orm's aliases)
orm/cli                 the simpleorm command as a library: cli.Run(ctx, registry, args, out, err)
orm/internal/core       the spec-level contracts: errors, EntityMap, tokens, Decimal/GUID/Enum,
                        naming, TypeHandler, the Dialect seam, the AST, the canonical JSON writer
orm/internal/metadata   the loaders (struct tags + descriptor, convention, builder) and the export
orm/internal/mapping    the conversion table, the JSON handler, the result mapper
orm/internal/params     @name binding and IN-list expansion
orm/internal/render     the reference SELECT rendering
orm/sample              the ten fixture entities and their migrations tree (mirrors dotnet/samples)
orm/internal/conformance one data-driven runner per conformance folder
```

## The shape

Entities are structs: per-field facts live in the `orm` tag, class-level
declarations and typed references in a small descriptor.

```go
type User struct {
    BaseModel                                 // embedded: created_at, updated_at
    ID           int64          `orm:"column,key,generated"`
    Name         string         `orm:"column"`
    Email        string         `orm:"column"`
    DisplayName  *string        `orm:"column"`                 // nullable = pointer
    Transactions []*Transaction `orm:"one_to_many=UserID"`     // declaration-only until Level 2
    Roles        []*Role        `orm:"many_to_many"`
    Profile      *UserProfile   `orm:"one_to_one=UserID"`
}

func (User) Entity() orm.EntityDef {
    return orm.EntityDef{
        Source:     orm.Table("users"),
        Indexes:    []orm.IndexDef{orm.Index("Email").Unique(), orm.Index("DisplayName")},
        ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[UserRole]("Roles")},
    }
}
```

Queries are declared once, next to their args; the session runs them:

```go
var UsersByName = orm.Inline[UsersByNameArgs, User]("select id, name, … from users where name like @Pattern")

db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New()})
defer db.Close()

users, err := orm.Query(ctx, db, UsersByName, UsersByNameArgs{Pattern: "A%"})
for user, err := range orm.Stream(ctx, db, AllUsers, orm.EmptyArgs{}) { … }

tx, err := db.Begin(ctx)
defer tx.Rollback(ctx)
n, err := orm.Execute(ctx, db, MarkShipped, MarkShippedArgs{ID: 7})
err = tx.Commit(ctx)

err = orm.Insert(ctx, db, &order)                 // the generated key lands on the entity
order, err := orm.Get[Order](ctx, db, 7)          // CRUD-001 when missing
link, err := orm.Get[UserRole](ctx, db, []any{userID, roleID})
err = orm.Update(ctx, db, &order)                 // optimistic concurrency when a version column is mapped
err = orm.UpdateOnly(ctx, db, &order, "Status")   // only the listed fields (ADR-0028); same key/version rules
err = orm.UpdateOnly(ctx, db, &profile, "Address") // an owned navigation (orm.OwnedType + `orm:"owned"`, ADR-0030) stands for its members

// Level 2 loading (spec/loading.md): nothing loads implicitly — a never-loaded collection is a nil slice,
// a loaded-but-empty one an empty non-nil slice; singular navigations are nil until loaded
err = orm.Load(ctx, db, &order, "Customer")                        // one entity, one navigation
err = orm.LoadEach(ctx, db, orders, "Lines")                       // []*Order: one batched query per navigation
users, err := orm.From[User](db).
    Include("Transactions", "Profile").Fetch(orm.FetchSubSelect).  // or FetchMultiQuery (default), FetchJoin
    OrderBy("ID").Limit(20).List(ctx)
err = orm.Delete[Order](ctx, db, 7)               // by key; orm.DeleteEntity(ctx, db, &order) checks the version

recent, err := orm.From[Order](db).
    Where(orm.Or(orm.Eq("Status", "Pending"), orm.In("ID", ids...)), orm.Ge("CreatedAtUtc", since)).
    OrderBy("CreatedAtUtc", orm.Desc).Limit(20).
    List(ctx)

err = orm.Validate(ctx, db, app.Registry)         // SchemaGuard: the complete report, or nil
```

### Tracking (Level 3 M1, ADR-0034)

Tracking is a session mode chosen at open; the default is untracked — the
Level 0–2 session, nothing remembered. Opt in per session:

```go
db, err := orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New(), Tracking: orm.TrackingTracked})

a, err := orm.Get[*User](ctx, db, 1)               // the pointer shape is the tracked one
b, err := orm.Get[*User](ctx, db, 1)               // a == b: one instance per (type, key); the re-read does not apply the row
a.Name = "Ada Lovelace"
changed, err := orm.ChangesOf(db, a)              // ["Name"] — dotted for owned members ("Address.City"); UOW-001 if untracked
err = orm.Update(ctx, db, a)                      // writes only the dirty columns; a clean instance writes nothing
err = orm.Refresh(ctx, db, a)                     // explicit overwrite from the row; works in both modes
```

Only a pointer can be the session's instance, so **pointer-shaped results are
the tracked ones**: `orm.From[*User](db)`, `orm.Query[A, *User]` (an entry
declared with a pointer result), `orm.QueryAll[*User]`, `orm.Get[*User]`,
`orm.GetOrDefault[User]` (already `*User`), and `[]*T` navigations loaded by
`Load`/`Include` in every fetch mode. A value-shaped result (`[]User`,
`orm.Get[User]`) is a copy: never registered, and for a key the session
tracks it copies the tracked instance's current state. A write through an
instance that is not the session's instance for its key — another instance
holds the key, or a tracked instance's key fields were edited — is `UOW-002`
before any SQL. Insert tracks the new instance; Delete forgets the key.

### Unit of work (Level 3 M2, ADR-0034)

A tracked session schedules writes and flushes them in one go; an untracked one
refuses every call with `UOW-003`. Nothing is written or loaded before `Flush`:

```go
user := &User{Name: "Linus", Email: "linus@example.com", CreatedAtUtc: now}
order := &Transaction{Status: sample.Pending, Amount: orm.MustDecimal("5"), CreatedAtUtc: now, User: user}

err = orm.Persist(db, order)                  // schedules an insert (a tracked instance schedules nothing itself)
err = orm.Persist(db, user)
err = orm.Remove(db, existing)                // schedules a delete of a tracked instance (UOW-001 otherwise)
summary, err := db.Flush(ctx)                 // inserts, dirty updates, deletes — FK order, keys flowed; one transaction
// summary.Inserted == 2: the user went first and its generated key reached order.UserID
db.HasPendingWork()                           // anything scheduled? (dirty tracked instances flush regardless; false when untracked)
```

The flush writes **inserts** (each after the scheduled parents it references
through a non-nil many-to-one navigation or a persist cascade; otherwise types in
foreign-key order, then schedule order), then every **dirty tracked instance** as a
column-list update, then **deletes**, children first. Every statement is the
session's own `Insert`/`Update`/`DeleteEntity`, so the identity gate, the version
rules, and the snapshots apply unchanged. Inside `db.Begin` the flush uses your
transaction and leaves it open; otherwise it owns one, and a failure rolls it back,
keeps the schedule, and puts the identity map, snapshots, versions, and generated
keys back as they were.

Cascades are **declared** on the navigation, per operation, with the tag option
`cascade=persist`, `cascade=remove`, or `cascade=persist+remove`
(`orm:"one_to_many=TransactionID,cascade=persist+remove"`; `many_to_many` cannot
cascade, `MAP-025`). A cascade walks only what is in memory: a persist cascade
schedules the non-nil, initialized navigation's targets (a nil slice is "never
loaded" and cascades nothing); a remove cascade removes a loaded collection's
members and is `UOW-005` over a nil slice — `orm.Load` it first, nothing loads
implicitly. A nil singular navigation cascades nothing. A refusal anywhere in a
cascade schedules nothing.

### Hooks, detach and merge (Level 3 M3, ADR-0034)

Hooks are **declared on the column**, never registered callbacks, and the session acts on them in every write —
the immediate operations and the unit of work alike. The session clock is `orm.Options.Clock` (UTC; the wall
clock by default; a non-UTC instant is `VAL-020`):

```go
type BaseModel struct {
    CreatedAtUtc time.Time  `orm:"column=created_at,created_at"`   // filled on insert when unset (the zero instant)
    UpdatedAtUtc *time.Time `orm:"column=updated_at,updated_at"`   // stamped on every write that changes the row
}
type Role struct {
    BaseModel
    ID        int64  `orm:"column,key,generated"`
    IsDeleted bool   `orm:"column,soft_delete"`                    // a delete sets this instead of removing the row
}
```

`updated_at` joins the SET of a full-row update, of `UpdateOnly` (after your columns), of a tracked dirty update,
and of a soft delete — and is never what makes a tracked instance dirty (a clean instance writes and stamps
nothing). On a `soft_delete` entity `orm.DeleteEntity` (and a flushed `Remove`) is the version-checked update
that sets the flag; `orm.Delete[T]` by key does the same, but refuses a versioned entity with `CRUD-012`. **Reads
never filter on the flag**: write `orm.Eq("IsDeleted", false)` yourself.

```go
err = orm.Detach(db, &user)                 // the session forgets it (and cancels what was scheduled for it); UOW-001 for a stranger
managed, err := orm.Merge(ctx, db, &copy)   // the session's instance for the copy's key (reads the row if unknown): copies every
                                            // mapped non-key value, the version included; navigations are not merged; CRUD-003 for a non-table type, CRUD-002 for an unset key
```

Both need a tracked session (`UOW-003` otherwise).

Errors are values with a stable code: `errors.As(err, &e)` on an `*orm.Error`,
or `orm.CodeOf(err)`. Every I/O function takes a `context.Context`.

How Go differs from the reference — and only there — is the §10 table in
[CODING-STANDARD.md](CODING-STANDARD.md): the tag-plus-descriptor shape, errors as
values, `context.Context` instead of cancellation tokens, `iter.Seq2` streaming,
generics as package functions, explicit registration instead of assembly
scanning, `embed.FS` snapshots, the CLI as a library. Read the standard before
contributing; it is the uniformity contract.

## Migrations

Versions and steps are types with the reference's names (the runner parses
them); each object's steps live in their own package under the migrations
package, exactly the layout the diff generator emits:

```go
// migrations/V0002.go
type V0002 struct{}

func (V0002) Compose(version *orm.VersionBuilder) { version.Apply(user.V0002_AddDisplayName{}) }

// migrations/Table/User/V0002_AddDisplayName.go
type V0002_AddDisplayName struct{ orm.TableMigration[models.User] }

func (V0002_AddDisplayName) Action(actions *orm.TableActions) {
    actions.AddColumn("display_name", "TEXT").Post("update users set display_name = name")
}

//go:embed Table/*/*.schema.json View/*/*.schema.json
var Snapshots embed.FS                           // rollbacks derive from these (ADR-0018)
```

The application registers everything once and embeds the CLI in its own `main`:

```go
var App = orm.Registry{
    Entities:   []reflect.Type{orm.TypeOf[User](), orm.TypeOf[Role]()},
    Entries:    []orm.Entry{UsersByName, MarkShipped},
    Migrations: migrationSet,                    // orm.NewMigrationSet(V0001{}, V0002{}, …)
    Snapshots:  migrations.Snapshots,
}

func main() { os.Exit(cli.Run(context.Background(), App, os.Args[1:], os.Stdout, os.Stderr)) }
```

```bash
simpleorm migrate --db app.db
simpleorm migrate down --to 3 --db app.db --snapshots ./migrations
simpleorm status --db app.db
simpleorm validate --db app.db
simpleorm snapshot --out ./migrations --db app.db
simpleorm diff --out ./migrations --name AddNote
simpleorm shadow --out ./migrations --from V0006 --to V0009
```

There is no `--assembly`: the registry *is* the application. `--package` (the
migrations package's import path, for the code `diff` emits) defaults to the
package of the registered versions; `--dialect sqlite|sqlserver|postgres`
(default `sqlite`) picks the dialect for every database command — `--db` is then
that driver's native connection string — and `shadow` accepts SQLite only.

### Dialects

Three dialects ship (spec/dialects.md): `sqlite.New()` (the reference rendering),
`sqlserver.New()` (ADR-0024) and `postgres.New()` (ADR-0025). Each opens its
driver's native connection string:

```go
orm.Open(ctx, "app.db", orm.Options{Dialect: sqlite.New()})
orm.Open(ctx, "sqlserver://user:pass@host:1433?database=app", orm.Options{Dialect: sqlserver.New()}) // or an ADO-style server=…;database=… string
orm.Open(ctx, "postgres://user:pass@localhost:5432/app", orm.Options{Dialect: postgres.New()})       // or a key/value DSN
```

The two non-SQLite dialects are **runtime-first**: the session, generated CRUD,
criteria queries, loading in every fetch mode, tracking, the unit of work, hooks,
SchemaGuard (each dialect describes statements through its driver), and the
migration runner with the dialect's run lock (`sp_getapplock` /
`pg_advisory_xact_lock`) all work. The snapshot, shadow, diff, and force-sync
tooling stays SQLite-only, so a rollback there needs a `Down()` override
(`MIG-020` says so). On PostgreSQL a slice argument binds as one typed array
parameter (`where id = any(@Ids)`), and a legacy `timestamp without time zone` /
`datetime2` column refuses `VAL-020` at read time unless a `TypeHandler` declares it.

The live legs (`orm/sqlserver/live_test.go`, `orm/postgres/live_test.go`) run
against a real server and skip as a group where none answers; set
`SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER` to an admin connection string with
the right to create databases (defaults: `postgres://postgres:postgres@localhost:5432/postgres`;
`sqlserver://localhost:1433?database=master&encrypt=disable` with integrated
authentication). Each test creates and drops its own `simpleorm_test_<hex>` database.

### Draft migrations (Level 3 M5, ADR-0034)

A column that was renamed declares its previous name with the `renamed_from`
tag option, beside `column` (`orm:"column=role_name,renamed_from=name"`).
`diff` reads it exactly like `--rename roles.name=role_name` (one
data-preserving `RenameColumn`, not a drop and an add), but the declaration
stays with the model. It is permanent and harmless: once the snapshot has the
new column it declares nothing, and an explicit `--rename` still wins. The
manual builder spells it `Property("Name").RenamedFrom("name")`.

`simpleorm diff --draft` writes the new root with a marker method:

```go
func (V0012) IsDraft() bool { return true }
```

`migrate` and `baseline` refuse a pending draft with `MIG-013` before anything
executes (the earlier versions stay unapplied too; a baseline *below* the
draft is fine). `status` lists its steps as `Draft`, `shadow` replays it, and
`diff --amend` regenerates it and keeps the marker. Review the generated code,
then delete the method; the data-driven form is `orm.NewSQLVersion(...).AsDraft()`.

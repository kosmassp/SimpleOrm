# SimpleOrm — Go port

The Go implementation of the SimpleOrm spec, **Levels 0–1** (CLAUDE.md §12: the
first port covers the mapper, the micro-ORM with generated CRUD, the ADR-0012
criteria core, SchemaGuard, and code migrations with the diff generator). It
shares no code with the C# reference; it shares `../spec/` and `../conformance/`,
and it is correct when it passes the same conformance files unchanged
(ADR-0027).

Requirements: Go 1.25+. The only runtime dependency is `modernc.org/sqlite`
(pure Go, no cgo — the analog of `Microsoft.Data.Sqlite`/`pdo_sqlite`); tests
use the standard `testing` package against real temp-file databases.

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
err = orm.Delete[Order](ctx, db, 7)               // by key; orm.DeleteEntity(ctx, db, &order) checks the version

recent, err := orm.From[Order](db).
    Where(orm.Or(orm.Eq("Status", "Pending"), orm.In("ID", ids...)), orm.Ge("CreatedAtUtc", since)).
    OrderBy("CreatedAtUtc", orm.Desc).Limit(20).
    List(ctx)

err = orm.Validate(ctx, db, app.Registry)         // SchemaGuard: the complete report, or nil
```

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
package of the registered versions; `--dialect` accepts only `sqlite` today.

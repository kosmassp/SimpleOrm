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
orm/sqlite              the SQLite dialect (Dialect, New) and its shadow host (ShadowHost; Shadow wraps the replay)
orm/sqlserver           the SQL Server dialect over go-mssqldb (Dialect, New; sp_describe_first_result_set describer; ShadowHost)
orm/postgres            the PostgreSQL dialect over pgx (Dialect, New; the @name → $n connector, Prepare-based describer; ShadowHost with the trigger read-back)
orm/migrations          versions, steps, actions, runner, snapshots, derived downs, generator,
                        diff/amend, force sync, and the shadow replay (Shadow, ShadowHost) — on every dialect
                        (application code reaches it through orm's aliases)
orm/cli                 the simpleorm command as a library: cli.Run(ctx, registry, args, out, err)
orm/internal/core       the spec-level contracts: errors, EntityMap, tokens, Decimal/GUID/Enum,
                        naming, TypeHandler, the Dialect seam, the AST, the canonical JSON writer
orm/internal/metadata   the loaders (struct tags + descriptor, convention, builder) and the export
orm/internal/mapping    the conversion table, the JSON handler, the result mapper
orm/internal/params     @name binding and IN-list expansion
orm/internal/render     the reference SELECT rendering
orm/sample              the eleven fixture entities, the trigger class, and their migrations tree (mirrors dotnet/samples)
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

### Statement batching (Level 4, ADR-0044)

A *message* is what one round trip sends. By default (`Batching: orm.BatchingOff`) every statement is a message of
its own — the Level 0–3 session exactly. Opt in per session, and the statements of **one call** (a `Flush`, the
`Send` of an explicit batch) go out in as few messages as the plan allows; nothing waits and nothing is deferred
past the call that holds the statements — no timer, no implicit queue:

```go
db, err := orm.Open(ctx, conn, orm.Options{Dialect: postgres.New(), Tracking: orm.TrackingTracked,
    Batching: orm.BatchingAuto, MaxStatementsPerMessage: 100}) // 0 means the default, 100; negative is refused at Open
db.BatchingInEffect()   // true on PostgreSQL (pgx Batch) and SQL Server (one multi-statement text); false on SQLite

b := db.Batch()                                   // queuing does no I/O
err = orm.BatchInsert(b, &order)                  // generic package functions beside the immediate calls
err = orm.BatchUpdate(b, &customer)
err = orm.BatchUpdateOnly(b, &user, "Name")
err = orm.BatchDelete[Role](b, 3)                 // by key; orm.BatchDeleteEntity(b, &x) is the version-checked form
err = orm.BatchExecute(b, MarkShipped, MarkShippedArgs{ID: 7})   // a registry command
result, err := b.Send(ctx)                        // result.Affected: one count per queued operation; result.Messages
b.Discard()                                       // the dispose analog: drop an unsent batch (TX-003 on any further use)
```

The plan never reorders, adds, or drops a statement. A statement starts a new message when it **depends on the
message in progress** (it writes an instance the message already writes — or another instance of the same type with the same set key, which the identity gate would otherwise read before the message updated it — or its many-to-one navigation — or, in a
flush, its cascade owner — points at an instance inserted in the message with a database-generated key: the keys must
come back before the statement is prepared), when it **reads a result after a registry command**, or when it would
**overflow** the parameter cap (SQL Server's 2098). A message that holds `MaxStatementsPerMessage` statements goes out as soon as it is full, before the next statement is prepared. So a graph of parents and
children flushes in as many messages as it has levels. Each queued operation behaves exactly as its immediate call
would at that point of the sequence; keys do not flow between queued operations (the unit of work is the form that
flows keys). `Send` always runs in a transaction — yours, left open, or one it owns. A failed call undoes every
in-memory effect of every statement it prepared and rolls its own transaction back; inside your transaction the
rows of the statements that ran — under batching, possibly some after the failing one in its message — are yours to
roll back. SQLite has no round trip to save: its messages run one statement at a time, reusing one prepared command
per distinct statement text within the call.

### Performance (Level 4, ADR-0048)

Nothing to configure. A session renders each entity's generated CRUD text once (per column set for column-list
updates); binding plans are cached per SQL text and args type, mapping plans per result type and column list;
loading correlates single integer keys by `int64` and skips the post-fetch sort only where the dialect stores the key
as an integer or exact numeric type (a SQLite `TEXT` decimal, strings, GUIDs, and temporals still sort, so every
collection stays in value order). The SQL sent is unchanged. Benchmarks:
`go test -C go -run '^$' -bench . -benchmem ./orm/` (PostgreSQL legs skip without a server).

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

//go:embed Table/*/*.schema.json View/*/*.schema.json Trigger/*/*.schema.json
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
(default `sqlite`) picks the dialect for every command — `--db` is then that driver's
native connection string (a bare path also works for SQLite). `snapshot`, `shadow`, `diff`
(and `--amend`), `migrate down`, and `migrate --force` work on all three (ADR-0043,
"Migration tooling beyond SQLite" below); `shadow` on SQL Server and PostgreSQL takes the
**admin** connection as `--db` (the throwaway `simpleorm_shadow_<hex>` database is created
and dropped there).

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
`pg_advisory_xact_lock`) all work; since ADR-0043 so does the migration tooling
(snapshot, shadow, diff, derived rollbacks, force sync — see below). On PostgreSQL a slice argument binds as one typed array
parameter (`where id = any(@Ids)`), and a legacy `timestamp without time zone` /
`datetime2` column refuses `VAL-020` at read time unless a `TypeHandler` declares it.

The live legs (`orm/sqlserver/live_test.go`, `orm/postgres/live_test.go`) run
against a real server and skip as a group where none answers; set
`SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER` to an admin connection string with
the right to create databases (defaults: `postgres://postgres:postgres@localhost:5432/postgres`;
`sqlserver://localhost:1433?database=master&encrypt=disable` with integrated
authentication). Each test creates and drops its own `simpleorm_test_<hex>` database.

### Migration tooling beyond SQLite (Level 4, ADR-0043)

`snapshot`, `shadow`, `diff` (and `--amend`), the derived rollbacks, and `migrate --force` work on SQL Server and
PostgreSQL as on SQLite. What a tree author needs to know:

- **A tree belongs to one dialect.** Its table steps hold literal column specs in that dialect's text (the sample's
  `STRICT` creates are SQLite's), its checksums are that dialect's renderings, and every non-SQLite snapshot
  carries a `"dialect": "<name>"` stamp right after `"object"` (snapshot format v3; an unstamped file is SQLite's, so a
  SQLite tree is untouched). A snapshot stamped for another dialect is `MIG-014` — in the derived rollback (the
  whole range, before anything executes), the `diff` baseline (`--amend` included), and the `shadow --from`
  baseline.
- **Storage facets.** The tag says a size the dialect's default type does not carry:
  `orm:"column,length=50"` (string or bytes), `orm:"column,precision=18,scale=2"` (decimal; a scale left out is 0).
  SQL Server renders `nvarchar(50)` / `decimal(18,2)`, PostgreSQL `character varying(50)` / `numeric(18,2)`,
  SQLite ignores them. Without them a real tree's `decimal(18,2)` would read as a type change against the
  model's `decimal(38,9)` on every `diff`. `MAP-032` for a misplaced facet (length off a string/bytes column,
  precision/scale off a decimal, a scale without a precision or above it, a non-positive value, any facet on a
  view/statement/procedure, a facet beside `ignore`, a navigation, or no `column`); a malformed number stays
  `MAP-023`. The builder has `Length(n)`, `Precision(p)`, `Scale(s)` under the same rule.
- **`StorageType(map, property)`** is spelled exactly as introspection reports it, so snapshot-versus-shadow, diff,
  and force sync are string equalities: SQL Server's `decimal(38,9)` (no space) and `nvarchar(450)` for a key
  column *or a column of a declared index*, PostgreSQL's `format_type` forms.
- **Views and triggers are dialect-neutral.** A view snapshot is `create [materialized] view <name> as <body>`
  with `or alter`/`or replace`/`if not exists` and the name's quoting stripped; the rollback renders it again
  through `CreateViewFromBodySQL`/`DropViewSQL`. A trigger snapshot is its neutral declaration, as before.
- **The derived rollback's order**: renames, the type/nullability refusal (`MIG-020`), the indexes the version
  added dropped *first* (SQLite and SQL Server refuse to drop a column an index covers), restored columns added,
  added columns dropped, the previous indexes recreated. SQL Server's `DropColumnSQL` is one batch that drops a
  leftover default constraint first.
- **The `MIG-012` view guard has a mode per dialect**: text comparison where the engine stores the view as
  submitted (SQLite, SQL Server — `ViewDefinitionIsVerbatim`), and on PostgreSQL (which reformats the parse tree)
  a probe: under a savepoint the expected body is created as `simpleorm_expect_<hex>`, both definitions are read
  through `ViewDefinitionSQL`, the probe is dropped and the savepoint released; a body that no longer parses is
  drift, not a failed run.
- **`shadow` runs everywhere.** The throwaway database comes from the dialect's `ShadowHost` (SQLite a temp file;
  SQL Server and PostgreSQL a `simpleorm_shadow_<hex>` database created and dropped on the `--db` admin
  connection, failure included). Tables are introspected through `ColumnsInfoSQL` (five columns now, `generated`
  last) and `IndexesInfoSQL`; a view is the step's last create statement (a later drop ends the history; a step
  with no create falls back to the catalog only where it is verbatim, else it is skipped with a note); a trigger is
  the declaration the step's actions leave behind. Where the catalog is lossless (SQLite, PostgreSQL triggers;
  SQLite and SQL Server views) it is read too and a disagreement is *noted*, never a different file.
  `sqlite.Shadow` is the SQLite spelling; `migrations.Shadow` takes the dialect and host.

Live: `orm/internal/conformance` replays `conformance/migrations-cases/{postgres,sqlserver}/` and a short tree
(table + unique index, view, add-column, trigger) through `shadow` on each dialect, comparing the history-side snapshots to
the metadata-side export; they skip where no server answers.

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

### Triggers in the mapping (Level 4, ADR-0042)

A trigger is its own declaration type — an empty struct whose `Trigger` method returns the declaration —
and the entity links it from its descriptor. Nothing in the declaration or in the body names a dialect:

```go
type RoleAuditTrigger struct{}

func (RoleAuditTrigger) Trigger() orm.TriggerDef {
    return orm.TriggerDef{
        Name: "trg_roles_audit", Timing: orm.TimingAfter, Events: orm.EventUpdate,   // events combine: EventUpdate | EventDelete
        SQL: `insert into role_changes (role_id, role_name, changed_at)
select n.id, n.role_name, {now}
from {new: id, role_name} n;`,
    }
}

func (Role) Entity() orm.EntityDef {
    return orm.EntityDef{Source: orm.Table("roles"), Triggers: []orm.TriggerLink{orm.Trigger[RoleAuditTrigger]()}}
}
```

The body is set-based SQL over the changed rows with three tokens: `{new: cols}` and `{old: cols}` (the inserted or
updated rows, the deleted or pre-update rows, as a relation) and `{now}` (UTC now in the storage form). The
dialect renders everything else — SQLite and PostgreSQL row triggers, SQL Server's statement trigger over
`inserted`/`deleted` — and refuses a form it lacks with `DDL-005` (`before` on SQL Server, `instead of` on a
table outside SQL Server). `UpdateOf` names properties and narrows an update trigger. Every misplaced or
malformed declaration is `MAP-029`, collected with the entity's other errors; list the classes in
`orm.Registry{Triggers: …}` so the scan rule (a class linked twice, a name declared twice) and the generator see them.

Migrations create triggers as their own object kind, `Trigger/<Class>/`:

```go
// migrations/Trigger/RoleAuditTrigger/V0013_CreateRoleAuditTrigger.go
type V0013_CreateRoleAuditTrigger struct{ orm.TriggerMigration[models.Role, models.RoleAuditTrigger] }

func (V0013_CreateRoleAuditTrigger) Action(actions *orm.TriggerActions) { actions.CreateTrigger() }
// literal forms (what `diff` emits): actions.Recreate(orm.TimingAfter, orm.EventUpdate, `…`, "col"…), actions.ExpectDefinition(…), actions.DropTrigger()
```

A trigger snapshot is its neutral declaration (`kind: "trigger"`), so one tree serves every dialect; `diff`
compares declarations, `shadow` takes it from the step (and, on SQLite and PostgreSQL, parses the stored trigger back as a check), a rollback is derived (guard,
drop, the previous declaration re-rendered), and a trigger changed outside the code refuses with `MIG-012`
(`--force` recreates it with a notice). `orm.CreateTriggers[T](ctx, db)` is the dev/test utility beside
`CreateTable` (replace semantics). A `--` line comment in a body is `MAP-029` (layout
collapses in snapshots and would fold the rest of the body into it); use `/* */`.

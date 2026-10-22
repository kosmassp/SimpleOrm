# Migrations

Migrations are code, organized per object and recorded per version. Applying
them is always an explicit act: the application never migrates at startup, it
only checks (`MIG-030`). The rules are in
[`spec/migrations.md`](../../spec/migrations.md).

## Layout

```
Migrations/
  V0001.cs                          root version: the recorded, checksummed unit
  V0002.cs
  Table/User/V0002_AddDisplayName.cs      per-object steps
  Table/User/V0002.schema.json            the object's snapshot at that version
  View/UserTransactionTotal/V0006_AddLastTransactionAt.cs
  Trigger/RoleAuditTrigger/V0013_CreateRoleAudit.cs
```

A root version `V<n>` composes its object steps in explicit order (tables in
FK order, views after, triggers last). An object step `V<n>_<Description>`
carries the root's version. Names are checked: malformed `MIG-001`, duplicate
versions or an object composed twice `MIG-002`, a step whose version differs
from its root `MIG-003`, a step no root composes `MIG-004`.

```csharp
public sealed class V0001 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.User.V0001_CreateUsers>()
        .Apply<Table.Role.V0001_CreateRoles>()
        .Apply<Table.UserRole.V0001_CreateUserRoles>()
        .Apply<Table.Transaction.V0001_CreateTransactions>()
        .Apply<Table.TransactionDetail.V0001_CreateTransactionDetails>()
        .Apply<View.UserTransactionTotal.V0001_CreateTotalsView>();
}

public sealed class V0002 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.User.V0002_AddDisplayName>();
}
```

## Table steps and actions

Whatever the declaration order, table actions execute rename, then add, then
remove, then raw SQL. Every action takes optional `Pre(sql)` and `Post(sql)`
data hooks that share the version's atomicity. Column specs are literal and
frozen: an applied step's rendered SQL never changes when the model evolves.

```csharp
public sealed class V0002_AddDisplayName : TableMigration<Models.User>
{
    public override void Action(TableActions actions) => actions
        .AddColumn("display_name", "TEXT")
        .Post("update users set display_name = name");
}

public sealed class V0004_RenameNameToRoleName : TableMigration<Models.Role>
{
    public override void Action(TableActions actions) => actions.RenameColumn("name", "role_name");
}

public sealed class V0011_AddIsDeleted : TableMigration<global::SimpleOrm.Sample.Models.Role>
{
    public override void Action(TableActions actions)
    {
        actions.AddColumn("is_deleted", "INTEGER", nullable: false, defaultSql: "0");
    }
}
```

The actions: `CreateTable()` (from metadata, legal only for the initial
create), `DropTable()`, `RenameTable(fromName)`, `RenameColumn(from, to)`,
`AddColumn(name, type, nullable = true, defaultSql = null)`, `RemoveColumn(name)`,
`DropIndex(name)`, `Sql(sql)`. The typed actions render through the dialect
(SQLite says `rename column`, SQL Server `exec sp_rename`), so one tree serves
every dialect while each database's checksums stay its own.

A data-only step is the same mechanism:

```csharp
public sealed class V0005_SeedUserRole : TableMigration<Models.Role>
{
    public override void Action(TableActions actions) => actions
        .Sql("insert into roles (role_name, created_at) values ('user', '2026-08-29T00:00:00.0000000Z')");

    public override void PreDown(MigrationSql sql) => sql
        .Sql("delete from roles where role_name = 'user'");
}
```

View steps run in declaration order: `CreateView()`, `DropView()`,
`RecreateView()` (the definition lives in the attribute, so a view change is
"recreate at this version"), `ExpectDefinition(ddl)`, `Sql(sql)`. Trigger steps
are on [Triggers](10-triggers.md).

In PHP a step names its entity through a method instead of a generic argument:

```php
final class V0002_AddDisplayName extends TableMigration
{
    public function entityClass(): string { return User::class; }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('display_name', 'TEXT')->post('update users set display_name = name');
    }
}
```

In Go the step embeds a generic marker and each object directory is a package:

```go
type V0002_AddDisplayName struct{ orm.TableMigration[sample.User] }

func (V0002_AddDisplayName) Action(actions *orm.TableActions) {
    actions.AddColumn("display_name", "TEXT").Post("update users set display_name = name")
}
```

## The version table, checksums, and the run lock

The runner records one row per (version, object) in `schema_version`:

```
version INTEGER, object TEXT, description TEXT, checksum TEXT,
applied_at TEXT (ISO-8601 UTC), execution_ms INTEGER, primary key (version, object)
```

The checksum is SHA-256 over the step's rendered Up statements. Every run
validates the whole plan before executing anything: drift on an applied step is
`MIG-010`, history rows unknown to the code are `MIG-011`, a pending `[Draft]`
is `MIG-013`. A statement that fails mid-run is `MIG-021`, naming the SQL.

The dialect provides the run lock. On SQLite the entire run is one
`BEGIN IMMEDIATE` transaction; on SQL Server one transaction holding
`sp_getapplock`; on PostgreSQL one transaction holding `pg_advisory_xact_lock`
under a 60s `lock_timeout`. DDL is transactional on all three, so a failed run
applies nothing.

`baseline --version N` records versions at or below N as applied without
running them, for a database created another way.

## Derived rollbacks

Nobody writes `Down()`. At `migrate down` the runner derives each rollback
from the versioned snapshots (`V000N.schema.json`): typed `RenameColumn`
actions invert data-preservingly, removed columns come back (a NOT NULL restore
lands nullable with a notice), added columns drop, indexes revert, a view's
previous definition is restored, a trigger's previous declaration is rendered.
A same-column type or nullability change is not derivable; no snapshot and no
override refuses with `MIG-020`.

Embed the snapshots so rollbacks work deployed:

```xml
<EmbeddedResource Include="Migrations\**\*.schema.json" />
```

`Down()` stays available as a manual override and always wins; `PreDown` and
`PostDown` hooks carry the data work (a seed step deletes its rows in `PreDown`,
as `V0005_SeedUserRole` above). In Go the snapshots are an `embed.FS`
(`//go:embed Table/*/*.schema.json View/*/*.schema.json Trigger/*/*.schema.json`);
in PHP they are files in the migrations directory.

## Snapshots

A snapshot records an object's shape at a version: tables by columns (dialect
storage types, name-sorted columns and indexes, `key` and `generated` flags),
views by normalized DDL, triggers by their neutral declaration.

```json
{
  "object": "roles",
  "asOfVersion": 11,
  "generatedAt": "2026-10-16T13:36:53.6818911Z",
  "columns": [
    { "column": "created_at", "type": "TEXT", "nullable": false },
    { "column": "id", "type": "INTEGER", "nullable": false, "key": true, "generated": true },
    { "column": "is_deleted", "type": "INTEGER", "nullable": false },
    { "column": "role_name", "type": "TEXT", "nullable": false },
    { "column": "updated_at", "type": "TEXT", "nullable": true }
  ],
  "indexes": [
    { "name": "ix_roles_role_name", "columns": [ { "column": "role_name", "direction": "asc" } ], "unique": true }
  ]
}
```

Every snapshot is producible two ways and the two must agree byte for byte
apart from `generatedAt`: `snapshot` renders the current model at the last
version touching each object; `shadow` replays the migrations into a throwaway
database and introspects after each version. That equality is the integrity
check between the model and the history.

## Generating migrations

`diff` compares the model with the latest committed snapshots (no database
needed) and emits the next version as ordinary source with literal SQL.

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- diff --assembly App.dll --out App/Migrations --namespace App.Migrations --name AddNote
```

- Tables diff by columns, views by normalized DDL, triggers by declaration.
- Indexes match structurally (unique flag plus ordered columns and directions),
  never by name, so an index a DBA added under another name counts as implemented.
- Renames are declared, never inferred: `--rename users.name=full_name`, or
  `[RenamedFrom("name")]` on the property (below).
- Removals need `--allow-remove` (`DDL-003`).
- A type or nullability change and a NOT NULL addition are `DDL-004`: write
  those by hand with `AddColumn(name, type, nullable: false, defaultSql: ...)`.
- An object with migration history but no snapshot refuses rather than being
  created anew: run `shadow` or `snapshot` first.

### `[RenamedFrom]`

A declared rename stays with the model instead of being retyped on every run.
It is permanent and harmless once applied: when the latest snapshot already has
the new column, or has neither, it declares nothing. `MAP-027` where it cannot
apply (no `[Column]`, an owned member, a view, a blank name, two properties
naming one previous column).

```csharp
[Column("role_name")]
[RenamedFrom("name")]
public required string Name { get; set; }
```

In PHP: `#[RenamedFrom('name')]`. In Go: `orm:"column=role_name,renamed_from=name"`.

### `diff --draft` and `[Draft]`

`diff --draft` stamps the generated root `[Draft]`. `migrate` and `baseline`
refuse the whole plan while the marker is there (`MIG-013`, before anything
executes), `status` reports `Draft`, SchemaGuard counts it as pending
(`MIG-030`), `--amend` keeps the marker, and `shadow` replays it. Review the
generated code, remove the attribute, then migrate. In PHP the marker is
`#[\SimpleOrm\Migrations\Draft]`; in Go a method `func (V0012) IsDraft() bool { return true }`.

### `diff --amend`

While a version is still being shaped, `--amend` regenerates the newest version
in place instead of stacking a new one: the baseline is the snapshot history
below it, the previously generated files are replaced, and an empty diff
removes the version. A hand-written version is replaced only with `--force`
(its raw SQL, hooks, and data steps cannot be computed from a diff; the run
names each such file to re-add). The tool never looks at git; the boundary is
the databases: one that applied the old draft reports `MIG-010` on its next
`migrate`, so `migrate down --to N-1` there first. Add `--db` to get that
warning up front.

## The view and trigger guard

Views get patched directly in the database during urgencies. A generated view
change step opens with `ExpectDefinition(<previous ddl>)`: when the live
definition does not match (changed outside the code, or absent), the run
refuses with `MIG-012` and, being one transaction, applies nothing; the hotfix
survives for review. `migrate --force` recreates the view from the code and
prints what drifted. Triggers reuse the same guard with a declaration-shaped
`ExpectDefinition`.

## `migrate --force`

After migrations run, `--force` also syncs any remaining live-schema gap to
the model: additive fixes apply, deletions only with `--allow-delete`
(`DDL-003`), anything inexpressible is reported as `DDL-004` and never guessed.
Views and triggers are never synced; for them `--force` is only the guard
override. There is never automatic sync against shared databases.

## The everyday loop

1. Change the model.
2. `diff` (add `--draft` if a reviewer should gate it) and review the generated
   `V000N` and its steps.
3. Build, then `migrate` against your development database.
4. `snapshot --out App/Migrations` to record the new shape, and commit the
   snapshots with the code.
5. Deploy; `migrate` there; the application's startup `SchemaGuard` confirms
   `MIG-030` is clear.

## The CLI

Every command takes `--assembly <dll>`; the database commands also `--db`
(a connection string, or a bare path to a SQLite file); `--namespace` filters
the migrations namespace (one per database in multi-database apps);
`--dialect sqlite|sqlserver|postgres` (default `sqlite`).

| Command | Flags | Does |
|---|---|---|
| `migrate` | `[--force [--allow-delete]]` | apply pending versions; `--force` overrides the `MIG-012` guard and syncs the live schema |
| `migrate down` | `--to <N> [--snapshots <dir>] [--force]` | revert versions above N from the snapshots (embedded, or read from `--snapshots`) |
| `status` | | one line per (version, object): `Applied`, `Pending`, or `Draft` |
| `baseline` | `--version <N>` | record versions at or below N without running them |
| `validate` | | SchemaGuard: the full report, or exit 0 |
| `export-metadata` | `[--out dir]` | each entity's `EntityMap` JSON |
| `snapshot` | `--out <dir>` | write `V000N.schema.json` per object from the model |
| `diff` | `--out <dir> --namespace <ns> [--name <Description>] [--rename table.old=new]... [--allow-remove] [--draft] [--amend [--force] [--db <value>]]` | generate the next version |
| `shadow` | `--out <dir> [--from V000N] [--to V000M] [--dialect <name> --db <admin>]` | rebuild snapshots by replay; `--from` trusts N and regenerates only (N, M]; on sqlserver/postgres `--db` is the admin connection |

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- migrate --assembly App.dll --db app.db --namespace App.Migrations
dotnet run --project dotnet/src/SimpleOrm.Cli -- shadow --assembly App.dll --out App/Migrations --from V0007 --to V0009
```

From code, the runner is `new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations")`
with `MigrateAsync(ct)`, `MigrateDownAsync(targetVersion, ct)`, `BaselineAsync(version, ct)`,
`StatusAsync(ct)`, and `HasPendingAsync(ct)`.

In PHP there is no assembly: `php bin/simpleorm migrate --src src --namespace 'App' --db app.db`,
with migrations defaulting to `<src>/Migrations` under `<Ns>\Migrations`
(`--migrations`/`--migrations-namespace` override). In Go the CLI is a library
the application's `main` embeds, `cli.Run(ctx, registry, os.Args[1:], os.Stdout, os.Stderr)`,
and `simpleorm migrate --db app.db` takes no assembly because the registry is
the application.

## The tooling on SQL Server and PostgreSQL (ADR-0043)

`snapshot`, `shadow`, `diff` (and `--amend`), the derived rollbacks, and the
`migrate --force` sync work on SQL Server and PostgreSQL as on SQLite, with
`--dialect`. Three things to know:

- **A tree and its snapshots belong to one dialect.** Table steps hold literal
  column specs in one dialect's text, so a tree runs on one engine, and its
  snapshots are that engine's: every non-SQLite `V000N.schema.json` carries
  `"dialect": "sqlserver"` or `"postgres"` (an unstamped file is SQLite's,
  so existing trees never changed), and a snapshot of another dialect refuses
  with `MIG-014`.
- **`shadow` needs an admin connection there.** On SQL Server and PostgreSQL
  `--db` is the admin connection string: the command creates
  `simpleorm_shadow_<hex>` on it, replays into it, and drops it afterwards.
  Tables are introspected; views and triggers are recorded from the replayed
  step itself (PostgreSQL reformats a view's text, SQL Server's `inserted`
  loses a token's column list), and where the catalog is lossless the shadow
  reads it too and notes a disagreement.
- **Declare real sizes on the model, not in migrations only.** The dialect's
  defaults (`nvarchar(max)`, `decimal(38,9)`) would make a real tree's
  `decimal(18,2)` a `DDL-004` on every `diff`; the storage facets
  `[Column(Length = 50)]` and `[Column(Precision = 18, Scale = 2)]` (PHP
  `#[Column(length: 50)]`, Go `orm:"column,length=50"`) render as each
  dialect spells them and SQLite ignores them. `MAP-032` for a misplaced one.

The `MIG-012` view guard compares normalized text where the engine stores a
definition as submitted (SQLite, SQL Server) and round-trips the expected body
through a probe view on PostgreSQL. One cost: SQL Server's drop-column
rendering now drops a leftover default constraint first, so a SQL Server
database that applied a `RemoveColumn` step before ADR-0043 reports `MIG-010`
on it.

## Pitfalls

- `MIG-010`: an applied step's rendered SQL changed. Never edit an applied
  migration; add a new version (or `migrate down` first on a development database).
- `MIG-011`: the database knows a version the code does not. You are running
  older code against a newer database, or someone else's branch migrated it.
- `MIG-012`: a view or trigger was changed by hand in the database. Review the
  hotfix, fold it into the code, then `migrate --force` if the code should win.
- `MIG-013`: a `[Draft]` root is pending. Review it and remove the attribute.
- `MIG-020`: `migrate down` needs a snapshot that does not exist, or the change
  is a type/nullability change. Run `shadow` or `snapshot`, or write `Down()`.
- `MIG-030`: SchemaGuard found pending migrations. The app never migrates
  itself; run the CLI.
- `DDL-003` / `DDL-004`: `diff` or the sync refuses a removal without its flag,
  or a change it cannot express. Add the flag, or write the step by hand.
- `MIG-001`..`MIG-004`: naming and composition errors in the tree. The message
  names the class.
- Forgetting the `EmbeddedResource` line: rollbacks work from source but refuse
  deployed (`MIG-020`).

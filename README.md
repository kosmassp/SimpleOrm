# SimpleOrm

A SQL-first micro-ORM for .NET, SQLite first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects
(PostgreSQL, MySQL, SQL Server).

**Status: Levels 0–3 complete in C#, PHP, and Go; Level 4 in progress.** New here? Start
with the **[developer guide](docs/guide/README.md)** — getting started, mapping, queries,
CRUD, criteria, relationships, tracking, hooks, migrations, triggers, SchemaGuard, dialects,
errors, testing, and the PHP and Go shapes. See [CLAUDE.md](CLAUDE.md) for the full project
brief and [docs/decisions.md](docs/decisions.md) for the decision log.

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
await db.UpdateOnlyAsync(user, [nameof(User.Name)], ct);   // only the listed properties (ADR-0028); same key/version rules
await db.DeleteAsync<User>(user.Id, ct);

// Tracking (Level 3 M1, ADR-0034): a session mode, untracked by default — opt in at open
await using var tracked = await Db.OpenAsync("Data Source=app.db",
    new DbOptions { Dialect = new SqliteDialect(), Tracking = TrackingMode.Tracked }, ct);
var a = await tracked.GetAsync<User>(7, ct);
var b = await tracked.GetAsync<User>(7, ct);          // the same instance: one per (type, key) per session
a.Name = "Ada Lovelace";
tracked.ChangesOf(a);                                 // ["Name"] — against the snapshot taken when read
await tracked.UpdateAsync(a, ct);                     // writes only name (column-list path); a clean instance writes nothing
await tracked.RefreshAsync(a, ct);                    // the explicit overwrite from the row; a re-read never overwrites
// UOW-001: a tracking question about an instance the session does not track; UOW-002: a write from an
// instance that is not the session's instance for its key (another holds it, or the key was changed)

// Unit of work (Level 3 M2, ADR-0036; tracked sessions only — UOW-003 otherwise)
tracked.Persist(order);                               // schedules the insert; [Cascade(Persist)] navigations come along
tracked.Remove(oldDetail);                            // schedules the delete of a tracked instance (cascades declared Remove)
var summary = await tracked.FlushAsync(ct);           // inserts in FK order (generated keys flow into dependents),
                                                      // then every dirty tracked instance, then deletes children-first —
                                                      // in the session transaction, or one the flush opens and commits
// UOW-004: a cycle among scheduled instances; UOW-005: a remove cascading over a never-loaded collection

// Statement batching (Level 4, ADR-0044): Off by default; Auto sends the statements one call holds —
// a flush, a batch send — in as few messages as the plan allows. No timer, nothing deferred past the call.
await using var batched = await Db.OpenAsync(connectionString,
    new DbOptions { Dialect = new PostgresDialect(), Batching = BatchingMode.Auto, MaxStatementsPerMessage = 100 }, ct);
await using var batch = batched.Batch();             // queuing does no I/O
batch.Insert(order);
batch.UpdateOnly(user, [nameof(User.Name)]);
batch.Execute(Commands.MarkShipped, new MarkShippedArgs(Id: 7));
var result = await batch.SendAsync(ct);              // one transaction; result.Affected per operation, result.Messages

// The statement log (Level 4, ADR-0045): one line per statement as it is sent, to a function you choose;
// unset (the default) nothing is written. Parameter values stay out unless LogParameterValues is on.
await using var logged = await Db.OpenAsync(connectionString,
    new DbOptions { Dialect = new SqliteDialect(), Log = DbLog.Console /* or line => logger.LogDebug(line) */ }, ct);
// User.get: select "id", "name", ... from "users" where "id" = @k0 [@k0]

// Resilience (Level 4, ADR-0047): both off by default. CommandTimeout reaches every statement the session sends;
// OpenRetries retries only the connection open, only on a failure the dialect calls transient, a fixed delay apart.
await using var remote = await Db.OpenAsync(connectionString, new DbOptions
    { Dialect = new SqlServerDialect(), CommandTimeout = TimeSpan.FromMinutes(5), OpenRetries = 3, OpenRetryDelay = TimeSpan.FromSeconds(2) }, ct);
// batched.BatchingInEffect: one round trip per message (SqlBatch / NpgsqlBatch on net10.0); SQLite runs the plan
// statement by statement, reusing prepared commands. TX-003: a batch used after it was sent or disposed
// (nothing loads implicitly); UOW-006: Persist of an instance scheduled for removal

// Detach / merge (Level 3 M3): the session forgets an instance, or takes a detached copy's values back
tracked.Detach(order);                                // no longer the session's instance; scheduled work cancelled
var managed = await tracked.MergeAsync(detachedCopy, ct);   // the tracked instance (or the row read now) takes the copy's
                                                      // values, version included — a stale copy still fails CRUD-010

// Declared hooks (Level 3 M3, spec/hooks.md) — metadata the session acts on, never callbacks
// [CreatedAt]  filled with DbOptions.Clock on insert when unset (a caller's value is kept)
// [UpdatedAt]  stamped on every write that changes a row (joins a column-list SET; never on a clean update)
// [SoftDelete] a delete becomes the update that sets the flag; reads never filter on it — write the criteria yourself
var live = await db.Query<Role>().Where(Criteria.Eq(nameof(Role.IsDeleted), false)).ToListAsync(ct);

// Owned value types (ADR-0030): [Owned] class + [Owned] navigation → address_* columns of user_profiles,
// one flat EntityMap, dotted paths in criteria and column lists; an all-NULL row reads back as null
var inParis = await db.Query<UserProfile>().Where(Criteria.Eq("Address.City", "Paris")).ToListAsync(ct);
await db.UpdateOnlyAsync(profile, ["Address"], ct);        // the navigation name stands for all its members

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
reading an unloaded navigation never fires SQL. Unloaded is not empty: a
database-read entity's collection navigations **throw `REL-004`** until loaded
(the FKs prove children may exist — forgetting to load is a bug, not an empty
list); singular navigations stay null until loaded, and a dead link (FK with no
target row) loads as null:

```csharp
await db.LoadAsync(tx, nameof(Transaction.User), ct);          // one entity, one query
await db.LoadEachAsync(users, nameof(User.Transactions), ct);  // N entities, still one query
await db.LoadEachAsync(users, nameof(User.Roles), ct);         // many-to-many: two (link, targets)

var page = await db.Query<User>()                              // eager (ADR-0022): loads with the query;
    .Include(nameof(User.Transactions), nameof(User.Profile))  // Fetch picks the strategy —
    .Fetch(FetchMode.SubSelect)                                // MultiQuery (default) | SubSelect | Join —
    .OrderBy("Id").Limit(20).ToListAsync(ct);                  // identical graphs, different trade-offs
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

### Variants (no inheritance mapping, ADR-0041)

SimpleOrm maps **concrete classes only**. A base class shares members — the
sample's abstract `BaseModel` gives every table its audit columns, each table
carrying its own copy — and nothing else: it has no relation, no key space, no
identity, and reading, writing, querying, or exporting through it throws
`MAP-028`. Declare base classes `abstract`; a base class carrying `[Table]` (or
any other relation source) is refused with both class names, and a concrete
sourceless class that another mapped class extends is caught at startup by
`SchemaGuard.ValidateAsync`. Single-table and table-per-type inheritance are
ruled out permanently (a root-typed navigation is the polymorphic association
ADR-0019 forbids; two of the target languages have no subclassing). Rows of
several kinds in one table are modelled with what already exists:

**Owned variants with a kind column.** One table, a `Kind` enum, one nullable
`[Owned]` navigation per variant (ADR-0030): a variant's columns sit under a
prefix, an all-NULL group reads back as null, the caller switches on `Kind`, and
the variant filter is ordinary criteria.

```csharp
[Table("documents")]
public sealed class Document : BaseModel
{
    [Key] [Generated] [Column] public long Id { get; set; }
    [Column] public DocumentKind Kind { get; set; }          // Invoice | CreditNote — TEXT by name
    [Column] public string Number { get; set; } = "";
    [Owned] public InvoiceData? Invoice { get; set; }         // invoice_due_date, invoice_total  (NULL for a credit note)
    [Owned] public CreditNoteData? CreditNote { get; set; }   // credit_note_reason, credit_note_invoice_id
}

var invoices = await db.Query<Document>()
    .Where(Criteria.Eq(nameof(Document.Kind), DocumentKind.Invoice))
    .ToListAsync(ct);
foreach (var d in invoices) { var due = d.Invoice!.DueDate; }   // the owned group is present for its kind
```

**A write model plus read models.** One table-backed entity owns the writes; one
keyed `[View]` or `[Statement]` per variant (ADR-0008) is its SQL-first,
read-only projection — trackable, with its own navigations, validated by
SchemaGuard like any other source.

```csharp
[View("invoices", """
    select id, number, invoice_due_date as due_date, invoice_total as total
    from documents where kind = 'Invoice'
    """)]
public sealed class Invoice            // not a BaseModel: the audit hooks belong to the table that is written (MAP-026 on a view)
{
    [Key] [Column] public long Id { get; set; }
    [Column] public string Number { get; set; } = "";
    [Column] public DateOnly DueDate { get; set; }
    [Column] public decimal Total { get; set; }
}

var overdue = await db.Query<Invoice>().Where(Criteria.Lt(nameof(Invoice.DueDate), today)).ToListAsync(ct);
await db.InsertAsync(new Document { Kind = DocumentKind.Invoice, Number = "A-7", Invoice = new() { DueDate = due, Total = 12m } }, ct);
```

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

While a version is still being shaped, **`diff --amend`** regenerates it in place
instead of stacking a new one (ADR-0017 add.3): the target is the newest version
in the assembly, the baseline is the snapshot history *below* it (a snapshot you
took of the draft is ignored and removed), and the previously generated files are
replaced — an empty diff removes the version. A hand-written version is replaced
only with `--force`: its raw SQL, hooks, and data steps (seeders) can't be
computed from a diff, so the run names each such file for you to re-add what
still applies; a dialect switch refuses. Whether a version may be amended is your
team's call (unpushed, or agreed) — the tool never looks at git. The boundary is
the databases: one that applied the old draft reports `MIG-010` on its next
`migrate`, so run `migrate down --to N-1` there first or recreate it; add `--db`
to get that warning up front. Re-declare `--rename`s, and re-apply any hand edits
you had made inside the generated files. In both modes, `diff` needs the recorded
past to exist: an object that earlier versions migrate but no snapshot describes
refuses (run `shadow`, or `snapshot` on a migrated database, first) rather than
being created anew:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- diff --amend --assembly App.dll --out App/Migrations --namespace App.Migrations --name AddNote --db app.db
```

Two Level 3 additions keep the intent with the model (ADR-0038). A rename can be
**declared on the property** instead of re-typed on every run: `[RenamedFrom("name")]`
beside `[Column("role_name")]` is read by `diff` exactly like `--rename roles.name=role_name`
(one data-preserving `RenameColumn`, never a drop and an add) and is permanent and
harmless once applied — when the latest snapshot already has the new column, or has
neither, it declares nothing (`MAP-027` where it cannot apply: no `[Column]`, an owned
member, a view). And `diff --draft` stamps the generated root `[Draft]`: `migrate` and
`baseline` refuse the whole plan while it is there (`MIG-013`), `status` reports it as
`Draft`, SchemaGuard counts it as pending (`MIG-030`), `--amend` keeps the marker, and
`shadow` replays it — review the generated code, remove the attribute, then migrate:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- diff --draft --assembly App.dll --out App/Migrations --namespace App.Migrations --name RenameRoleName
```

`shadow` rebuilds the snapshots from history instead — it replays every version
into a throwaway database and records what each one leaves behind (tables by
introspection, views and triggers from the replayed step), which both regenerates
lost files and proves the snapshots match the migrations. When history is too long
to replay, `--from V000N` trusts version N (baseline restored from the committed
snapshots, nothing below N verified) and regenerates only `--to V000M`:

```bash
dotnet run --project dotnet/src/SimpleOrm.Cli -- shadow --assembly App.dll --out App/Migrations --from V0007 --to V0009
```

On SQL Server and PostgreSQL the same commands run with `--dialect` (ADR-0043);
for `shadow`, `--db` is then the admin connection on which the throwaway
`simpleorm_shadow_<hex>` database is created and dropped. A tree and its
snapshots belong to one dialect — a non-SQLite snapshot carries `"dialect"`,
and one of another dialect refuses with `MIG-014`.

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

### Triggers in the mapping (ADR-0042)

A trigger is its own class carrying **one** body, the way a materialized view
carries its SQL; the entity links it. Nothing names a dialect:

```csharp
[Trigger("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update, """
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    """)]
public sealed class RoleAuditTrigger { }

[Table("roles")]
[Trigger(typeof(RoleAuditTrigger))]
public sealed class Role : BaseModel { … }
```

The body is set-based SQL over the changed rows. Three tokens stand for what
differs per database: `{new: cols}` and `{old: cols}` are the changed rows as a
relation (`inserted`/`deleted` on SQL Server, a one-row derived table over
`new.`/`old.` on SQLite and PostgreSQL), `{now}` is UTC now in the storage form.
`set nocount on`, `begin/end`, PostgreSQL's function and `return`, and the
granularity are the dialect's rendering. Timing `Before`/`After`/`InsteadOf`,
events combinable, `UpdateOf = [nameof(Role.Name)]` to narrow an update; a form
a dialect lacks (BEFORE on SQL Server, INSTEAD OF on a table elsewhere) refuses
with `DDL-005` when rendered. Migrations own the trigger as an object kind:

```csharp
// Migrations/Trigger/RoleAuditTrigger/V0013_CreateRoleAudit.cs
public sealed class V0013_CreateRoleAudit : TriggerMigration<Role, RoleAuditTrigger>
{
    public override void Action(TriggerActions actions) => actions.CreateTrigger();   // from the declaration
}
```

`simpleorm diff` emits trigger steps from the literal declaration
(`Recreate(timing, events, sql)`, a change guarded by `ExpectDefinition` of the
previous one — `MIG-012` when the live trigger was changed by hand, `--force`
recreates), `snapshot`/`shadow` record `Trigger/<Class>/V000N.schema.json` by
declaration, and `migrate down` derives the rollback. For dev and test,
`db.CreateTriggersAsync<Role>(ct)` creates the declared triggers from metadata —
separately from `CreateTableAsync`, never implicitly.

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

### Statement log (ADR-0045)

`DbOptions.Log` is an `Action<string>` called once per statement, as it is sent,
with `<name>: <sql> [@a, @b]` — the registry entry's name (`Queries.UserByEmail`,
learned when SchemaGuard enumerates the registry), or `User.insert`,
`Transaction.update`, `User.get`, `User.criteria`, `migrate`, `validate …` — and
the SQL exactly as the driver receives it. `DbLog.Console` writes to stdout;
forwarding to `ILogger` is one lambda; unset, nothing is formatted. Values appear
only with `LogParameterValues = true`. The CLI takes `--log`. The guide:
[docs/guide/16-observability.md](docs/guide/16-observability.md); the rule:
[spec/session.md "Statement log"](spec/session.md).

### Resilience (ADR-0047)

`DbOptions.CommandTimeout` (unset: the driver's default) applies to every command
and batch the session creates, in whole seconds rounded up; a timeout that fires is
the driver's own exception. `OpenRetries` (default 0) and `OpenRetryDelay` (default
1 s, fixed) retry **only the connection open**, only when the dialect's `IsTransient`
says so — SQL Server's transient error numbers, PostgreSQL's SQLSTATE class 08,
53300, 57P03, or a socket/IO/timeout cause; SQLite never. A statement is never
retried. The connect timeout stays in the connection string. The CLI takes
`--command-timeout <seconds>`. The guide: [docs/guide/01-getting-started.md](docs/guide/01-getting-started.md);
the rule: [spec/session.md "Resilience"](spec/session.md).

## SQL Server dialect (ADR-0024)

The first Level 4 dialect, pulled forward for real-world field-testing. Same
code, different dialect:

```csharp
await using var db = await Db.OpenAsync(
    "Server=.;Database=app;Integrated Security=true",
    new DbOptions { Dialect = new SqlServerDialect() }, ct);
```

What differs under the hood — and only under the hood, the API is identical:
identifiers are always bracketed (legacy schemas are full of reserved words),
generated keys come back via `scope_identity()` (trigger-safe), paging renders
`OFFSET/FETCH` (an unordered page gains `order by (select null)`), composite
subquery membership rewrites as a correlated EXISTS (no row-value `IN` in
T-SQL), and the migration run lock is `sp_getapplock` inside one transaction.
The CLI takes `--dialect sqlserver`.

Dates: the §7.9 UTC rule maps to **`datetimeoffset`** — it carries the marker,
reads back as `DateTime` Kind=Utc. A legacy `datetime`/`datetime2` column reads
back **refusing with `VAL-020`** (no marker — same strictness as markerless TEXT
on SQLite); if you know such a column's kind, register an `ITypeHandler<DateTime>`
that declares it. Generated DDL uses `nvarchar(max)` for strings
(`nvarchar(450)` for key and indexed columns) and `decimal(38,9)`; declare the
real sizes on the model with the storage facets, `[Column(Length = 50)]` and
`[Column(Precision = 18, Scale = 2)]`, which every dialect renders its own way
(ADR-0043).

`snapshot`, `shadow`, `diff`, derived rollbacks, and `migrate --force` work here
as on SQLite since ADR-0043 (pinned by `conformance/migrations-cases/sqlserver/`
and a shadow round trip; their live run awaits the owner's engine). Procedures
stay dormant until Level 4. Tests run on SQL Server LocalDB or
`SIMPLEORM_SQLSERVER` when present and skip cleanly when not.

Two requirements the first live run against a real engine taught (ADR-0040 add.2): the
process must **not** run in globalization-invariant mode (`Microsoft.Data.SqlClient`
refuses every connection with "Globalization Invariant Mode is not supported"; the
CLI and the tests set `InvariantGlobalization` to false), and a table whose triggers
lack `SET NOCOUNT ON` inflates the affected-row count the version check reads — see
spec/dialects.md Clarification 22 for the measurement and the pending ruling. The
live tests take the admin connection string from `SIMPLEORM_SQLSERVER` (C#: an
ADO.NET connection string; Go: `sqlserver://user:password@host:1433?database=master`;
PHP: a `sqlsrv:` or `odbc:` DSN such as `odbc:Driver={ODBC Driver 17 for SQL
Server};Server=host,1433;Database=master;UID=…;PWD=…`) and skip as a group without it.

## PostgreSQL dialect (ADR-0025)

The second Level 4 dialect — the one several Level 1 seams were designed for,
and where their promises come due:

```csharp
await using var db = await Db.OpenAsync(
    "Host=localhost;Username=app;Password=…;Database=app",
    new DbOptions { Dialect = new PostgresDialect() }, ct);
```

- **Array parameters, realized** (§7.12): write the SQL natively —
  `where id = any(@Ids)` — and a collection-typed args property binds as **one**
  typed-array parameter. No placeholder expansion, no 2100-parameter ceilings;
  an empty list is a typed empty array and matches no rows, same as everywhere.
- **Dates**: `timestamp with time zone` is the UTC-marked storage; values bind
  natively (Postgres refuses `timestamptz >= text`) and read back
  `DateTime` Kind=Utc. A legacy `timestamp` (no marker) refuses with `VAL-020`;
  register an `ITypeHandler<DateTime>` declaring its kind if you own such
  columns.
- **Materialized views work** — `[MaterializedView]` entities create
  (`create materialized view if not exists`) and read like any relation;
  refresh scheduling stays yours.
- Migrations run inside one transaction holding `pg_advisory_xact_lock` — DDL
  is transactional, so a failed run applies nothing.
- Identifiers are always double-quoted (snake_case matches the catalog fold,
  reserved words stop mattering); generated keys use
  `generated by default as identity … returning`; storage types are the
  `format_type` spellings (`numeric` unconstrained, `text` everywhere strings
  live). The CLI takes `--dialect postgres`.

The migration tooling works here too (ADR-0043): `shadow` provisions its
database on the admin connection, the view guard round-trips through a probe
view because `pg_get_viewdef` reformats definitions, and the trigger read-back
is lossless. Tests run against a local server
(`SIMPLEORM_POSTGRES` connection string, or localhost:5432 by convention) and
skip cleanly without one. On `netstandard2.0` the dialect rides Npgsql 4.1 with
known legacy-temporal caveats — prefer `net10.0` for Postgres.

## PHP port (ADR-0026)

The first port, Levels 0–1, lives in [`php/`](php/README.md): same spec, same
conformance files, no shared code. Same API in PHP shape — `Db::open`, registries
as static methods returning `Query`/`Command`, `$db->from(User::class)->where(…)`,
`get`/`insert`/`update`/`delete`, `SchemaGuard::validate`, code migrations with
the runner, snapshots, `diff --amend`, and the `simpleorm` CLI (`--src`/`--namespace`
in place of an assembly). Its coding standard — `php/CODING-STANDARD.md` — lists
the only permitted divergences (synchronous methods, `Decimal`, `private(set)`
navigations, …). `cd php && composer install && composer test`.

## Go port (ADR-0027)

The second port, Levels 0–1, lives in [`go/`](go/README.md): same spec, same
conformance files, no shared code, one runtime dependency (`modernc.org/sqlite`,
pure Go). Same API in Go shape — struct tags plus a small `Entity()` descriptor,
`orm.Open`, `orm.Query(ctx, db, entry, args)`, `orm.Get[User]`,
`orm.From[User](db).Where(…)`, errors as `*orm.Error` values, `context.Context`
everywhere, explicit `orm.Registry` instead of assembly scanning, `embed.FS`
snapshots, and the CLI as a library the application's `main` embeds. Level 2 is
there too (ADR-0031): `orm.Load`/`orm.LoadEach` and
`orm.From[User](db).Include("Transactions").Fetch(orm.FetchSubSelect)` — built
from the spec and conformance files with the reference off limits, which is how
Level 2 was proven reimplementable. Its coding standard — `go/CODING-STANDARD.md`
— lists the only permitted divergences. `cd go && go vet ./... && go test ./...`.

## Performance

Compiled expression-tree mappers with typed-getter fast paths. BenchmarkDotNet vs
Dapper and raw `Microsoft.Data.Sqlite` (net10.0, `dotnet/benchmarks/`): mapping
1000 rows to entities runs **~5% faster than Dapper with 21% less allocation**
(matching the raw reader's allocation floor); single-row reads are at parity.

Level 2 paths (ADR-0029 refresh, same machine, 1000 rows × 3 child rows):

| Path | SimpleOrm | Hand-written Dapper |
|---|---|---|
| criteria `Where(Eq).OrderBy.Limit(100)` (AST → SQL) | 134 µs | 123 µs (inline SQL) |
| registry query, same SQL | 123 µs | 123 µs |
| one-to-many graph, `Fetch(SubSelect)` | 4.1 ms | 3.8 ms (multi-mapped join, grouped by hand) |
| one-to-many graph, `Fetch(Join)` | 6.4 ms, 2× the allocation | |
| one-to-many graph, `Fetch(MultiQuery)` / `LoadEachAsync` | 7.2 ms (500-key chunks) | 16.0 ms (one 1000-key `IN` list) |

The criteria renderer costs ~9% over the equivalent inline SQL. On SQLite the
parameter-heavy `IN` list is what hurts, so **SubSelect is the fastest mode for
large owner sets** there; Join pays for its segment readers in allocation.
Level 3 paths (ADR-0039, same machine; `TrackingBenchmarks`, SQLite in WAL mode so
the journal does not swamp the library):

| Path | Untracked | Tracked |
|---|---|---|
| 1000-row read, warm session (every row an identity hit) | 751 µs | 822 µs (1.09×, +32% allocation) |
| 1000-row read, fresh session (1000 registrations + snapshots) | 2.2 ms | 3.7 ms (~1.5 µs and ~0.4 KB per row) |
| get + update one row (Dapper by hand: 32 µs) | 33 µs, the full row | 33 µs, the dirty column only; 9.8 µs when clean (no statement) |
| 100 inserts in one transaction (Dapper: 519 µs) | 1.40 ms | `Persist` × 100 + `Flush`: 1.67 ms |
| nothing-to-do flush over 1000 tracked rows | — | ~100 µs and ~30 KB for the dirty sweep |

Tracking costs an identity lookup per row read and a snapshot per row registered;
a flush sweeps every tracked instance (linear in the session's size, as in every
snapshot-tracking ORM), so a session is a unit of work, not a process-wide cache.

Statement batching (ADR-0044; `BatchBenchmarks`, `Off` against `Auto`, same
machine; PostgreSQL 16.4 on loopback, so the round trip saved is the cheapest
there is — over a network the gain grows with latency):

| Path | SQLite `Off` → `Auto` | PostgreSQL `Off` → `Auto` |
|---|---|---|
| `Persist` × 100 + `Flush` (session opened per call) | 2.04 → 1.66 ms (noisy) | 19.5 → 5.1 ms (3.9×) |
| 100 inserts, one explicit batch | 1.65 → 0.73 ms (2.3×) | 19.7 → 4.8 ms (4.1×) |
| 100 registry commands, one explicit batch | 0.78 → 0.34 ms (2.3×) | 20.5 → 5.5 ms (3.8×) |

On SQLite there is no round trip to save: `Auto` reuses one prepared command per
statement text within the call. On PostgreSQL a message goes out as one
`NpgsqlBatch`. Allocation is unchanged within a few percent.

PostgreSQL (`PostgresQueryBenchmarks`, loopback PostgreSQL 16.4, same shapes, Dapper and a raw
Npgsql reader as baselines; 2026-10-26):

| Path | SimpleOrm | Dapper (sync) | Raw Npgsql reader |
|---|---|---|---|
| 1000 rows → entities | 967 µs / 179 KB | 1173 µs / 225 KB | 816 µs / 177 KB |
| single row by key (`GetAsync`) | 165 µs | 220 µs | |
| criteria page, 100 rows | 489 µs | 244 µs | |
| one-to-many graph, `Fetch(SubSelect)` | 5.3 ms / 1.8 MB | 2.8 ms / 0.6 MB (two queries, grouped by hand) | |

Two things the PostgreSQL numbers say. Small result sets cost more than Dapper's synchronous
path because the library is async-only and Npgsql's async reader is dearer per call than its
sync one: against Dapper's own `QueryAsync` the 100-row page is at parity (455 µs vs 434 µs in a
Stopwatch probe) and the 1000-row read is level with both. And the one-to-many graph is about
2× Dapper's grouped lookup on PostgreSQL, where array parameters make Dapper's single `IN` list
cheap; the difference is the attached, identity-resolved collections and their allocation,
which the Level 4 performance pass owns.

Numbers are machine-specific — run `dotnet run -c Release` in the benchmarks
project for yours.

## Layout

```
spec/          language-neutral spec (grows per milestone)
conformance/   executable definition: JSON cases every implementation must pass
dotnet/        C# reference implementation
  src/SimpleOrm/           core (netstandard2.0 + net10.0, depends only on System.Data.Common)
  src/SimpleOrm.Sqlite/    SQLite dialect (Microsoft.Data.Sqlite)
  src/SimpleOrm.SqlServer/ SQL Server dialect (Microsoft.Data.SqlClient, ADR-0024)
  src/SimpleOrm.Postgres/  PostgreSQL dialect (Npgsql, ADR-0025)
  src/SimpleOrm.Cli/       CLI (stub until milestone 5)
  samples/SimpleOrm.Sample/ sample entity models used by tests and conformance
  tests/SimpleOrm.Tests/   integration tests against real SQLite databases
                           (+ SQL Server LocalDB / local PostgreSQL where present;
                           skip cleanly otherwise)
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

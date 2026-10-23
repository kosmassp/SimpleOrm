# SimpleOrm — PHP port

The PHP implementation of the SimpleOrm spec, **Levels 0–3** (ADR-0026 for the
mapper, the micro-ORM with generated CRUD, the ADR-0012 criteria core,
SchemaGuard, and code migrations with the diff generator; ADR-0033 for
relationships: explicit/batch loading, the Level 2 query AST, and eager loading
in all three fetch modes; the Level 4 items so far — the SQL Server and PostgreSQL
dialects, triggers in the mapping, and the migration tooling beyond SQLite: ADR-0040,
ADR-0042, ADR-0043). It
shares no code with the C# reference; it shares `../spec/` and `../conformance/`,
and it is correct when it passes the same conformance files unchanged.

Requirements: PHP 8.4 with `pdo_sqlite` (Laragon ships the extension DLL; the
Composer scripts load it with `-d extension=pdo_sqlite`, so no `php.ini` change
is needed), Composer. The SQL Server and PostgreSQL dialects need `pdo_sqlsrv`
(or `pdo_odbc`) and `pdo_pgsql` respectively — only when you use them; see
"Dialects" below.

```bash
cd php
composer install
composer test
php bin/simpleorm migrate --src src --namespace 'App' --db app.db
```

Layout mirrors the C# areas:

```
src/Errors        SimpleOrmException (code + target), MappingException, SchemaValidationException
src/Metadata      EntityMap and friends; Attributes/; the loaders; JSON export
src/Naming        NamingConvention, SnakeCaseNamingConvention
src/Types         Decimal (string-backed value object)
src/Mapping       TypeConverter, TypeHandler registry, ResultMapper, RowSegment (join-mode row slices),
                  UnloadedNavigations (the REL-004 unset)
src/Parameters    SqlPlaceholders, ParameterBinder (@name → :name, IN-list expansion)
src/Query         Criteria, SelectAst (+ SelectJoin/JoinPair, projection), AnsiSelectRenderer,
                  CriteriaQuery (include/fetch), FetchMode
src/Dialect       Dialect (the seam, same members as C# IDialect), SqliteDialect, SqlServerDialect,
                  PostgresDialect, Dialects (the name → dialect registry behind --dialect), ShadowHost
                  + one per dialect (the throwaway database of `shadow`), the trigger read-back parsers
src/Session       Db, DbOptions, transaction scope, Query/Command registry types, DbLoading and
                  DbEagerJoin (the loading engines), KeyTuple, the Navigations trait
src/Migrations    versions, steps, actions, runner, snapshots, derived downs, generator, sync
src/Validation    SchemaGuard
src/Cli           the simpleorm command
tests/Sample      the fixture entities (mirror dotnet/samples)
tests/Dialect     the SQL Server and PostgreSQL dialect tests; *LiveTest.php are the live legs
tests/Conformance one data-driven runner per conformance folder
tests/Samples     drives samples/SimpleOrm.Sample the way dotnet/tests drive the C# sample
samples/SimpleOrm.Sample
                  a standalone consumer project (own composer.json, path repository to ../..):
                  the sample model, its V0001–V0012 migrations + snapshots, repositories, a
                  registry, and bin/demo.php — see its README
```

How PHP differs from the reference — and only there — is the table in
[CODING-STANDARD.md §10](CODING-STANDARD.md): synchronous methods, `Generator`
streaming, `Decimal` and `DateTimeImmutable` conventions, `private(set)`
navigations, explicit target classes on collection navigations, registries as
static methods, `$db->from(User::class)` for criteria, directory-based migration
discovery. Read the standard before contributing; it is the uniformity contract.

Base classes (ADR-0041): declare a class that only shares members (the sample's `BaseModel`) `abstract` — loading one as an
entity by any path is `MAP-028`, as is extending a class that carries `#[Table]` (or any relation source, or `#[Index]`), and
`SchemaGuard::validate` / the CLI's `export-metadata` refuse a concrete sourceless class that another scanned class extends.

## Relationships (Level 2, ADR-0033)

Nothing loads implicitly (`spec/loading.md`). Navigations are declared
(`#[ManyToOne]`, `#[OneToOne]`, `#[OneToMany(Target::class, 'fk')]`,
`#[ManyToMany(Target::class, through: Link::class)]`, `public private(set)`),
and loaded on request:

```php
$db->load($transaction, 'user');                      // one entity, one navigation
$db->loadEach($users, 'transactions');                // the batch form: one query per navigation (two for many-to-many)

$users = $db->from(User::class)
    ->where(Criteria::like('name', 'A%'))
    ->include('transactions', 'profile')              // eager: REL-001 for an unknown name, before any SQL
    ->fetch(FetchMode::SubSelect)                     // MultiQuery (default) | SubSelect | Join
    ->toList();
```

All three fetch modes load identical graphs; join mode refuses paging with a
collection include (`REL-005`) and more than one collection include (`REL-006`).

**Unloaded is not empty.** The mapper `unset()`s every collection navigation of
an entity read from the database, so reading one before loading it refuses:
with the opt-in trait `SimpleOrm\Session\Navigations` on the entity class the
refusal is `SimpleOrmException('REL-004', …)`, without it PHP's own `Error`
("must not be accessed before initialization") — never a silent `[]`. Loading
re-initializes the property (an empty collection reads as `[]`); entities you
construct yourself keep their `= []` initializers; singular navigations stay
`null` until loaded (no proxies), and a null FK or a dead link loads as `null`.

### Tracking (Level 3 M1, ADR-0034)

Tracking is a session mode chosen at open; the default is untracked — the Level
0–2 session exactly (fresh instances, full-row `update`):

```php
$db = Db::open($dsn, new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked));

$a = $db->get(User::class, 1);
$b = $db->get(User::class, 1);        // $b === $a: one instance per (class, key), every read path
$a->name = 'Ada Lovelace';
$db->changesOf($a);                   // ['name']  (dotted for owned members: 'address.city')
$db->update($a);                      // writes only `name`; a clean instance writes nothing
$db->refresh($a);                     // the explicit overwrite from the row (works untracked too)
```

A re-read of a tracked key returns the tracked instance and does not apply the
row — dirty or not — until `refresh()`. `changesOf()`/`isDirty()` are `UOW-001`
for an instance the session does not track (every instance in an untracked
session); `isTracked()` never throws. A write from an instance that is not the
session's instance for its key — a second copy, or a tracked instance whose key
was changed — is `UOW-002` before any SQL. DTO results, keyless entities, and
rows with a null key part are never tracked.

### Unit of work (Level 3 M2, ADR-0034)

A tracked session can schedule writes and flush them in one explicit step
(`UOW-003` on an untracked session). Nothing is written, and nothing loads, until
`flush()`:

```php
$order->setUser($user);                // however your model assigns the navigation; the FK follows at flush
$db->persist($order);                  // schedules an insert (a tracked instance schedules nothing)
$db->persist($user);
$db->remove($oldLine);                 // schedules a delete of a tracked instance (UOW-001 otherwise)
$summary = $db->flush();               // FlushSummary: ->inserted, ->updated, ->deleted
```

A flush writes inserts in foreign-key order (a parent first; its generated key flows into the
child's FK before the child's insert), then every dirty tracked instance as a column-list
update, then deletes children first — all through the session's own `insert`/`update`/`delete`,
inside the active transaction or one the flush begins (rolled back on failure; the schedule is
kept and the session's keys, tracking, snapshots, and versions are put back, so it flushes again as
it was). A cycle among the scheduled instances is `UOW-004`, before any SQL; persisting an instance
scheduled for removal is `UOW-006`; `hasPendingWork()` tells whether anything is scheduled (false,
never an error, on an untracked session). Cascades
are declared, never inferred: `#[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]` on a
`#[ManyToOne]`/`#[OneToOne]`/`#[OneToMany]` navigation (`MAP-025` anywhere else, or on a
`#[ManyToMany]`). `persist` cascades over initialized navigations only and records the owner key each
cascaded child takes; `remove` cascades over loaded ones and refuses a never-loaded collection
(`UOW-005` — load it first, nothing loads implicitly). `remove` of an instance scheduled for insert
cancels the insert and the inserts its cascade scheduled.

`detach($entity)` makes a tracked session forget an instance (its values stay; a scheduled insert is cancelled
with the inserts its cascade scheduled, a scheduled removal dropped; `UOW-001` for an instance the session neither
tracks nor has scheduled), and `merge($detached)` brings a detached copy back: it returns the session's instance for
the copy's key — the tracked one, else the row read now (`CRUD-001` when there is none; `CRUD-002` for a copy with no
complete key, `CRUD-003` for a view or statement) — with every mapped non-key value of the copy (the version included) written onto it, so it is dirty
where they differed; navigations are not merged and the copy stays untracked. Both are `UOW-003` on an untracked session.

### Hooks (Level 3 M3, ADR-0034)

Declared metadata the session acts on, never callbacks (`spec/hooks.md`). `#[CreatedAt]` and `#[UpdatedAt]` mark
`DateTimeImmutable` columns (each with `#[Column]`, never a key, version, or generated column; at most one of each role per
entity, `MAP-026` otherwise): `insert` fills `#[CreatedAt]` when it is unset (null or uninitialized) and keeps a value you
set; every write that changes a row stamps `#[UpdatedAt]` — a full-row `update`, `updateOnly` (the column joins the SET even
when not listed), a tracked dirty update — but the stamp alone never makes an instance dirty, so a clean instance still
writes nothing. `#[SoftDelete]` marks a non-nullable `bool` column: `delete` then sets the flag (and the stamp) with the
update rules — the entity form is version-checked (`CRUD-010`), the by-key form of a versioned entity is `CRUD-012`, a missing
row `CRUD-001` — and a flushed `remove` does the same. Reads never filter on the flag: filter in a criteria query yourself
(`Criteria::eq('isDeleted', false)`). The instant is `DbOptions` `clock:` — a `Closure` returning a UTC
`DateTimeImmutable` (a non-UTC one is `VAL-020`), the wall clock by default:

```php
new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked, clock: fn () => new DateTimeImmutable('2026-08-29T00:00:00Z'));
```

### Draft migrations (Level 3 M5, ADR-0034)

`#[RenamedFrom('old_column')]` beside `#[Column('new_column')]` declares the column a property was stored under before
(`MAP-027` without `#[Column]`, on an `#[Ignore]`d or navigation property, on an owned type's member, or with a blank
name; the export carries it as `"renamedFrom"` only when declared). `simpleorm diff` reads it exactly like
`--rename table.old=new` — one data-preserving rename instead of a drop and an add — but it stays with the model: once the
latest snapshot has the new column (or neither column) it declares nothing, and an explicit `--rename` of the same old
column wins. `diff --draft` writes the generated root with `#[SimpleOrm\Migrations\Draft]`: `migrate` and `baseline` refuse
a pending draft (`MIG-013`, the whole plan, before anything executes) until you have reviewed the generated code and
removed the attribute; `status` lists it as `draft`, `shadow` replays it, `diff --amend` keeps the marker. A baseline
below the draft is fine; a draft already recorded as applied is history, not pending work.

### Storage facets (Level 4, ADR-0043)

`#[Column(length: 50)]` on a string or bytes column and `#[Column(precision: 18, scale: 2)]` on a decimal declare a size the
dialect's default storage type does not carry — SQL Server `nvarchar(50)` / `varbinary(50)` / `decimal(18,2)`, PostgreSQL
`character varying(50)` / `numeric(18,2)` (`bytea` has no length), SQLite ignores them. A `scale` left out beside a `precision`
is 0; a declared `length` wins over SQL Server's indexable default (`nvarchar(450)` for a key column or a column of a declared
index). The export carries `"length"` or `"precision"` + `"scale"` only when declared; the mapper and the session ignore them,
`Dialect::storageType($map, $property)`, snapshots, `diff`, and force sync read them — without them a real tree's `decimal(18,2)`
would read as a type change against the model's `decimal(38,9)` on every `diff`. A misplaced facet (a length on an int, a
precision on a string, a scale without or above its precision, a non-positive length or precision, any facet on a view) is
`MAP-032` — from the attribute loader and `EntityMapBuilder::column(length:, precision:, scale:)` alike.

### Triggers in the mapping (Level 4, ADR-0042)

A trigger is its own **declaration class**; the entity links it. The declaration names no dialect — the body is
set-based SQL with three tokens (`{new: cols}` the inserted/updated rows as a relation, `{old: cols}` the deleted or
pre-update rows, `{now}` UTC now in the §7.9 storage form) and each dialect renders the rest (header, granularity,
wrapper, expansions): SQLite one row-level trigger per event, SQL Server one statement-level trigger over `inserted`/
`deleted`, PostgreSQL a PL/pgSQL function plus the trigger. Events are an **array of `TriggerEvent` cases**, a set kept
in the fixed order `insert`, `update`, `delete`; `updateOf` names entity **properties**:

```php
#[Trigger('trg_roles_audit', TriggerTiming::After, [TriggerEvent::Update], <<<'SQL'
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    SQL)]
final class RoleAuditTrigger {}

#[Table('roles')]
#[Trigger(RoleAuditTrigger::class)]       // repeatable: a table carries many triggers, a trigger belongs to one table
final class Role { … }
```

`MAP-029` is every misplaced or malformed declaration, collected with the entity's other errors; the scan rule (a trigger
class linked by two entities, two classes declaring one name) runs in `SchemaGuard` and every CLI command that
enumerates entities. A form a dialect lacks is `DDL-005` when rendered (`before` on SQL Server, `instead_of` on a table
elsewhere). The manual builder links with `EntityMapBuilder::for(X::class)->trigger(Y::class)`.

Migrations create them: `TriggerMigration` (`entityClass()` + `triggerClass()`, object folder `Trigger/<Class>/`,
`schema_version` object = the trigger name) with `createTrigger()` (the initial create, from the entity's declaration),
`recreate(timing, events, sql, updateOf)`, `expectDefinition(...)` (the `MIG-012` guard: the live trigger must be exactly the
dialect's rendering, else the run refuses; `--force` recreates with notice) and `dropTrigger()` — never raw SQL. The trigger
snapshot is the neutral declaration (`"kind": "trigger"`), so one tree serves every dialect; `snapshot` writes it from
metadata, `shadow` parses the stored SQLite trigger back (the two agree byte for byte), rollbacks derive from it, and
`diff` emits literal-declaration steps (a removal is a bare `dropTrigger()`, gated by `--allow-remove`). In a dev/test
session `$db->createTriggers(Role::class)` creates an entity's triggers (drop then create, so it is safe to repeat).

### Statement batching (Level 4, ADR-0044)

A **message** is what one round trip sends. By default (`BatchingMode::Off`) every statement is a message of its
own — the Level 0–3 session exactly. With `BatchingMode::Auto` the statements of **one call** — a `flush()` or the
`send()` of an explicit batch — are grouped into as few messages as a fixed plan allows (`spec/session.md`
"Batching"); nothing waits, nothing is deferred past the call that holds the statements, and a lone statement runs as it
always did:

```php
$db = Db::open($dsn, new DbOptions(new SqlServerDialect(), batching: BatchingMode::Auto, maxStatementsPerMessage: 100));

$batch = $db->batch();                         // queuing does no I/O and checks nothing
$batch->insert($order);
$batch->updateOnly($user, ['name']);
$batch->execute(Commands::markShipped(), new MarkShippedArgs(id: 7));
$batch->delete(Role::class, 3);                // the key or the entity form, as $db->delete()
$result = $batch->send();                      // BatchResult: ->affected (one count per operation), ->messages
```

`send()` always runs in a transaction (the session's, else one it begins and commits) and each operation behaves exactly
as its immediate call would at that point of the sequence — the same gates (`CRUD-002`…`CRUD-007`, `UOW-002`,
`CRUD-004`), the same judging (`CRUD-010`/`CRUD-001`), the same effect on the instance and the tracking; keys do **not**
flow between queued operations (the unit of work is the form that flows keys). A batch is spent by its send or
`dispose()`: using it again is `TX-003`. A failed call undoes every in-memory effect of every statement it prepared and
rolls its own transaction back; inside **your** transaction the rows of the statements that ran stay yours to roll back.
Reads are not batched.

`$db->batchingInEffect()` says which realization is in force. Only **SQL Server** sends a planned message as one
round trip in PHP (one multi-statement text with the placeholders renamed apart, its results read with `nextRowset()`; a
registry command always goes on its own). **SQLite** and **PostgreSQL** are *sequential* — `pdo_sqlite` prepares one
statement and `pdo_pgsql` cannot carry per-statement results — so the same plan is formed and each message's statements
run one at a time, **reusing one prepared `PDOStatement` per distinct SQL text** within the call (on SQLite, where
there is no round trip to save, that reuse is the gain). Nothing refuses: one configuration serves every database.

## The CLI

`bin/simpleorm` has no assembly to load, so it takes a PSR-4 root instead:
`--src <dir> --namespace <Ns>` names the directory entities and registries are
discovered under (via `ClassScanner`). Migrations default to `<src>/Migrations`
under `<Ns>\Migrations` — override either with `--migrations`/
`--migrations-namespace` when they live elsewhere. Snapshots default to the
migrations directory (`--snapshots` for `migrate down`; `--out` for
`snapshot`/`diff`/`shadow`). `--db` is the dialect's native connection string — for
SQLite a bare path, a `sqlite:` DSN, or `Data Source=...`; `--dialect
sqlite|sqlserver|postgres` (default `sqlite`) picks the dialect for every command, `shadow`
included: on SQL Server and PostgreSQL `shadow`'s `--db` is the **admin** connection (the right to create databases), on which a
throwaway `simpleorm_shadow_<hex>` database is created and dropped (failure included); on SQLite it is ignored (a temp file).

```bash
php bin/simpleorm migrate --src src --namespace 'App' --db app.db
php bin/simpleorm status --src src --namespace 'App' --db app.db
php bin/simpleorm validate --src src --namespace 'App' --db app.db
php bin/simpleorm snapshot --src src --namespace 'App' --db app.db
php bin/simpleorm diff --src src --namespace 'App' --name AddNote
php bin/simpleorm diff --src src --namespace 'App' --name AddNote --draft
php bin/simpleorm shadow --src src --namespace 'App' --from V0001 --to V0003
```

## Dialects

Three dialects ship in `SimpleOrm\Dialect` (spec/dialects.md): `SqliteDialect`
(the reference rendering), `SqlServerDialect` (ADR-0024) and `PostgresDialect`
(ADR-0025). Each takes its PDO driver's native DSN verbatim:

```php
new DbOptions(new SqliteDialect());      // 'app.db', 'sqlite:app.db', or 'Data Source=app.db'
new DbOptions(new SqlServerDialect());   // 'sqlsrv:Server=host;Database=app;Encrypt=no' or an 'odbc:…' DSN (pdo_sqlsrv / pdo_odbc)
new DbOptions(new PostgresDialect());    // 'pgsql:host=localhost;port=5432;dbname=app;user=app;password=…' (pdo_pgsql)
```

The two non-SQLite dialects run the session, generated CRUD, criteria queries,
loading in every fetch mode, tracking, the unit of work, hooks, SchemaGuard, and the
migration runner with the dialect's run lock (`sp_getapplock` / `pg_advisory_xact_lock`).
Since ADR-0043 the migration tooling works on them as on SQLite: `snapshot`, `shadow`,
`diff` (and `--amend`), derived rollbacks, and `migrate --force`. A tree belongs to one
dialect — its literal SQL, its checksums, and its snapshots, every non-SQLite one stamped
`"dialect": "<name>"` (format v3) and refused by the other dialects' readers with `MIG-014`.
Storage types compare as introspection spells them (`decimal(38,9)`, `nvarchar(450)`,
`format_type`'s spellings); view snapshots are dialect-neutral (`create [materialized] view
<name> as <body>`) and render through the seam when a rollback restores one; the `MIG-012`
view guard compares text where the engine stores it verbatim and round-trips the expected body
through a probe view on PostgreSQL, whose `pg_get_viewdef` reformats. On PostgreSQL a
collection argument binds as one array parameter (`where id = any(:ids)`), and a legacy
`timestamp without time zone` / `datetime2` column refuses `VAL-020` at read time unless a
`TypeHandler` declares it.

`shadow` gets its throwaway database from a `ShadowHost` beside the seam (`Dialects::shadowHost()`:
`SqliteShadowHost` a temp file, `SqlServerShadowHost` / `PostgresShadowHost` a database created
on the admin connection and dropped afterwards). `SchemaShadow::for($dialect, $adminConnection)->rebuildSnapshots(…)`
replays the history; tables are introspected through `columnsInfoSql`/`indexesInfoSql`, views and
triggers are taken from the replayed step (a catalog may not hand them back as declared), and where
the catalog is lossless (SQLite, PostgreSQL triggers; SQL Server views) the shadow reads it too and
notes a disagreement. SQL Server has no trigger read-back.

The live legs (`tests/Dialect/*LiveTest.php`) run against a real server and skip
as a group where none answers: `composer test-live` loads `pdo_pgsql` and
`pdo_odbc`; set `SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER` to an admin DSN with
the right to create databases (defaults: `localhost:5432` as `postgres`/`postgres`;
LocalDB through `pdo_sqlsrv`). Each test creates and drops its own
`simpleorm_test_<hex>` database.

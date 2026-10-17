# SimpleOrm — PHP port

The PHP implementation of the SimpleOrm spec, **Levels 0–3** (ADR-0026 for the
mapper, the micro-ORM with generated CRUD, the ADR-0012 criteria core,
SchemaGuard, and code migrations with the diff generator; ADR-0033 for
relationships: explicit/batch loading, the Level 2 query AST, and eager loading
in all three fetch modes). It
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
                  PostgresDialect, Dialects (the name → dialect registry behind --dialect)
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

## The CLI

`bin/simpleorm` has no assembly to load, so it takes a PSR-4 root instead:
`--src <dir> --namespace <Ns>` names the directory entities and registries are
discovered under (via `ClassScanner`). Migrations default to `<src>/Migrations`
under `<Ns>\Migrations` — override either with `--migrations`/
`--migrations-namespace` when they live elsewhere. Snapshots default to the
migrations directory (`--snapshots` for `migrate down`; `--out` for
`snapshot`/`diff`/`shadow`). `--db` is the dialect's native connection string — for
SQLite a bare path, a `sqlite:` DSN, or `Data Source=...`; `--dialect
sqlite|sqlserver|postgres` (default `sqlite`) picks the dialect for every database
command, and `shadow` accepts SQLite only.

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

The two non-SQLite dialects are **runtime-first**: the session, generated CRUD,
criteria queries, loading in every fetch mode, tracking, the unit of work, hooks,
SchemaGuard, and the migration runner with the dialect's run lock (`sp_getapplock`
/ `pg_advisory_xact_lock`) all work. The snapshot, shadow, diff, and force-sync
tooling stays SQLite-only, so a rollback there needs a `Down()` override
(`MIG-020` says so). On PostgreSQL a collection argument binds as one array
parameter (`where id = any(:ids)`), and a legacy `timestamp without time zone` /
`datetime2` column refuses `VAL-020` at read time unless a `TypeHandler` declares it.

The live legs (`tests/Dialect/*LiveTest.php`) run against a real server and skip
as a group where none answers: `composer test-live` loads `pdo_pgsql` and
`pdo_odbc`; set `SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER` to an admin DSN with
the right to create databases (defaults: `localhost:5432` as `postgres`/`postgres`;
LocalDB through `pdo_sqlsrv`). Each test creates and drops its own
`simpleorm_test_<hex>` database.

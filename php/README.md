# SimpleOrm — PHP port

The PHP implementation of the SimpleOrm spec, **Levels 0–2** (ADR-0026 for the
mapper, the micro-ORM with generated CRUD, the ADR-0012 criteria core,
SchemaGuard, and code migrations with the diff generator; ADR-0033 for
relationships: explicit/batch loading, the Level 2 query AST, and eager loading
in all three fetch modes). It
shares no code with the C# reference; it shares `../spec/` and `../conformance/`,
and it is correct when it passes the same conformance files unchanged.

Requirements: PHP 8.4 with `pdo_sqlite` (Laragon ships the extension DLL; the
Composer scripts load it with `-d extension=pdo_sqlite`, so no `php.ini` change
is needed), Composer.

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
src/Dialect       Dialect (the seam, same members as C# IDialect), SqliteDialect
src/Session       Db, DbOptions, transaction scope, Query/Command registry types, DbLoading and
                  DbEagerJoin (the loading engines), KeyTuple, the Navigations trait
src/Migrations    versions, steps, actions, runner, snapshots, derived downs, generator, sync
src/Validation    SchemaGuard
src/Cli           the simpleorm command
tests/Sample      the fixture entities (mirror dotnet/samples)
tests/Conformance one data-driven runner per conformance folder
tests/Samples     drives samples/SimpleOrm.Sample the way dotnet/tests drive the C# sample
samples/SimpleOrm.Sample
                  a standalone consumer project (own composer.json, path repository to ../..):
                  the sample model, its V0001–V0009 migrations + snapshots, repositories, a
                  registry, and bin/demo.php — see its README
```

How PHP differs from the reference — and only there — is the table in
[CODING-STANDARD.md §10](CODING-STANDARD.md): synchronous methods, `Generator`
streaming, `Decimal` and `DateTimeImmutable` conventions, `private(set)`
navigations, explicit target classes on collection navigations, registries as
static methods, `$db->from(User::class)` for criteria, directory-based migration
discovery. Read the standard before contributing; it is the uniformity contract.

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

## The CLI

`bin/simpleorm` has no assembly to load, so it takes a PSR-4 root instead:
`--src <dir> --namespace <Ns>` names the directory entities and registries are
discovered under (via `ClassScanner`). Migrations default to `<src>/Migrations`
under `<Ns>\Migrations` — override either with `--migrations`/
`--migrations-namespace` when they live elsewhere. Snapshots default to the
migrations directory (`--snapshots` for `migrate down`; `--out` for
`snapshot`/`diff`/`shadow`). `--db` is a bare SQLite path, a `sqlite:` DSN, or
`Data Source=...`; `--dialect` accepts only `sqlite` today (ADR-0026).

```bash
php bin/simpleorm migrate --src src --namespace 'App' --db app.db
php bin/simpleorm status --src src --namespace 'App' --db app.db
php bin/simpleorm validate --src src --namespace 'App' --db app.db
php bin/simpleorm snapshot --src src --namespace 'App' --db app.db
php bin/simpleorm diff --src src --namespace 'App' --name AddNote
php bin/simpleorm shadow --src src --namespace 'App' --from V0001 --to V0003
```

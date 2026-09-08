# SimpleOrm.Sample (PHP)

The PHP counterpart of `dotnet/samples/SimpleOrm.Sample`: the fixture entity
model, its nine-version migration history with snapshots, two repositories, a
query/command registry, and a runnable end-to-end demo. It is a consumer of the
port, not part of it: it depends on `simpleorm/simpleorm` through a Composer
path repository and uses only the public API.

```
bin/demo.php                     migrate → validate → CRUD → criteria → registry → statement →
                                 view → concurrency → transaction → delete → derived rollback → export
src/Models/Tables/               User, Role, UserRole (composite key), UserProfile (1:1),
                                 Transaction (version column), TransactionDetail, BaseModel, TransactionStatus
src/Models/Views/                UserTransactionTotal (self-contained view, ADR-0008 add.3)
src/Models/Statements/           DailySales + DailySalesArgs (statement entity, ADR-0008 add.2)
src/Models/Procedures/           UserActivityReport (declaration-only on SQLite)
src/Models/MaterializedViews/    MonthlySalesTotal (declaration-only on SQLite)
src/Migrations/                  V0001..V0009 root versions composing per-object steps under
                                 Table/<Entity>/ and View/<Entity>/, with V000N.schema.json snapshots
src/Repositories/                Repository (the ADR-0016 generic base), UserRepository, TransactionRepository
src/Registry/                    Queries (incl. §7.10 json_group_array nesting), Commands, their args and rows
```

The snapshot files are copied verbatim from the C# sample: they are language
neutral (dialect storage types, ADR-0017 format v2), which is what lets
`migrate down` derive every rollback here without a single `down()` override.

## Run it

```bash
cd php/samples/SimpleOrm.Sample
composer install
composer demo
```

`composer demo` migrates a temp SQLite file, validates it with SchemaGuard,
walks every Level 0–1 feature, rolls the history back to V0003 and forward
again, and deletes the file. Pass a path to keep the database:
`php -d extension=pdo_sqlite bin/demo.php sample.db`.

The CLI works against the sample the same way it works against any app: a PSR-4
root plus its namespace (`php/README.md`):

```bash
composer simpleorm -- migrate --src src --namespace 'SimpleOrm\Sample' --db sample.db
composer simpleorm -- status  --src src --namespace 'SimpleOrm\Sample' --db sample.db
composer simpleorm -- validate --src src --namespace 'SimpleOrm\Sample' --db sample.db
composer simpleorm -- export-metadata --src src --namespace 'SimpleOrm\Sample' --out build/metadata
composer simpleorm -- diff --src src --namespace 'SimpleOrm\Sample' --name Nothing
composer simpleorm -- shadow --src src --namespace 'SimpleOrm\Sample' --out build/shadow
composer simpleorm -- migrate down --to 3 --src src --namespace 'SimpleOrm\Sample' --db sample.db
```

From the port's own checkout the sample is also autoloaded for tests
(`composer.json` `autoload-dev`), and `tests/Samples/SampleProjectTest.php`
drives it the way `dotnet/tests` drive the C# sample.

## What differs from the C# sample

Only what `php/CODING-STANDARD.md` §10 already lists: synchronous calls,
`Decimal` and `DateTimeImmutable` (UTC) for money and instants, `private(set)`
navigations with explicit target classes, `#[Column(type: ColumnType::Int32)]`
where the C# property type carried the token, `entityClass()` on migration
steps instead of a generic argument, and `$db->from(User::class)` for criteria.
One addition: the C# reference ships `Repository<TEntity>` in the library
(ADR-0016); the PHP port has no counterpart yet, so `src/Repositories/Repository.php`
carries it inside the sample.

# SimpleOrm PHP — coding standard

This file is the uniformity contract for everyone (human or agent) writing code
under `php/`. Read it before writing a line; re-read the checklist before saying
anything is done. The C# implementation under `dotnet/` is the **reference**: the
PHP port mirrors its behavior, names, error codes, and rendered SQL — it does not
reinvent them. Where PHP forces a different shape, the adaptation is listed in
§10 and nowhere else.

## 1. Language level and files

- **PHP 8.4**, `declare(strict_types=1);` as the first statement of every file.
- One class, interface, enum, or trait per file; file name = type name; PSR-4:
  `SimpleOrm\` → `src/`, `SimpleOrm\Tests\` → `tests/`.
- PSR-12 formatting: 4-space indent, `{` on its own line for classes/methods,
  same line for control structures, one blank line between members, no trailing
  whitespace, LF line endings, 120-column soft limit.
- LF line endings are pinned repo-wide by the root `.gitattributes`
  (`* text=auto eol=lf`, overriding `core.autocrlf`): nowdoc DDL and migration
  checksums are byte-exact, so a CRLF checkout changes checksums and fails
  `version_table_sql_matches_the_reference_ddl`.
- `use` imports sorted alphabetically, one per line, no unused imports, no group
  use, fully-qualified names only in attributes' string arguments.
- No `require`/`include` outside `bin/` and test bootstrap; Composer autoload only.
- No runtime dependencies beyond `ext-pdo` (+ the PDO driver of the dialect in use:
  `pdo_sqlite`, `pdo_pgsql`, `pdo_sqlsrv` or `pdo_odbc` — extensions, never packages;
  `composer.json` lists them under `suggest`). Dev dependency: PHPUnit only. Anything
  else is asked for first (CLAUDE.md §4).

## 2. Naming

| Thing | Style | Example |
|---|---|---|
| Namespaces | `SimpleOrm\<Area>` mirroring the C# areas | `SimpleOrm\Metadata`, `SimpleOrm\Migrations` |
| Classes, interfaces, enums, attributes | PascalCase, **no `I` prefix, no `Interface` suffix** | `Dialect`, `TypeHandler`, `EntityMap` |
| Methods, properties, variables | camelCase | `columnName`, `keyProperties()` |
| Constants | UPPER_SNAKE | `GENERATED_MARKER` |
| Enum cases | PascalCase | `RelationKind::MaterializedView` |
| Error codes | exactly as `spec/errors.md` | `MAP-001`, `MIG-020` |
| Test methods | `#[Test]` + snake_case sentence | `amend_refuses_a_dialect_switch` |
| Database names | come from the naming convention, never hand-built | |

Mirror the C# member names when porting: `IDialect.QuoteIdentifier` → `Dialect::quoteIdentifier`, `EntityMap.KeyProperties` → `EntityMap::$keyProperties`. A reader of one implementation must find the other by name.

## 3. Types and shape

- Every parameter, return, and property is typed. `mixed` only for values that
  genuinely cross the database boundary (a bound parameter, a read cell) and for
  args objects; never as a shortcut.
- **`final` by default.** A class is non-final only when the design extends it
  (`SimpleOrmException`, `MigrationVersion`, `TableMigration`, `ViewMigration`).
- **Immutable data**: model and value classes are `final readonly class` (or use
  `public readonly` properties) with constructor promotion. No setters on data.
- Closed sets are enums, never string constants: `RelationKind`, `KeyStrategy`,
  `ColumnType`, `SortOrder`, `MigrationState`, `FetchMode` (Level 2).
- Collections: PHP arrays with docblock shapes — `list<PropertyMap>`,
  `array<string, string>` — and `readonly` where they are data. Never mutate an
  array you received.
- Class references are `class-string` (`Transaction::class`), never bare names.
- Nullability is real: `?T` or a default; never `null` smuggled through `mixed`.
- No static mutable state, no singletons, no globals. Caches live in instances
  (`EntityMapLoader` owns its map cache; `Db` owns its loader).
- Composition over inheritance; interfaces at seams only: `Dialect`,
  `TypeHandler`, `NamingConvention`. Do not add abstractions "for the future".
- **A base class is never an entity** (`MAP-028`, ADR-0041). Declare a class that only shares members `abstract`: the
  one map-loading entry point (`EntityMapLoader::load`), the builder, and `MappingOptions` refuse an abstract class
  (`isAbstract()`), a mapped class whose parent chain (`getParentClass()`) carries `#[Table]`/`#[View]`/
  `#[MaterializedView]`/`#[Statement]`/`#[Procedure]` or `#[Index]` is refused with both classes named, and
  assembly scans (SchemaGuard, the CLI) refuse a concrete sourceless class another scanned class extends — all
  three through `SimpleOrm\Metadata\BaseClassRules`, the only home of the rule. PHP does not inherit class attributes
  by reflection, so a subclass can never adopt its parent's relation source; PHP has no divergence here (§10).

## 4. Errors

- Runtime failures throw `SimpleOrmException(errorCode, target, message)` — the
  code from `spec/errors.md`, the thing at fault (query name, `Type.property`,
  parameter, session), and what was expected. The message reads
  `"<code> <target>: <message>"` and **says what to do** when there is something
  to do (`add --force`, `use isNull()`).
- Metadata loading collects every violation for a type into `MappingException`
  (never first-error-only); SchemaGuard collects everything into
  `SchemaValidationException`. Concurrency conflicts are `ConcurrencyException`
  (`CRUD-010`).
- A new rule gets its code in `spec/errors.md` before it gets a message.
- No `@` suppression, no empty `catch`, no catching `\Throwable` to continue.
  Provider exceptions (`PDOException`) are wrapped where the spec names a code
  (`MIG-021`, `VAL-001`) and otherwise propagate.

## 5. SQL

- **Never build SQL from user data by concatenation.** Values bind as parameters
  (`@name`, rewritten to PDO's `:name` by `SqlPlaceholders`); identifiers render
  only through `Dialect::quoteIdentifier`; migration DDL from literal, reviewed
  specs.
- Parameterized statements run through `PdoBinder::bindAndExecute()`, never a
  bare `PDOStatement::execute([...])`: PDO binds every array value as
  `PDO::PARAM_STR`, which SQLite coerces back only where the compared column has
  declared affinity — a view's aggregate or a `pragma_*` column compares a
  TEXT "0" as greater than every INTEGER, so numeric filters match nothing (§10).
- Rendered SQL for SQLite is **byte-identical to the C# reference**: the
  `conformance/ast/` `sqlite` strings, generated CRUD, CREATE TABLE/INDEX, and
  migration action DDL. Checksums (`SHA-256` of rendered Up SQL) must match a C#
  run on the same migration. When in doubt, read the C# renderer and copy the
  string shape exactly, including case and spacing.
- Explicit column lists always; `select *` is a lint error (`VAL-021`), never
  emitted by the library.

## 6. Comments and documentation

- Every class carries a docblock stating its **purpose** and citing its ruling:
  the CLAUDE.md section or ADR (`(§7.23)`, `(ADR-0017 add.3)`). Not what the
  code does — why it exists and what it guarantees.
- Inline comments explain a non-obvious decision or a guard's reason; they do not
  narrate statements. A comment that restates the next line is deleted.
- Public methods get a one-line docblock when the name is not the whole story;
  `@param`/`@return` only to carry array shapes or `class-string` templates.
- English, no filler, present tense.

## 7. Tests

- PHPUnit 11; `final class <Subject>Test extends TestCase`; `#[Test]` attribute on
  every test with a snake_case sentence name; one behavior per test; assert the
  error **code** (`$e->errorCode`), never the message text.
- Real databases only: a temp-file SQLite database per test class
  (`Support\TempDatabase`), deleted afterwards; a per-fixture database on a real
  server for the SQL Server and PostgreSQL legs (`Support\PostgresDatabase`,
  `Support\SqlServerDatabase`), skipped as a group where no server answers
  (spec/dialects.md "Testing contract"). **No mocks, no fakes, no in-memory
  stand-ins for the database.**
- Conformance runners live in `tests/Conformance/`, one per `conformance/*`
  folder, read the JSON from `../../conformance/`, and are data-driven
  (`#[DataProvider]`). They are the definition of done: a feature without a
  passing conformance case is not done (CLAUDE.md §9).
- Fixture entities live in `tests/Sample/Models/` and mirror
  `dotnet/samples/SimpleOrm.Sample/Models` one-to-one. Test-local fixtures go in
  the test file's own namespace, never in `Sample`.
- Run: `composer test` (which loads `pdo_sqlite` via `-d extension=…` so no
  `php.ini` edit is needed).

## 8. Reuse, maintainability, extensibility

- One implementation per concept: placeholder scanning is `SqlPlaceholders`;
  snake_case is `SnakeCaseNamingConvention`; DDL rendering is the dialect;
  JSON writing for conformance artifacts is `Json\CanonicalWriter`. If you are
  about to write a second one, use the first.
- The dialect seam carries **exactly** the C# `IDialect` member set (CLAUDE.md
  §7.25). New members only when a second dialect needs them — and then the
  SQLite implementation returns its previous strings byte-for-byte.
- Extension points are interfaces with one obvious implementation
  (`Dialect`/`SqliteDialect`, `TypeHandler`, `NamingConvention`); everything else
  is `final`. Extending SimpleOrm means implementing an interface, not subclassing.
- Small methods with one job; no method longer than a screen; no boolean
  parameters that switch behavior (split the method or use an enum).
- Read `EntityMap` and nothing else for metadata (§7.1): no subsystem inspects
  attributes or reflection beyond the loaders.

## 9. Process

- Read the C# file for your area before porting it; keep the same decomposition
  where PHP allows it, so a fix in one implementation is findable in the other.
- Build and run the whole PHP test suite before declaring anything done.
- Deviations from the spec that PHP forces go into §10 of this file and into
  `php/README.md` — never silently into code.

## 10. PHP adaptations of the spec (the only allowed divergences)

| Spec / C# | PHP | Why |
|---|---|---|
| `async` + `CancellationToken` everywhere | synchronous methods, no token | PHP has no async; the spec-level contract is the behavior, not the calling convention (CLAUDE.md §12 anticipated this) |
| `StreamAsync` (`IAsyncEnumerable<T>`) | `stream()` returning a `Generator` | lazy row iteration |
| C# attributes | PHP 8 attributes in `SimpleOrm\Metadata\Attributes` | same names, same arguments |
| `[Column]` derives the neutral type from the CLR type | `#[Column(type: ColumnType::…)]` may **override** the token; defaults: `int`→`int64`, `float`→`double`, `bool`→`bool`, `string`→`string`, `Decimal`→`decimal`, `DateTimeImmutable`→`datetime`, pure enum→`enum_text` (`#[EnumAsInt]`→`enum_int`) | PHP cannot express `int32`, `date`, `time`, `datetimeoffset`, `guid`, `bytes` by type alone |
| `decimal` | `SimpleOrm\Types\Decimal` (string-backed value object) | PHP has no decimal type; floats are wrong |
| `DateTime` Kind=Utc | `DateTimeImmutable` in UTC; binding a non-UTC zone is `VAL-020` like `Kind=Unspecified` | the §7.9 rule, expressed with PHP's zone |
| `required` properties | a typed property without a default is required (`MAP-002` when the row lacks it); nullable or defaulted properties are optional | PHP's own initialization rule |
| navigation "no public setter" (`MAP-011`) | `public private(set)` (PHP 8.4 asymmetric visibility); a plainly `public` navigation is `MAP-011` | the library must be the only writer |
| `[OneToMany]`/`[ManyToMany]` element type from `IEnumerable<T>` | the target class is the attribute's first argument: `#[OneToMany(Transaction::class, 'userId')]`, `#[ManyToMany(Role::class, through: UserRole::class)]` | PHP arrays carry no element type |
| `[Statement("sql", "name", typeof(T), …)` pair stream | `#[Statement(sql, parameters: ['since' => ColumnType::DateTime])]` | PHP attributes take maps |
| `[Index("A", "B", SortOrder.Desc)]` token stream | `#[Index(['a', 'b', SortOrder::Desc], name: …, unique: …)]` | same tokens, one array |
| registry `static readonly Query<TArgs,TResult> X = Query.Inline(…)` | `public static function x(): Query { return Query::inline(XArgs::class, Row::class, 'select …'); }` | PHP has no generics and no `new` in static property initializers; SchemaGuard enumerates public static methods returning `Query`/`Command` |
| `db.Query<T>()` criteria entry | `$db->from(User::class)` | `query()` is taken by registered queries; `from` reads as SQL |
| `GetAsync<T>(7)` / `((a, b))` | `$db->get(User::class, 7)` / `$db->get(UserRole::class, [$a, $b])` | key parts as a list in key order |
| `[Statement]`-entity overloads of `QueryAsync<TResult>`/`QuerySingleAsync<TResult>`/`QuerySingleOrDefaultAsync<TResult>`/`StreamAsync<TResult>` | `Db::statement()`/`statementSingle()`/`statementSingleOrDefault()`/`streamStatement()` | the same-named registry methods (`query()`/`querySingle()`/`querySingleOrDefault()`/`stream()`) already take that name; PHP has no generic-arity overloading to tell a statement entity from a registered `Query` by type alone |
| `Criteria.In(string, string)` (the single-value overload guarding the "a lone string is `IEnumerable<char>`" trap) | `Criteria::inOne()` | `in()` already takes the array form; PHP cannot overload by parameter type (array vs. a lone string) |
| assembly scanning for migrations/entities | `MigrationSet::fromDirectory(dir, namespace)` (Composer-autoloaded classes under a namespace) or an explicit list; CLI takes `--migrations <dir> --namespace <ns>` | PHP has no assemblies |
| embedded `.schema.json` resources | snapshot files in the migrations directory (or `--snapshots`) | no resources in PHP |
| `Query.Embedded("path.sql")` (an embedded assembly resource, read once and cached in a `Lazy<string>`) | `Query::fromFile($argsType, $resultType, $path)` (and `Command::fromFile()`) — a real file read fresh on every call | no assemblies or embedded resources in PHP (the same reason as the row above); nothing to cache since there is no separate "declaration" moment to defer past (`SqlSource`'s docblock) |
| *(resolved)* `targetForeignKeyProperties` carried C# property names | the export now carries column names (`targetForeignKeyColumns`, `linkForeignKeyColumnsTo*`) resolved through the target's/link's map — no PHP-side spelling rule remains | ADR-0029 took the ADR-0026 proposal; row kept so the history reads |
| `ITypeHandler<T>` | `TypeHandler` interface: `type(): class-string`, `format()`, `parse()` | |
| unknown/handler column type exports as `clr:<full name>` | exports as `php:<fully qualified class-string>` | PHP has no CLR; the token names the platform the same way |
| `DiffOptions.Assembly` (one assembly for both class discovery and file I/O) | `DiffOptions::$migrationsDir` (PSR-4 directory `MigrationSet::fromDirectory()` reads compiled versions/steps from) *and* `$outDir` (where snapshots are read, sources written) | Composer's PSR-4 autoloader maps a namespace to one fixed, real directory it cannot be redirected to at runtime — unlike a loaded assembly, a namespace can't be pointed at an ephemeral temp directory. Ordinary CLI use sets both to the same path; `AmendCasesTest` gives `$migrationsDir` the fixed fixture directory and `$outDir` a fresh temp directory per case |
| a raw-SQL DTO's `List<Item>` member (§7.10 JSON nesting) resolved from the CLR generic argument | an `array`-typed member documented `@param list<\Fully\Qualified\Item> $name` (constructor parameter) or `@var list<\Fully\Qualified\Item>` (plain property) — the fully-qualified name must match `Item::class` verbatim, since `ResultMapper` uses it unmodified as the `TypeHandlerRegistry` lookup key (`"list<Item>"`, per `JsonTypeHandler::type()`) | PHP arrays carry no element type at all (unlike `[Column]`'s `ColumnType` override, which only replaces the *token* for an otherwise-typed scalar property); the docblock is the only place the element type can live, so it must be exact rather than convention-resolved |
| `SchemaGuard` describes a statement via `CommandBehavior.SchemaOnly` + `GetColumnSchema()` without executing it | `SchemaGuard` prepares each statement, binds `NULL` to every placeholder, and — for a query (a result type is expected) — executes it inside a transaction this class always rolls back before reading `columnCount()`/`getColumnMeta()`; a command (no result type) is only prepared, never executed, exactly like the C# reference. On `pgsql` (`PDO::ATTR_DRIVER_NAME`) every statement described — a registry entry's or a statement entity's, inside `validateStatement()` so no caller can forget it — runs under its own `savepoint simpleorm_shield`, rolled back to and released afterwards, because PostgreSQL aborts the whole transaction on any statement error and everything validated later would fail with "current transaction is aborted"; SQLite and SQL Server keep the transaction usable after an error and need none (the Go port keeps the same savepoint inside its `DescribeStatement`, where its describing lives — the rule in both: the savepoint wraps the describe, wherever the port realizes it) | PDO exposes column metadata only after `execute()`, never from a bare `prepare()`; the observable contract (no data changes, every violation still reported) is identical, only the mechanism differs. `getColumnMeta()` on this build reports `table`, `sqlite:decl_type`, `native_type`, and `name` (plus `pdo_type`/`len`/`precision`/`flags`) for a column resolved from a base table; for an **aliased** column `name` is the alias, not the origin column, and there is no key that recovers it — such a column is treated as an expression column (unknowable nullability, the stricter direction) unless its alias happens to name a real column of the reported table |
| CLI `--assembly <path>` loads the application assembly for both class discovery and file layout | CLI `--src <dir> --namespace <Ns>` (a PSR-4 root): entities and registries are discovered anywhere under it via `ClassScanner`; migrations default to `<src>/Migrations` under `<Ns>\Migrations` (`--migrations`/`--migrations-namespace` override either); snapshots default to the migrations directory (`--snapshots`); `--out` for `snapshot`/`diff`/`shadow` defaults to the migrations directory too; `--dialect sqlite\|sqlserver\|postgres` (default `sqlite`) resolves through `Dialects::create()` — any other name refuses before anything opens; `shadow` refuses every dialect but SQLite by name (spec/dialects.md "Connection strings and the CLI") | PHP has no assemblies (the same reason as the `DiffOptions`/`MigrationSet::fromDirectory` row above): Composer's PSR-4 autoloader maps a namespace to one fixed, real directory it cannot be redirected to at runtime, so the CLI needs an explicit root-directory-plus-namespace pair rather than a loadable binary |
| the `REL-004` unloaded-collection sentinel (`UnloadedList<T>`, a list that throws on any access, assigned to a database-read entity's collection navigations) | the mapper **`unset()`s** the collection navigation (through a closure bound to the entity's scope — `unset` is a write, so a `private(set)` property refuses it from outside), leaving the typed property uninitialized; with the opt-in trait `SimpleOrm\Session\Navigations` (`__get`, which PHP consults for an unset declared property) the read throws `SimpleOrmException('REL-004', …)`; without the trait it is PHP's own `Error` ("must not be accessed before initialization") — still a refusal, never a silent `[]`. User-constructed entities keep their `= []` initializer; loading re-initializes the property | ADR-0032 ruling 2 (spec/loading.md "The guard across languages"): a PHP array cannot throw on read, and the library cannot add `__get` to a user class — the trait is the user's opt-in |
| `Db` is a `partial class` split across `Db.cs`, `DbLoading.cs`, `DbEagerJoin.cs` | one `Db` plus two collaborators it owns and delegates to: `Session\DbLoading` (explicit/batch/MultiQuery/SubSelect engine) and `Session\DbEagerJoin` (join mode); the public surface stays on `Db` (`load`, `loadEach`, `from()->include()->fetch()`) | PHP has no partial classes; the file-per-concern decomposition is kept so a fix in one implementation is findable in the other |
| `Include(params string[])` / `Fetch(FetchMode)` | `include(string ...$navigations)` / `fetch(FetchMode $mode)` — `FetchMode` is a pure enum (`MultiQuery`, `SubSelect`, `Join`) | same names; PHP's variadic |
| `SelectJoin.On` as `(ParentProperty, TargetProperty)` tuples | `list<Query\JoinPair>` (a readonly value class) | PHP has no tuples |
| typed `DbParameter`s (ADO.NET infers `DbType` from the CLR value) | `PdoBinder::bindAndExecute()` binds each value with an explicit `PDO::PARAM_*` — `int`/`bool` → `PARAM_INT`, `null` → `PARAM_NULL`, a value whose property token is `bytes` → `PARAM_LOB` (`TypeConverter::toParameter()` wraps it as a `BinaryParameter`; a registry args property declares the token with `#[Column(type: ColumnType::Bytes)]`, ADR-0040 add.1), everything else → `PARAM_STR`; never a bare `PDOStatement::execute([...])` | PDO's `execute(array)` binds everything as `PARAM_STR`, and SQLite coerces text back only against declared affinity: a view aggregate or `pragma_*` column compares TEXT "0" above every INTEGER, so numeric filters silently match nothing; PHP has one string type for text and bytes, so only the token can choose the binary binding — under `PARAM_STR` SQLite stores TEXT (a STRICT `BLOB` refuses it) and PostgreSQL parses a `bytea` payload as escaped text |
| `DbOptions { Tracking = TrackingMode.Tracked }` (an init property) | `new DbOptions($dialect, tracking: TrackingMode::Tracked)` — `TrackingMode` is a pure enum (`Untracked` default, `Tracked`), the fourth constructor parameter of the readonly `DbOptions` | PHP's immutable value class takes options through the constructor; same names, named argument |
| `Db` is a `partial class`; tracking lives beside the loading partials | tracking is a third collaborator `Session\DbTracking` (identity map + snapshots, owned by `Db` only in `Tracked` mode, else `null`) with the readonly entry value `Session\TrackedInstance`; the public surface (`isTracking`, `isTracked`, `changesOf`, `isDirty`, `refresh`) stays on `Db`; the join engine builds its per-segment plans through `Db::adoptingPlan()` so roots and children pass the map too | PHP has no partial classes (the `DbLoading`/`DbEagerJoin` row above); an untracked session holds no map at all, so the Level 0–2 read path is unchanged |
| `ChangesOf<T>(entity)` / `IsDirty<T>` / `IsTracked<T>` / `RefreshAsync<T>` generic over the entity type | `changesOf(object)`, `isDirty(object)`, `isTracked(object)`, `refresh(object)` — the class comes from the instance | PHP has no generics; synchronous like the rest of the surface |
| `ChangesOf` names are the properties as the reference declares them (`Name`, `Address.City`) | names are the port's own declaration (`name`, `address.city`: `PropertyMap::propertyName()`, dotted for owned members) | the same rule as every property name in `UpdateOnly`/criteria in this port; the conformance runner maps the reference's spelling with `lcfirst` per segment |
| the snapshot holds the converter's database-bound value of each property (spec/session.md Clarifications, ADR-0035) | the same — `TypeConverter::toDatabase()`'s output, compared through `KeyTuple::valueEquals()` (temporals by instant, strings and bytes by content) — except that a `Decimal` the converter rendered as its own text stays the `Decimal`, so `10` and `10.0` are clean | PHP's bound decimal is text; the identity map's comparison is `KeyTuple` (§8), not a second equality |
| `[Cascade(CascadeOperations.Persist \| Remove)]` — a flags enum on a navigation | `#[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]`: a variadic list of `SimpleOrm\Metadata\CascadeOperation` cases (a string-backed enum whose value is the export token), normalized to declaration order without repeats; `RelationshipMap::$cascade` is a `list<CascadeOperation>` (empty when none) and the export's `"cascade"` array is written only when it is non-empty. An empty `#[Cascade()]` is `MAP-025` | PHP enums are not bit sets, so there is no `Persist \| Remove`; a list of cases is the nearest declaration with the same meaning |
| `HasPendingWork` (a property), `FlushAsync()` returning a summary record | `hasPendingWork()` (a method) and `flush(): FlushSummary` — a `final readonly class` with `inserted`/`updated`/`deleted` | PHP properties cannot be computed without a hook; the flush is synchronous like the rest of the surface; the readonly class is the record |
| `Db` is a `partial class`; the unit of work lives beside the tracking partial | the unit of work is a fourth collaborator `Session\DbUnitOfWork` (the planner and flusher, owned by `Db` only in `Tracked` mode, else `null` → `UOW-003` from `persist`/`remove`/`flush`; `hasPendingWork()` is false) with `Session\UnitOfWorkSchedule` (its pending inserts/removals, planned on a clone and swapped in only when a whole cascade succeeded), `Session\TypeRanks` (the foreign-key type rank, strongly connected components sharing a rank) and `Session\FlushJournal` (the undo of a failed flush: a `DbTracking::checkpoint()` of the identity map plus the keys and versions the statements wrote); the public surface (`persist`, `remove`, `flush`, `hasPendingWork`) stays on `Db`. Every flush statement is `Db::insert`/`update`/`delete` — `Db::requireWritableKeyed()` (the shared `CRUD-003`/`CRUD-002` check) and `DbTracking::writableDirtyProperties()`/`entries()` are the `@internal` seams it uses, so there is no second CRUD path (§8) | PHP has no partial classes (the `DbLoading`/`DbTracking` rows above) |
| "never loaded" is the unloaded sentinel (a navigation the unit of work must not read) | an **uninitialized** collection property — the mapper `unset()`s it (the `REL-004` row above) — tested with `ReflectionProperty::isInitialized()`, which never reads the property and so never fires the `Navigations` guard. `remove` cascading over an uninitialized collection is `UOW-005`; `persist` cascading over one cascades nothing; a loaded-but-empty collection (`[]`) cascades nothing in either | the same ruling as the `REL-004` row: PHP's uninitialized typed property is the one detectable "never loaded" state |
| keys flow before an insert "copying the target's key as it is, a null part included"; a failed flush resets a generated key to "unset" | a null key part is **not** copied into a non-nullable FK property (a typed property cannot hold it): the FK is left as it is and the insert's own checks (`CRUD-004`, the database) refuse it; a key property that was uninitialized before its insert goes back to uninitialized (`unset()`, not `null`) | PHP's typed properties have no "zero value" and no null for `int $id` |
| test runners: `tracking-cases` and `flush-cases` as separate C# engines | one `Tests\Support\StepScriptRunner` replays both folders (`TrackingCasesTest` and `FlushCasesTest` are two-line data providers over it); `RecordingUpdateDialect` records every rendered write (`statements`: `{op, entity, columns?}`) beside the update-only `updates` projection tracking tests read | one implementation per concept (§8): the flush vocabulary is the tracking vocabulary plus steps |
| `DbOptions.Clock` (a `Func<DateTime>`, UTC) | `new DbOptions($dialect, clock: fn (): DateTimeImmutable => …)` — the fifth constructor parameter, `?Closure`; null is the UTC wall clock. `DbOptions::now()` is the one place the clock is read and held to the UTC rule: a zone not named `UTC`, `Z`, or `+00:00` is `VAL-020`, raised when a hook first reads the clock (never at open) | PHP's immutable options class takes everything through the constructor; a `DateTimeImmutable` names its zone, and a zero offset is not "UTC" (Europe/London is +00:00 only in winter) |
| `[CreatedAt]`, `[UpdatedAt]`, `[SoftDelete]` on a column | `#[CreatedAt]`, `#[UpdatedAt]`, `#[SoftDelete]` in `SimpleOrm\Metadata\Attributes`; `PropertyMap::$audit` is an `AuditRole` enum (`None`, `CreatedAt`, `UpdatedAt` — the backing value is the export token) and `PropertyMap::$softDelete` a bool; `EntityMap::createdAtProperty()`/`updatedAtProperty()`/`softDeleteProperty()`. The manual `EntityMapBuilder::column()` takes `audit:`/`softDelete:`; the convention loader has no markers and declares none. `MAP-026` also covers a hook on a navigation, an `#[Ignore]`d or column-less property, an `#[Owned]` member, and a non-table source | same names; a property carries at most one hook, so the model holds one role value plus the flag (no flags enum, like the cascade row above) |
| `CreatedAt` is filled "when unset — null, or the type's default instant" | unset is `null` or an **uninitialized** typed property (both read as `null` through `PropertyMap::getValue()`); a `DateTimeImmutable` has no default instant | PHP typed properties have no zero value (the same ruling as the uninitialized-key row above) |
| hooks apply "to every write path" — the immediate operations and the unit of work | one implementation inside `Db`: `fillCreatedAt()` in `insert`, `withUpdatedAt()` + the stamp in `executeUpdate` (full row, column list, tracked dirty update), and `softDelete()` behind `delete`; the flush calls those same methods, and `FlushJournal` also records the created-at, stamp, flag, and version a statement moved on an instance so a failed flush puts them back; every undo (a failed immediate insert's created-at, a failed update's stamp, a soft delete's flag, the journal) restores through `PropertyMap::remember()`, which puts a never-initialized typed property back to *uninitialized* (a plain `setValue(null)` would be a `TypeError`). The by-key soft delete runs the same update path on a scratch instance (`newInstanceWithoutConstructor()`) carrying the key, flag, and stamp `DbTracking::writableDirtyProperties()` leaves the `#[UpdatedAt]` stamp out, so it never makes an instance dirty on its own (it still appears in `changesOf()`) | PHP has no partial classes, and one implementation per concept (§8): hooks live where the statements are |
| `Detach<T>(entity)` / `Merge<T>(detached)` | `detach(object $entity): void` and `merge(object $detached): object` — the class comes from the instance; `DbUnitOfWork::detach()` cancels what is scheduled, `DbTracking::detach()` forgets exactly that instance (by instance, never by its key), `DbTracking::heldInstance()` is merge's lookup; merge leaves a typed property the copy never initialized as it is on the session's instance and copies an owned navigation member by member into a fresh owned instance (nothing aliased); an unset key is null, a generated key at `0`, or an empty client GUID | PHP has no generics (the `changesOf` row above); an uninitialized property has no value to copy (null into a non-nullable one is a `TypeError`) |
| test runners: `hook-cases` and `merge-cases` as engines of their own | `HookCasesTest` and `MergeCasesTest` are two-line data providers over `Tests\Support\StepScriptRunner` (the `detach` and `merge` steps live there); every session-opening runner (`crud`, `tracking`, `flush`, `hook`, `merge`) fixes the clock through `Tests\Support\ConformanceClock` at `2026-08-29T00:00:00Z` | one implementation per concept (§8) |
| `[RenamedFrom("old")]` on a `[Column]` property | `#[RenamedFrom('old')]` in `SimpleOrm\Metadata\Attributes`; `PropertyMap::$renamedFrom` (`?string`); the manual `EntityMapBuilder::column()` takes `renamedFrom:`. Every misplacement (no `#[Column]`, on an `#[Ignore]`d or navigation property, on an `#[Owned]` navigation or an owned type's member, on a non-table source), a blank name, and two properties declaring one previous column (case-insensitive) are `MAP-027`, raised by the loader or the one assembler (spec/migrations.md Clarifications, ADR-0038); the held name is trimmed | same name and argument; the port keeps the declaration flat on the property like every other flag |
| `diff` takes "the declared renames" as a second map | `MigrationGenerator::diff($current, $snapshot, $renames, $declaredRenames = [])` — the explicit (`--rename`) pairs first, the model's `#[RenamedFrom]` pairs (`MigrationGenerator::declaredRenames($map)`, old => new) optional last; `diffMap()` derives the second map from the entity map. A declared pair declares nothing unless it can be a rename (the Clarifications' rule: old column in the snapshot, new one not, old one no longer mapped; an explicit rename of the same old column wins). Both maps are walked in ordinal order of the old name, the order the Go port's maps are walked in | the conformance runner feeds a case's `renamedFrom` columns as the second map; sorting keeps the emitted step identical across ports |
| `[Draft]` on the root class | `#[Draft]` (`SimpleOrm\Migrations\Draft`, class target) on the root; `MigrationVersion::isDraft()` reads it by reflection and is overridable; the generated root writes it as `#[\SimpleOrm\Migrations\Draft]` after a `DRAFT:` docblock line | same shape as C#'s marker attribute |
| `SqlVersion` takes a draft flag | `SqlVersion::draft($version, ...$steps)`, a static factory beside the constructor | the step list is variadic, so a `draft:` argument cannot follow it |
| a pending draft is `MIG-013` for `migrate`/`baseline`; `status` reports `Draft` | the runner checks after history validation (`migrate`) or after reading the history (`baseline`, versions ≤ the target only) and before any statement; `MigrationState::Draft` is the pending state of a draft's steps (a recorded draft keeps its normal state); `MigrationRunner::hasPending()` and `SchemaGuard` count a pending draft as pending (`MIG-030`); `shadow` replays a draft through a runner built with `replayDrafts: true` (constructor argument, shadow only); `diff --amend` of a root that is a draft keeps the marker | the Clarifications fix the semantics (pending, recorded, baseline scope, the version named, `MIG-030`, tooling-only replay, amend keeps the marker); these are PHP's seams for them — `replayDrafts` is `@internal`, set only by `SqliteShadow`, so the application has no way past `MIG-013` |
| the dialect packages `SimpleOrm.Sqlite` / `SimpleOrm.SqlServer` / `SimpleOrm.Postgres` (one assembly each, one provider package each) | one namespace, `SimpleOrm\Dialect`: `SqliteDialect`, `SqlServerDialect`, `PostgresDialect`, all over PDO; `Dialects::names()`/`Dialects::create($name)` is the one name→dialect registry the CLI's `--dialect` and the `ast/` runners share (spec/dialects.md). `SqlServerDialect` takes a `sqlsrv:` **or** `odbc:` PDO DSN verbatim (the SQL is the same over either driver) and never emulates prepares — PDO core rewrites the library's `:name` placeholders to the positional `?` that `pdo_odbc` needs, and emulation would interpolate string values as plain `'…'` literals with no `N` prefix, mangling non-ASCII text on the way to `nvarchar` (`ATTR_EMULATE_PREPARES = false` is set best-effort; a driver refusing the attribute prepares natively already); `PostgresDialect` takes a `pgsql:` DSN | PHP has no per-provider packages — PDO drivers are extensions loaded by name (`pdo_pgsql`, `pdo_sqlsrv`/`pdo_odbc`); the SQL Server driver is not installed on every machine, so the dialect must not depend on one |
| `BeginMigrationRunLock(connection)` returns a `DbTransaction` the runner commits or rolls back | `beginMigrationRunLock(PDO $connection): void` opens the transaction with `exec('BEGIN IMMEDIATE')` (SQLite), `exec('BEGIN TRANSACTION')` (SQL Server) or `exec('BEGIN')` (PostgreSQL) and takes the lock inside it (`sp_getapplock` / `set local lock_timeout` + `pg_advisory_xact_lock`, one `exec` each); the runner ends the run with a raw `COMMIT`/`ROLLBACK`. A refused `sp_getapplock` rolls the transaction back before the `PDOException` surfaces, so the connection is left as it was | `PDO::beginTransaction()` cannot say `BEGIN IMMEDIATE` and returns no transaction object; the raw statements are the one form that works on all three drivers |
| the generated key is read with `ExecuteScalar` — the first column of the first row of the first result set | `Db::runInsert` advances with `nextRowset()` past every result that has no columns, then reads column 0 of the first row (`fetch(PDO::FETCH_NUM)`) and `closeCursor()`s: `returning` yields the key as the only result on SQLite and PostgreSQL; SQL Server's unnamed `select cast(scope_identity() as bigint)` scalar follows the insert's own row-count result as the batch's next rowset | PDO surfaces a batch's row-count result as a rowset with no columns, where ADO.NET's `ExecuteScalar` skips it |
| `SupportsArrayParameters` binds a collection as one **typed** array parameter (Npgsql) | the collection binds as one `PDO::PARAM_STR` holding the PostgreSQL array literal (`{1,2}`, elements quoted/escaped per the literal grammar, `{}` when empty) under PDO's untyped parameter; the SQL stays untouched and PostgreSQL infers the array type from the comparison | PDO has no array parameters and sends every parameter untyped (spec/dialects.md "Array parameters") |
| `BindsTemporalsNatively` hands the provider a UTC `DateTime` value | the flag is honored for the converter's contract (`TypeConverter` returns the UTC `DateTimeImmutable`) but the PDO binder still sends the ISO-8601 string: under PDO's untyped parameter PostgreSQL infers `timestamptz` and `timestamptz >= text` never arises — nothing observable changes | PDO cannot bind a `DateTimeImmutable`; the untyped parameter is the equivalent (spec/dialects.md "Temporals") |
| the converter tells `DateTime`, `DateTimeOffset`, `DateOnly`, `TimeOnly` apart by CLR type on **write** | one `DateTimeImmutable` type, told apart by the mapped column's token: `TypeConverter::toDatabase(..., ?ColumnType $type)` takes it from the `PropertyMap` at every property-driven site (CRUD, key reads, criteria, tracking snapshots); `date` writes the value's **own calendar date** `Y-m-d`, `time` the time of day `H:i:s.u0`, `datetimeoffset` the `"o"` text with the value's offset kept (`+00:00`, never `Z`; natively the UTC instant, as `ToUniversalTime()`), `datetime` and a value with no token (a registered query's argument) the UTC `Z` form. `date`/`time` are text under native binding too: they carry no kind to refuse, and PostgreSQL infers the parameter's type from the column (the Go port binds `date` as the calendar date at UTC midnight and `time` as text — the same rule: never the instant shifted) | PHP has one temporal type; the token is the only place the shape can come from |
| markerless temporal storage (`datetime2`, `timestamp without time zone`) refuses `VAL-020` by the value's `Kind` | by the value's text: PDO returns every temporal as a string, and the §7.9 marker check accepts `Z`, `+hh:mm`, `+hhmm`, `+hh` (PostgreSQL spells `+07`), and the SQL Server driver's ` +00:00` after a space; a string with none refuses. `bytea` arrives as a stream and is read to a string; `time` values may carry fractional seconds | PDO's string-typed results carry the marker in the text itself |
| `[PostgresFact]`/`[SqlServerFact]` skip as a group where no server answers; a database per fixture | `Tests\Support\LiveServer` probes the admin DSN once per process (`SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER`, else the conventional default); `PostgresDatabase`/`SqlServerDatabase` create and drop a `simpleorm_test_<hex>` database and `skipUnlessAvailable()` in `setUp`; `composer test` stays `pdo_sqlite`-only unless the other extensions are loaded (`-d extension=pdo_pgsql`) | PHPUnit has no attribute-level skip; the extension may be absent altogether (then the probe fails and the leg skips) |
| the provider types every parameter and returns binary columns as bytes | `Dialect\PdoConventions` (optional, beside the seam, as Go's `TemporalMarkers`): `SqlServerDialect` declares `TemporalText::Odbc` (ODBC Driver 17 converts parameter text client-side and takes only `yyyy-mm-dd hh:mm:ss.fffffff +hh:mm`) and `BinaryText::Hex` (`pdo_odbc` fetches `varbinary` as hexadecimal text, decoded by the converter); `pdo_sqlite`/`pdo_pgsql` declare nothing | PDO binds untyped text and fetches character data; spec/dialects.md Clarification 19 (ADR-0040 add.2) |
| `[Trigger("name", TriggerTiming.After, TriggerEvent.Update \| TriggerEvent.Delete, "sql")]` — a flags enum for the events; `[Trigger(typeof(X))]` the entity-side link | `#[Trigger('name', TriggerTiming::After, [TriggerEvent::Update, TriggerEvent::Delete], 'sql', updateOf: ['prop'])]`: one `SimpleOrm\Metadata\Attributes\Trigger` attribute whose events are an **array of `TriggerEvent` cases** (a set; `TriggerEvent::ordered()` normalizes it to the fixed order `insert`, `update`, `delete`, without repeats — the same shape as the cascade row above), and whose one-argument form `#[Trigger(RoleAuditTrigger::class)]` is the link (`Trigger::isLink()`: nothing but the first argument given); `updateOf` names entity **properties** (resolved to columns); `MAP-029` for an incomplete declaration (no timing, no event, a blank body) — it is never read as a link. `TriggerDefinition::$events`/`$updateOf` are the resolved lists; `EntityMap::$triggers` is a `list<EntityTrigger>` (the trigger class + its definition), exported only when declared | PHP enums are not bit sets; one attribute class keeps the C# name `Trigger` for both shapes, and PHP attribute arguments are the only place the two can be told apart |
| `TriggerMigration<TEntity, TTrigger>` and its `Create/Recreate/ExpectDefinition/DropTrigger` actions | `TriggerMigration` with `entityClass()` and `triggerClass()` (no generics, like `TableMigration`/`ViewMigration`) and `TriggerActions::createTrigger()/recreate()/expectDefinition()/dropTrigger()`; the object folder is the trigger class (`Trigger/<Class>/V000N_<Desc>.php`), the object name is `TriggerRules::declaredName()` (readable from the class itself, so a removal step works after the entity unlinked it). The data-driven form is `SqlVersionStep::$trigger`/`$expectTrigger` (`SqlVersionTrigger`) | PHP has no generics (the `TableMigration` row above) |

Everything not in this table is identical to the reference. If you find something
that cannot be, stop and add a row here — with the reason — before coding around it.

## 11. Checklist before you say "done"

- [ ] `declare(strict_types=1)`, PSR-4 path, one type per file, `final`/`readonly` where §3 says
- [ ] Names mirror the C# reference; error codes match `spec/errors.md`
- [ ] No concatenated user data in SQL; SQLite SQL byte-identical to the reference
- [ ] Class docblocks cite the ruling; comments explain why
- [ ] Tests: `#[Test]` sentences, real SQLite temp files, conformance runner green
- [ ] `composer test` passes end to end; no new dependency
- [ ] Any spec divergence is a row in §10, not a comment in code

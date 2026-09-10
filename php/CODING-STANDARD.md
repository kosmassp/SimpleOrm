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
- No runtime dependencies beyond `ext-pdo` (+ the SQLite driver). Dev dependency:
  PHPUnit only. Anything else is asked for first (CLAUDE.md §4).

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
  (`Support\TempDatabase`), deleted afterwards. **No mocks, no fakes, no
  in-memory stand-ins for the database.**
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
| `SchemaGuard` describes a statement via `CommandBehavior.SchemaOnly` + `GetColumnSchema()` without executing it | `SchemaGuard` prepares each statement, binds `NULL` to every placeholder, and — for a query (a result type is expected) — executes it inside a transaction this class always rolls back before reading `columnCount()`/`getColumnMeta()`; a command (no result type) is only prepared, never executed, exactly like the C# reference | PDO exposes column metadata only after `execute()`, never from a bare `prepare()`; the observable contract (no data changes, every violation still reported) is identical, only the mechanism differs. `getColumnMeta()` on this build reports `table`, `sqlite:decl_type`, `native_type`, and `name` (plus `pdo_type`/`len`/`precision`/`flags`) for a column resolved from a base table; for an **aliased** column `name` is the alias, not the origin column, and there is no key that recovers it — such a column is treated as an expression column (unknowable nullability, the stricter direction) unless its alias happens to name a real column of the reported table |
| CLI `--assembly <path>` loads the application assembly for both class discovery and file layout | CLI `--src <dir> --namespace <Ns>` (a PSR-4 root): entities and registries are discovered anywhere under it via `ClassScanner`; migrations default to `<src>/Migrations` under `<Ns>\Migrations` (`--migrations`/`--migrations-namespace` override either); snapshots default to the migrations directory (`--snapshots`); `--out` for `snapshot`/`diff`/`shadow` defaults to the migrations directory too; `--dialect` accepts only `sqlite` — any other name refuses, naming ADR-0026 | PHP has no assemblies (the same reason as the `DiffOptions`/`MigrationSet::fromDirectory` row above): Composer's PSR-4 autoloader maps a namespace to one fixed, real directory it cannot be redirected to at runtime, so the CLI needs an explicit root-directory-plus-namespace pair rather than a loadable binary |
| the `REL-004` unloaded-collection sentinel (`UnloadedList<T>`, a list that throws on any access, assigned to a database-read entity's collection navigations) | the mapper **`unset()`s** the collection navigation (through a closure bound to the entity's scope — `unset` is a write, so a `private(set)` property refuses it from outside), leaving the typed property uninitialized; with the opt-in trait `SimpleOrm\Session\Navigations` (`__get`, which PHP consults for an unset declared property) the read throws `SimpleOrmException('REL-004', …)`; without the trait it is PHP's own `Error` ("must not be accessed before initialization") — still a refusal, never a silent `[]`. User-constructed entities keep their `= []` initializer; loading re-initializes the property | ADR-0032 ruling 2 (spec/loading.md "The guard across languages"): a PHP array cannot throw on read, and the library cannot add `__get` to a user class — the trait is the user's opt-in |
| `Db` is a `partial class` split across `Db.cs`, `DbLoading.cs`, `DbEagerJoin.cs` | one `Db` plus two collaborators it owns and delegates to: `Session\DbLoading` (explicit/batch/MultiQuery/SubSelect engine) and `Session\DbEagerJoin` (join mode); the public surface stays on `Db` (`load`, `loadEach`, `from()->include()->fetch()`) | PHP has no partial classes; the file-per-concern decomposition is kept so a fix in one implementation is findable in the other |
| `Include(params string[])` / `Fetch(FetchMode)` | `include(string ...$navigations)` / `fetch(FetchMode $mode)` — `FetchMode` is a pure enum (`MultiQuery`, `SubSelect`, `Join`) | same names; PHP's variadic |
| `SelectJoin.On` as `(ParentProperty, TargetProperty)` tuples | `list<Query\JoinPair>` (a readonly value class) | PHP has no tuples |
| typed `DbParameter`s (ADO.NET infers `DbType` from the CLR value) | `PdoBinder::bindAndExecute()` binds each value with an explicit `PDO::PARAM_*` — `int`/`bool` → `PARAM_INT`, `null` → `PARAM_NULL`, everything else → `PARAM_STR`; never a bare `PDOStatement::execute([...])` | PDO's `execute(array)` binds everything as `PARAM_STR`, and SQLite coerces text back only against declared affinity: a view aggregate or `pragma_*` column compares TEXT "0" above every INTEGER, so numeric filters silently match nothing |

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

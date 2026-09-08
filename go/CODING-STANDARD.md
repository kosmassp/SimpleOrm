# SimpleOrm Go — coding standard

This file is the uniformity contract for everyone (human or agent) writing code
under `go/`. Read it before writing a line; re-read the checklist before saying
anything is done. The C# implementation under `dotnet/` is the **reference**: the
Go port mirrors its behavior, names, error codes, and rendered SQL — it does not
reinvent them. Where Go forces a different shape, the adaptation is listed in
§10 and nowhere else. The PHP port (`php/CODING-STANDARD.md`) is the worked
example of the same method (ADR-0026); this port follows it (ADR-0027).

## 1. Language level, module, files

- **Go 1.25+** (`go.mod`; the SQLite driver needs it). Module
  `github.com/kosmassp/SimpleOrm/go`; everything lives under `go/orm/`.
- `gofmt` (and `goimports` grouping: standard library, blank line, module
  packages) on every file; `go vet ./...` clean; no linter directives.
- **Packages mirror the C# assemblies** (the public surface) and the C# areas
  (the internals):

  | Package | C# counterpart | Holds |
  |---|---|---|
  | `orm` | `SimpleOrm` | the session (`Open`, `Query`, `Insert`, `Get`, `From`…), the registry (`Inline`, `Embedded`, `Registry`), SchemaGuard, and **aliases** of every contract in `internal/core` — the one import application code needs |
  | `orm/sqlite` | `SimpleOrm.Sqlite` | the SQLite dialect and the shadow replayer |
  | `orm/migrations` | `Migration*.cs`, `Schema*.cs`, `DownDeriver`, `SnapshotSet` | versions, steps, actions, runner, snapshots, derived downs, generator, diff/amend, force sync |
  | `orm/cli` | `SimpleOrm.Cli` | the `simpleorm` command as a library the application's `main` embeds |
  | `orm/internal/core` | `Metadata.cs`, `QueryAst.cs`, `Criteria.cs`, `IDialect.cs`, `SimpleOrmException.cs`, … | the spec-level contracts, frozen: errors, `EntityMap`, tokens, `Decimal`/`GUID`/`Enum`, naming, `TypeHandler`, `Dialect`, the AST, the canonical JSON writer, the placeholder scanner |
  | `orm/internal/metadata` | `EntityMapLoader`, `AttributeMapLoader`, `MapAssembler`, `ConventionMapLoader`, `EntityMapBuilder`, `EntityMapJson` | the loaders and the export |
  | `orm/internal/mapping` | `TypeConverter`, `TypeHandlers` (JSON), `ResultMapper` | conversion and row mapping |
  | `orm/internal/params` | `ParameterBinder` | `@name` binding and IN-list expansion |
  | `orm/internal/render` | `AnsiSelectRenderer` | the reference SELECT rendering |
  | `orm/sample` | `dotnet/samples/SimpleOrm.Sample` | the ten fixture entities and their migrations tree |
  | `orm/internal/testsupport`, `orm/internal/conformance` | `SqliteFixture`, `Conformance*Tests` | test plumbing; one conformance runner per folder |

  `internal/` is what keeps the public surface to three packages (plus
  `migrations`, which application migration code imports through `orm`'s
  aliases). Internal packages import `core`; application code and the sample
  import `orm`. There are no import cycles by construction: `core` ← `metadata`,
  `mapping`, `params`, `render` ← `sqlite`, `migrations` ← `orm` ← `cli`. `cli`
  is the one exception: its own CLI commands (`export-metadata`, `snapshot`,
  `diff`, `shadow`) need a bare `metadata.Loader` that is not tied to a `Db`
  session, and `orm`'s aliases expose no constructor for one — so `cli` imports
  `orm/internal/metadata` directly alongside `orm`, rather than through it.
- **File names mirror the C# file names** in snake_case: `entity_map_json.go` ↔
  `EntityMapJson.cs`, `ansi_select_renderer.go` ↔ `AnsiSelectRenderer.cs`,
  `schema_guard.go` ↔ `SchemaGuard.cs`. A reader of one implementation must find
  the other by name. Every package has a `doc.go` or a package comment on its
  first file.
- LF line endings are pinned repo-wide by the root `.gitattributes`: migration
  checksums hash rendered SQL byte-for-byte.
- No `init()` functions; no package-level mutable state; package-level `var`s
  only for immutable values (`SnakeCaseConvention`, compiled regexes,
  registered queries — which are immutable after declaration).
- **Runtime dependency: `modernc.org/sqlite` only** (ADR-0027 — the pure-Go,
  cgo-free driver; the analog of `Microsoft.Data.Sqlite`/`pdo_sqlite`),
  referenced only from package `orm/sqlite` (`driver.go` registers it,
  `dialect.go` reads its column metadata). Tests use the standard `testing`
  package only. Anything else is asked for first (CLAUDE.md §4).

## 2. Naming

| Thing | Style | Example |
|---|---|---|
| Packages | one lowercase word, no underscores | `metadata`, `migrations`, `sqlite` |
| Types, funcs, methods, fields | Go mixed caps under **Go's initialism rule** | `CreateTableSQL`, `UserID`, `GUID`, `DDL` |
| Interfaces | the noun, **no `I` prefix**, no `-er` suffix unless idiomatic | `Dialect`, `TypeHandler`, `NamingConvention`, `EntityDefiner` |
| Constants of a closed set | type-prefixed | `RelationTable`, `KeyNatural`, `TypeInt64`, `RelationshipManyToOne` |
| Error codes | exactly as `spec/errors.md` | `MAP-001`, `MIG-020` |
| Test functions | `Test<Subject>_<BehaviourInCamelCase>`; `t.Run` names are sentences | `TestDecimal_EqualIgnoresTrailingFractionZeros` |
| Migration types | exactly the reference's class names — `V0001`, `V0002_AddDisplayName` — underscores included (MIG-001 parses them) | |
| Database names | come from the naming convention, never hand-built | |

Mirror the C# member names when porting, then apply the initialism rule:
`IDialect.CreateTableSql` → `Dialect.CreateTableSQL`, `EntityMap.KeyProperties`
→ `EntityMap.KeyProperties`, `Db.GetAsync<T>` → `orm.Get[T]`, `SqlSource` →
`SQLSource`. Drop the `Async` suffix everywhere: every I/O function takes a
`context.Context` instead (§10).

## 3. Types and shape

- **Data types have exported fields and no behavior**; behavior types keep
  their fields unexported. `EntityMap`, `PropertyMap`, `SelectAst`, snapshot
  documents are plain structs built once and never mutated afterwards — never
  mutate a slice or map you received.
- Closed sets are typed integer constants with a `Token()`/`String()` method,
  never bare strings: `RelationKind`, `KeyStrategy`, `RelationshipKind`,
  `SortOrder`, `MigrationState`. `ColumnType` is a string type because its
  values *are* the export tokens.
- Nullability is a pointer (`*string`, `*time.Time`); `sql.Null*` is not
  supported. Never smuggle null through `any` where a pointer can carry it.
- `any` only for values that genuinely cross the database boundary (a bound
  parameter, a read cell, an args struct, a key part) and for AST values.
- **Generics where C# uses generic methods**: Go methods cannot take type
  parameters, so `db.GetAsync<T>` is `orm.Get[T](ctx, db, key)` and
  `db.QueryAsync(q, args)` is `orm.Query(ctx, db, q, args)` (T inferred from
  the entry). Generic *types* keep methods: `QueryEntry[TArgs, TResult]`,
  `Repository[T]`, `CriteriaQuery[T]`.
- Interfaces at seams only: `Dialect`, `TypeHandler`, `NamingConvention`,
  `EntityDefiner`, `ExplicitMap`, `Entry`, the migration `Version`/`Step`
  contracts. Optional behavior on a user type (a step's `Down`) is an
  **optional interface** checked with a type assertion — Go's way of "virtual
  with a default". Do not add abstractions "for the future".
- Composition over inheritance; embedding only for the sample's `BaseModel`
  pattern and the `TableMigration[T]`/`ViewMigration[T]` markers.
- The `orm` package **re-exports** `internal/core` through type aliases and
  thin wrapper functions (`orm/aliases.go`). Aliases are the same types; the
  wrappers add nothing. Internal packages refer to `core.X`; application code
  and the sample refer to `orm.X`. Never a second implementation behind an alias.
- No `reflect` outside the loaders, the result mapper, the binder, and the
  migration set's type-name parsing. Everything else reads `EntityMap` (§7.1).

## 4. Errors

- **Errors are values, never panics.** Runtime failures return
  `*core.Error{Code, Target, Message}` (`core.NewError`/`core.Errorf`) — the
  code from `spec/errors.md`, the thing at fault (query name, `Type.Property`,
  parameter, session), and what was expected. `Error()` reads
  `"<code> <target>: <message>"` and **says what to do** when there is
  something to do (`pass --force`, `use IsNull`). Callers use `errors.As`;
  tests and the CLI use `core.CodeOf`/`core.HasCode`.
- Metadata loading collects every violation for a type into
  `*core.MappingErrors` (never first-error-only); SchemaGuard collects
  everything into `*core.ValidationErrors`. Concurrency conflicts are
  `*core.ConcurrencyError` (CRUD-010).
- A new rule gets its code in `spec/errors.md` before it gets a message.
  ADR-0027 registered `MAP-023` (malformed mapping declaration — an
  unparseable `orm` tag or unknown token; unreachable where annotations are typed).
- `panic` is allowed only for programmer errors that a literal in code
  establishes at startup (`MustDecimal` on a malformed literal); the doc
  comment says so.
- Driver errors are wrapped where the spec names a code (`MIG-021`, `VAL-001`),
  otherwise returned with `fmt.Errorf("…: %w", err)` so the cause stays
  inspectable. Never an ignored error: `_ =` only for best-effort cleanup with a
  comment saying why (a temp file the OS may still lock).
- Cancellation surfaces as the context's error (`context.Canceled`,
  `context.DeadlineExceeded`), never as a SimpleOrm code (spec/session.md).

## 5. SQL

- **Never build SQL from user data by concatenation.** Values bind as
  parameters: the SQL keeps its `@name` placeholders (SQLite parses them
  natively) and the binder passes `sql.Named(name, value)` with the SQL's own
  spelling; identifiers render only through `Dialect.QuoteIdentifier`;
  migration DDL comes from literal, reviewed specs. IN lists expand to
  generated placeholders (`@ids_0…`), never to values.
- Values bind **typed**: `int64`, `float64`, `bool`, `string`, `[]byte`, or
  `nil` — what the driver stores natively — after the conversion table has run
  (`Decimal` and temporals bind as their ISO/invariant text, enums as names).
- Rendered SQL for SQLite is **byte-identical to the C# reference**: the
  `conformance/ast/` `sqlite` strings, generated CRUD, CREATE TABLE/INDEX, and
  migration action DDL. Checksums (SHA-256 of rendered Up SQL) must match a C#
  run on the same migration. When in doubt, read the C# renderer and copy the
  string shape exactly, including case and spacing.
- Explicit column lists always; `select *` is a lint error (`VAL-021`), never
  emitted by the library.
- The session owns **one** `*sql.Conn` (pinned from the driver's pool at open)
  and at most one `*sql.Tx`; every statement runs on that connection and inside
  the transaction when one is active (§7.17).

## 6. Comments and documentation

- Every package and every exported type carries a doc comment stating its
  **purpose** and citing its ruling: the CLAUDE.md section or ADR (`(§7.23)`,
  `(ADR-0017 add.3)`). Not what the code does — why it exists and what it
  guarantees.
- Inline comments explain a non-obvious decision or a guard's reason; they do
  not narrate statements. A comment that restates the next line is deleted.
- Exported functions get a one-line doc comment when the name is not the whole
  story; Go doc comments start with the identifier.
- English, no filler, present tense.

## 7. Tests

- Standard `testing` only; table-driven where there are vectors; one behavior
  per top-level test; assert the error **code** (`core.CodeOf`), never the
  message text.
- Real databases only: a temp-file SQLite database per test
  (`testsupport.TempDatabase(t)`), deleted afterwards. **No mocks, no fakes, no
  in-memory stand-ins for the database** — `:memory:` is not a temp file.
- Tests live beside their package as an external test package
  (`package metadata_test`), so they exercise the exported surface and can
  import `orm` and `sample` without cycles. Test-local fixtures stay in the
  test file, never in `sample`.
- Conformance runners live in `orm/internal/conformance/`, one file per
  `conformance/*` folder, read the JSON from `../../../../conformance/`
  (`testsupport.ConformanceDir`), and are data-driven (`t.Run` per case file).
  They are the definition of done: a feature without a passing conformance
  case is not done (CLAUDE.md §9).
- Fixture entities live in `orm/sample/` and mirror
  `dotnet/samples/SimpleOrm.Sample/Models` one-to-one; the sample migrations
  tree mirrors `dotnet/samples/SimpleOrm.Sample/Migrations`.
- Run: `cd go && go vet ./... && go test ./...`.

## 8. Reuse, maintainability, extensibility

- One implementation per concept: placeholder scanning is
  `core.FindPlaceholders`/`PlaceholderOccurrences`; snake_case is
  `core.ToSnakeCase`; ISO-8601 formatting is `core.FormatUTC` (and its
  siblings); JSON writing for conformance artifacts is `core.CanonicalJSON`;
  DDL rendering is the dialect; the Go-type → token rule is
  `core.ColumnTypeOf`. If you are about to write a second one, use the first.
- The dialect seam carries **exactly** the C# `IDialect` member set (CLAUDE.md
  §7.25) under Go names. New members only when a second dialect needs them —
  and then the SQLite implementation returns its previous strings byte-for-byte.
- Extension points are interfaces with one obvious implementation
  (`Dialect`/`sqlite.Dialect`, `TypeHandler`, `NamingConvention`); everything
  else is a concrete type. Extending SimpleOrm means implementing an interface,
  not embedding a struct.
- Small functions with one job; no function longer than a screen; no boolean
  parameters that switch behavior (split the function or use a typed constant).
- Read `EntityMap` and nothing else for metadata (§7.1): no subsystem inspects
  tags, descriptors, or reflection beyond the loaders.

## 9. Process

- Read the C# file for your area before porting it; keep the same
  decomposition where Go allows it, so a fix in one implementation is findable
  in the other.
- Keep the package compiling after every file you write: write dependencies
  first; `go build ./...` after each file; `go vet ./... && go test ./...`
  before declaring anything done.
- Never edit a file you do not own (the foundation files under
  `internal/core`, `orm/aliases.go`, `orm/registry.go`, the sample entities,
  this document): if a contract is missing or wrong, report it with the exact
  change you need.
- Deviations from the spec that Go forces go into §10 of this file and into
  `go/README.md` — never silently into code.

## 10. Go adaptations of the spec (the only allowed divergences)

| Spec / C# | Go | Why |
|---|---|---|
| `async` + `CancellationToken` everywhere | `context.Context` as the first parameter of every I/O function, no `Async` suffix; cancellation is observed when a statement starts and per row; it surfaces as the context's error | Go's idiom honors the §2 contract exactly (unlike PHP, which had to drop it) |
| `StreamAsync` (`IAsyncEnumerable<T>`) | `orm.Stream(ctx, db, q, args) iter.Seq2[T, error]` — `for row, err := range …`; nothing buffered | range-over-func is Go's lazy sequence |
| C# attributes on properties (`[Column]`, `[Key]`, `[Generated]`, `[Version]`, `[Ignore]`, `[EnumAsInt]`, `[ManyToOne]`, `[OneToMany]`, `[OneToOne]`) | the `orm` **struct tag**: `orm:"column"`, `orm:"column=created_at"`, options `key`, `generated`, `version`, `enum_int`, `type=<token>`; `orm:"ignore"`; `orm:"many_to_one=UserID"`, `orm:"one_to_many=UserID"`, `orm:"one_to_one=UserID"` (composite FK lists join with `+`, in key order), `orm:"many_to_many"` | Go's per-field annotation is the tag; it carries strings only |
| class-level attributes (`[Table]`, `[View]`, `[MaterializedView]`, `[Statement]`, `[Procedure]`, `[Index]`) and typed references (`[ForeignKey(typeof(T))]`, `[ManyToMany(typeof(Link))]`) | the **descriptor**: `func (User) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("users"), Indexes: …, ForeignKeys: …, ManyToMany: …} }` with `orm.Table/View/MaterializedView/Statement/Procedure`, `orm.Index("Status", "CreatedAtUtc", orm.Desc).Named(…).Unique()`, `orm.ForeignKey[User]("UserID")`, `orm.ManyToMany[UserRole]("Roles")`, `orm.Param[time.Time]("since")` | Go has no type-level annotations and a tag cannot name a type; the owner's brief asked for a small explicit descriptor |
| two relation sources on one class (`MAP-012`) | unreachable: `EntityDef.Source` is one value | one field cannot hold two sources |
| `[Statement("sql", "since", typeof(DateTime))]` pair stream (`MAP-017` odd count / wrong token) | `orm.Statement(sql, orm.Param[time.Time]("since"))` — typed pairs; only the duplicate-name sub-case is reachable | the spec now phrases MAP-017 as "a name→type mapping without repeats" (ADR-0029); the token-list sub-cases are C#'s spelling |
| the neutral type derives from the CLR type | `core.ColumnTypeOf`: `int`/`int64`→`int64`, `int32`→`int32`, `int16`→`int16`, `float64`→`double`, `float32`→`float`, `bool`, `string`, `[]byte`→`bytes`, `time.Time`→`datetime`, `orm.Decimal`, `orm.GUID`, an `orm.Enum` type→`enum_text`; `type=date`/`time`/`datetimeoffset` override `time.Time`, `type=guid` overrides a string; any other type is `custom` and needs a handler | Go cannot express `date`, `time`, `datetimeoffset`, or `guid` by type alone |
| C# enums (`Enum.Parse`, arbitrary values) | a named type implementing `orm.Enum` (`EnumNames() []string`, declaration order); a string-kinded value is its name, an int-kinded value its position; `enum_int` stores the position; an unknown name reads as `MAP-031`; a named type without `EnumNames` is a plain scalar | Go has no enum construct; the declaration is explicit, never guessed |
| `decimal` | `orm.Decimal` — string-backed, canonical, compared by value, no arithmetic | Go has no decimal type; floats are wrong for money |
| `Guid` | `orm.GUID` (`[16]byte`), lowercase hyphenated text, 16-byte blob accepted on read | |
| `DateTime.Kind` (Utc passes, Local converts, Unspecified is `VAL-020`) | `time.Time` always carries a location: binding converts to UTC; the Unspecified refusal on **write** is unreachable; reading a stored string without a marker still refuses `VAL-020` | Go has no "unspecified" instant |
| nullable value types (`int?`, `DateTime?`) and nullable references | pointer fields (`*int64`, `*time.Time`, `*string`); `sql.Null*` unsupported | one nullability idiom |
| `required` members (DTO strictness, `MAP-002`) | for a raw-result DTO, an exported **non-pointer** field is required and a pointer field optional; entity results are exact both ways regardless | Go has no `required`; a pointer is the field that can legitimately stay unset |
| constructor mapping (§7.8, `MAP-003`) | Go has no constructors: rows assign exported fields (case- and underscore-insensitive for DTOs); `MAP-003` is unreachable; unexported fields are never mapped and never an error | Go's own visibility rule |
| class-level `[Owned]` marking a value type (ADR-0030) | the type implements `orm.OwnedType` with an empty method (`func (Address) OwnedType() {}`), the way `orm.Enum` marks enums; the navigation opts in with the `owned` tag option (`owned=<prefix>` overrides, `owned=` disables); a pointer navigation is nullable, a struct value required | Go has no class attributes; a marker method is the idiom |
| `MAP-011` (a navigation exposes a public setter) | not enforced: Go fields have no setters and a navigation must be exported for the library to populate it; navigations are library-written by convention (documented on each) | the spec states MAP-011 as an intent — "the library is a navigation's only writer" — enforced where expressible, documented here (ADR-0029) |
| inherited properties, ordered most-derived first | **embedded structs (by value)**: the struct's own fields first in declaration order, then each embedded struct's fields | Go's inheritance analog |
| generic methods (`db.GetAsync<T>`, `db.Query<T>()`, `db.CreateTableAsync<T>`) | package-level generic functions: `orm.Get[T](ctx, db, key)`, `orm.GetOrDefault[T]`, `orm.QueryAll[T]`, `orm.From[T](db)`, `orm.CreateTable[T]`, `orm.CreateView[T]`, `orm.Insert(ctx, db, &e)`, `orm.Update`, `orm.Query/QuerySingle/QuerySingleOrDefault/Stream/Execute(ctx, db, entry, args)` | methods cannot have type parameters |
| `DeleteAsync<T>(keyOrEntity)` dispatching on the argument | `orm.Delete[T](ctx, db, key)` (plain) and `orm.DeleteEntity(ctx, db, &e)` (version-checked) | no overloading; two names beat a runtime type switch |
| composite keys pass a tuple | `[]any{userID, roleID}` in key order | Go has no tuples |
| `db.Query<T>()` criteria chain (it throws `QRY-005` for a statement/procedure source at once); `OrderBy(prop, order = Asc)` | `orm.From[T](db).Where(…).OrderBy("Name", orm.Desc).Limit(20).List(ctx)` / `Single(ctx)` / `SingleOrDefault(ctx)`; `OrderBy(property, order ...SortOrder)` (variadic stands in for the optional parameter); the `QRY-005` gate fires at the terminal — still in the session, before any rendering | `Query` is the registry-entry function, and a chain start cannot return an error |
| `Criteria.In` overloads (typed enumerable, `params object[]`, the lone-string guard) | one generic `orm.In[T](property, values ...T)`: `In("ID", ids...)`, `In("Name", "Ada", "Grace")`; a lone string is a one-element list by construction | Go infers T; the IEnumerable<char> trap cannot occur |
| `public static readonly Query<A, R> X = Query.Inline(…)` / `Query.Embedded("path.sql")` (embedded resource, `Lazy<string>`) | `var X = orm.Inline[A, R]("select …")`, `orm.InlineCommand[A]`; `orm.Embedded[A, R](sqlFS, "Users/ByName.sql")` reading an `embed.FS` once at first use (QRY-003 when missing); the entry types are `orm.QueryEntry[A, R]` and `orm.CommandEntry[A]` | `embed.FS` is Go's embedded resource; a Go type and a function cannot share the name `Query`, and the call site `orm.Query(ctx, db, entry, args)` wins over the rarely spelled type name |
| `EmptyArgs.Value` | `orm.EmptyArgs{}` | |
| assembly scanning for registry entries, entities, migration versions, snapshots | explicit registration: `orm.Registry{Entities: []reflect.Type{orm.TypeOf[User]()}, Entries: []orm.Entry{UsersByName, MarkShipped}, Migrations: …, Snapshots: …}`; SchemaGuard, the runner, and the CLI take a `Registry`; `MIG-004` (an un-composed step) is unreachable for compiled steps | Go has no assembly introspection; registration is a Go value |
| embedded `.schema.json` resources | `//go:embed Table/*/*.schema.json View/*/*.schema.json` in the application's migrations package, passed as `fs.FS` (`migrations.SnapshotsFromFS`); `--snapshots <dir>` reads files (`SnapshotsFromDirectory`) | `embed.FS` is the resource mechanism |
| the CLI loads `--assembly <path>` | the CLI is a **library**: the application's own `cmd/simpleorm/main.go` calls `cli.Run(ctx, registry, os.Args[1:])`; `--db`, `--dialect` (`sqlite` only), `--namespace` (a migrations set name when the registry holds several), `--snapshots`, `--out` keep their meaning | a Go binary cannot load another binary's values |
| `MigrationVersion`/`MigrationStep` classes with names parsed from the class name; `TableMigration<TEntity>` with virtual `Down/PreDown/PostDown` | types with the reference's names: `type V0002 struct{}` + `Compose(*orm.VersionBuilder)`; `type V0002_AddDisplayName struct{ orm.TableMigration[models.User] }` + `Action(*orm.TableActions)`; the optional `Down`, `PreDown`, `PostDown` are optional interfaces; `MIG-001` parses `reflect.Type.Name()`; data-driven steps: `orm.SQLVersion` | Go has no inheritance; embedding a generic marker carries the entity type, type assertions carry the optional overrides |
| generated migration sources: `Migrations/V0002.cs` composing `Table.User.V0002_AddDisplayName` | one Go **package per object directory**: `Table/User/V0002_AddDisplayName.go` is `package user`; the root `V0002.go` imports `<migrations import path>/Table/User`; the generator takes `DiffOptions.Package` (the migrations package's import path) and derives the models' import paths from `reflect.Type.PkgPath()`; files are gofmt-formatted | Go packages are directories; the conformance-pinned paths (`Table/AmendWidget/V0002_AddNote`) keep their layout |
| `DiffOptions.Assembly` (one assembly for both class discovery and file I/O) | `DiffOptions.Set` (the compiled versions), `OutDir` (where snapshots are read and sources written), `Package` (import path) | a compiled set cannot be pointed at a temp directory; the amend runner gives `Set` the fixture and `OutDir` a temp directory (the PHP split, ADR-0026) |
| *(resolved)* `targetForeignKeyProperties` / `linkForeignKeysTo*` carried C# property names | the export now carries column names (`targetForeignKeyColumns`, `linkForeignKeyColumnsTo*`) resolved through the target's/link's map; `core.ToPascalCase` is gone | ADR-0029 took the ADR-0026/0027 proposal; row kept so the history reads |
| `ITypeHandler<T>` | `orm.TypeHandler[T]` (`Parse`, `Format`) registered with `orm.RegisterHandler[T](registry, h)` | methods cannot take type parameters |
| unknown/handler column type exports as `clr:<full name>` | exports as `go:<import path>.<Type>` | Go has no CLR; the token names the platform the same way |
| `SchemaGuard` describes a statement via `CommandBehavior.SchemaOnly` + `GetColumnSchema()` (origin table/column, declared type) | the SQLite dialect describes a prepared statement without executing it through the driver's column-info API (`sqlite3_column_decltype/table_name/origin_name`), reached with `(*sql.Conn).Raw`; a column with no origin is an expression column (unknowable nullability, the stricter direction) | `database/sql` exposes declared types but not origins; the driver does |
| a SchemaGuard violation's source names the offending `Type.Field` holding the query/command value | the source is the entry's `SQLSource.Description()` — the file path for `Embedded`, or a prefix of the inline SQL for `Inline`/`InlineCommand` | Go has no field reflection over a package-level value the way C# has `Type.GetFields()` |
| `Microsoft.Data.Sqlite`'s `BeginTransaction()` defaults to `BEGIN IMMEDIATE` | the dialect's `CreateConnection` opens the driver with `_txlock=immediate`, so every session transaction and the migration run lock (`BeginMigrationRunLock` → `conn.BeginTx`) are `BEGIN IMMEDIATE` | same locking semantics as the reference |
| `Repository<TEntity>` (subclass for entity-specific methods) | `orm.Repository[T]` with the same methods; entity-specific methods come from **embedding** it in the application's own type | Go's composition |
| `Db.OpenAsync(connectionString, options, ct)`; `DbOptions { Dialect, Mapping, TypeHandlers }` | `orm.Open(ctx, connectionString, orm.Options{Dialect: sqlite.New(), Mapping: …, TypeHandlers: …})`; `db.Begin(ctx)` returns `*orm.Tx` with `Commit(ctx)`/`Rollback(ctx)`; `db.Close()` rolls back an active transaction, then releases the connection | Go has no `IAsyncDisposable`; `defer db.Close()` is the idiom |

Everything not in this table is identical to the reference. If you find something
that cannot be, stop and add a row here — with the reason — before coding around it.

### The `orm` tag, precisely

```
orm:"column"                      mapped column; name from the naming convention
orm:"column=created_at"           explicit column name
orm:"column,key"                  key part (declaration order is key order)
orm:"column,key,generated"        database-generated key (integer kinds only, MAP-019)
orm:"column,version"              the version column (tables only)
orm:"column,enum_int"             an orm.Enum stored by position
orm:"column,type=date"            neutral type override: date, time, datetimeoffset, guid, int16, int32, …
orm:"owned"                       owned value type (ADR-0030): a struct (or pointer) implementing orm.OwnedType, its column fields flattened as <field>_<column>
orm:"owned=addr_"                 owned with an explicit prefix; orm:"owned=" disables the prefix
orm:"ignore"                      exported field deliberately unmapped
orm:"many_to_one=UserID"          navigation; FK fields on this struct, in the target's key order (A+B)
orm:"one_to_many=UserID"          navigation; FK fields on the target (the slice's element type)
orm:"one_to_one=UserID"           navigation; FK fields on the target (the pointer's type)
orm:"many_to_many"                navigation resolved through the descriptor's ManyToMany entry
```

Options are comma-separated; a value follows `=`; an unknown option, token, or
value is `MAP-023`. An exported field with no `orm` tag on a type that carries
any declaration is `MAP-010`; unexported fields are invisible.

## 11. Checklist before you say "done"

- [ ] `gofmt` clean, `go vet ./...` clean, package doc present, file names mirror the C# files
- [ ] Names mirror the C# reference under the initialism rule; error codes match `spec/errors.md`
- [ ] No concatenated user data in SQL; SQLite SQL byte-identical to the reference
- [ ] Doc comments cite the ruling; comments explain why
- [ ] Tests: real SQLite temp files, codes asserted, conformance runner green
- [ ] `go test ./...` passes end to end; no new dependency; no file you don't own edited
- [ ] Any spec divergence is a row in §10, not a comment in code

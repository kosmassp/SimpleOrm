# Dialects — the seam and its three realizations

The dialect is the only place SQL text is shaped for a database (CLAUDE.md §7.25,
§10.2). Every other subsystem — generated CRUD, the criteria renderer, SchemaGuard,
the migration runner, the loaders of Level 2, the unit of work of Level 3 — is
dialect-neutral and reaches the database through the seam's members. This document
states the seam as the spec sees it and the renderings of the three dialects that
exist today: **SQLite** (the reference, ADR-0003), **SQL Server** (ADR-0024), and
**PostgreSQL** (ADR-0025). A port implements the seam member for member (the names
below under its own casing rule) and must reproduce the SQL of this document where
`conformance/ast/` pins it byte for byte; everything else here is normative in
*shape* and *semantics*, and the reference text is given so that implementations
stay uniform rather than inventive.

The two non-SQLite dialects are **runtime-first** (ADR-0024/0025): session, CRUD,
criteria rendering, loading in every fetch mode, tracking, unit of work, hooks,
SchemaGuard, and the migration runner with the dialect's run lock. The snapshot,
shadow, diff, and force-sync tooling is SQLite-only in every implementation (see
"Deferred" below).

## The seam, member by member

| Member | Purpose | Consulted by |
|---|---|---|
| `CreateConnection(connectionString)` | an unopened connection for the dialect's native connection string | the session, the runner, the CLI |
| `QuoteIdentifier(name)` | every relation and column name the library emits passes through it | renderer, generated CRUD, `Get`, DDL |
| `CreateTableSql(map)`, `CreateIndexSql(map)`, `CreateViewSql(map)` | metadata-rendered DDL, idempotent (`IF NOT EXISTS` or the dialect's guard) | `CreateTable`/`CreateView`, an object's initial migration |
| `InsertSql`, `UpdateSql`, `UpdateOnlySql`, `DeleteSql` | the generated CRUD of spec/crud.md | the session, the unit of work |
| `LimitOffsetClause(limit, offset)` | the paging clause from pre-bound placeholders | the renderer |
| `PagingRequiresOrderBy` | a paged select without orderings gains `order by (select null)` | the renderer |
| `SupportsRowValueIn` | false → composite subquery membership rewrites as a correlated EXISTS (spec/query-ast.md) | the renderer |
| `SelectSql(ast, bind)` | the one rendering entry point; delegates to the reference renderer unless the dialect disagrees | criteria queries, loading |
| `SupportsArrayParameters` | true → a collection-typed argument binds as **one** array parameter with the SQL untouched (spec/session.md) | the parameter binder |
| `BindsTemporalsNatively` | true → temporals bind as native UTC values, not ISO-8601 strings | the type converter |
| `SupportsMaterializedViews`, `SupportsProcedures`, `SupportsTransactionalDdl` | capability flags; a `[MaterializedView]` on a dialect without them is `DDL-002` | `CreateView`, SchemaGuard, snapshots |
| `ColumnsInfoSql`, `IndexesInfoSql`, `ViewDefinitionSql` | introspection queries, parameter `@relation`, fixed result shapes (below) | SchemaGuard, force sync, the `MIG-012` guard |
| `IsDeclaredTypeCompatible(declaredType, type)` | the `VAL-011` table | SchemaGuard |
| `StorageType(property)` | the declared type `CREATE TABLE` emits (snapshots, diff, sync) | DDL, snapshots |
| `BeginMigrationRunLock(connection)` | one transaction held for the whole run, made exclusive the dialect's way | the runner |
| `VersionTableSql` | idempotent DDL for `schema_version` — same columns everywhere | the runner |
| `RenameTableSql`, `RenameColumnSql`, `AddColumnSql`, `DropColumnSql`, `DropTableSql`, `DropIndexSql` | the typed migration actions and the derived rollback | the runner, the down deriver, force sync |

A port adds members only where the reference has them. Describing a statement
without executing it (SchemaGuard, §7.19) is **not** a seam member: the reference
reads the provider's column schema through its data-access layer; a port whose
database layer has no such API exposes it as an optional capability beside the
seam (the Go `StatementDescriber`) or realizes it inside its SchemaGuard (the PHP
shield transaction) — its §10 says which.

## Capability flags

| Flag | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| `QuoteIdentifier` | unquoted — the reference rendering *is* the SQLite rendering | `[name]`, a `]` doubled | `"name"`, a `"` doubled |
| `PagingRequiresOrderBy` | false | **true** | false |
| `SupportsRowValueIn` | true | **false** | true |
| `SupportsArrayParameters` | false | false | **true** |
| `BindsTemporalsNatively` | false | false | **true** |
| `SupportsMaterializedViews` | false | false | **true** |
| `SupportsProcedures` | false | true (dormant — no execution machinery before Level 4) | true (dormant) |
| `SupportsTransactionalDdl` | true | true | true |

## Rendering

### Paging clause

Placeholders bind WHERE first, then limit, then offset, on every dialect — even
where the clause reorders them.

| Given | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| limit + offset | `limit @l offset @o` | `offset @o rows fetch next @l rows only` | `limit @l offset @o` |
| limit only | `limit @l` | `offset 0 rows fetch next @l rows only` | `limit @l` |
| offset only | `limit -1 offset @o` | `offset @o rows` | `offset @o` |

### Generated keys (spec/crud.md)

The generated INSERT lists every non-generated column. Where the database
generates the key:

- SQLite, PostgreSQL: `insert into <t> (…) values (…) returning <key>` — one row,
  one column, read as the key.
- SQL Server: `insert into [t] (…) values (…); select cast(scope_identity() as bigint)`
  — one batch, one scalar. Not an `OUTPUT` clause: it fails on tables with
  triggers, and legacy schemas have them. The generated-key strategy is
  identity-only (ADR-0020), which `scope_identity()` always covers.

Update, column-list update, and delete differ between dialects only by quoting.

### Metadata-rendered DDL

**Create table.** Column lines are `    <column> <storage type>[ not null]`, joined
by `,\n`, one per mapped property in metadata order; the body opens with ` (\n`
and closes with `\n)`.

| | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| guard | `create table if not exists <t>` | `if object_id(N'<t>', N'U') is null\ncreate table [t]` | `create table if not exists "t"` |
| database-generated key | `<c> INTEGER PRIMARY KEY` (the rowid alias) | `[c] <storage> identity(1,1) primary key` | `"c" <storage> generated by default as identity primary key` ("by default", so fixtures may insert explicit keys) |
| client GUID key | `<c> TEXT NOT NULL PRIMARY KEY` | `[c] uniqueidentifier not null primary key` | `"c" uuid not null primary key` |
| natural / composite key | trailing `primary key (<a>, <b>)` line | same, quoted | same, quoted |
| closing | `\n) STRICT` | `\n)` | `\n)` |

SQLite's `STRICT` is the SQLite realization of "strict by default" (§7.19); the
other two enforce declared types natively and need no analog. `NOT NULL` is
uppercase on SQLite (frozen by checksums) and lowercase on the other two.

**Create index** — `create [unique ]index … on <t> (<c>[ desc], …)`:

- SQLite / PostgreSQL: `create [unique ]index if not exists <i> on <t> (…)`.
- SQL Server: `if not exists (select 1 from sys.indexes where name = N'<i>' and object_id = object_id(N'<t>'))\ncreate [unique ]index [i] on [t] (…)`.

**Create view** — the defining SQL follows ` as\n`:

- SQLite: `create view if not exists <v> as\n…`.
- SQL Server: `create or alter view [v] as\n…`.
- PostgreSQL: `create or replace view "v" as\n…`; a materialized view:
  `create materialized view if not exists "v" as\n…`.

### Migration-action DDL (spec/migrations.md)

The SQLite strings are frozen — recorded checksums hash them. `N'…'` literals
double any single quote.

| Action | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| rename table | `alter table F rename to T` | `exec sp_rename N'F', N'T'` | `alter table "F" rename to "T"` |
| rename column | `alter table T rename column F to N` | `exec sp_rename N'T.F', N'N', 'COLUMN'` | `alter table "T" rename column "F" to "N"` |
| add column | `alter table T add column C <type>[ not null][ default <d>]` | `alter table [T] add [C] <type>[ not null][ default <d>]` | `alter table "T" add column "C" <type>[ not null][ default <d>]` |
| drop column | `alter table T drop column C` | `alter table [T] drop column [C]` | `alter table "T" drop column "C"` |
| drop table | `drop table T` | `drop table [T]` | `drop table "T"` |
| drop index | `drop index I` | `drop index [I] on [T]` (index names are table-scoped) | `drop index "I"` (schema-scoped) |

### The version table

Same columns and semantics everywhere (spec/migrations.md "Recording and
integrity"); the DDL is the dialect's:

```sql
-- SQL Server
if object_id(N'schema_version', N'U') is null
create table schema_version (
    version      bigint        not null,
    object       nvarchar(200) not null,
    description  nvarchar(400) not null,
    checksum     nvarchar(64)  not null,
    applied_at   nvarchar(40)  not null,
    execution_ms bigint        not null,
    primary key (version, object)
)

-- PostgreSQL
create table if not exists schema_version (
    version      bigint not null,
    object       text not null,
    description  text not null,
    checksum     text not null,
    applied_at   text not null,
    execution_ms bigint not null,
    primary key (version, object)
)
```

### The run lock

One transaction for the whole run; DDL is transactional on all three, so a failed
run applies nothing (spec/migrations.md "Locking and atomicity").

- SQLite: `BEGIN IMMEDIATE`.
- SQL Server: begin a transaction, then
  `declare @rc int; exec @rc = sp_getapplock @Resource = N'simpleorm:migrations', @LockMode = 'Exclusive', @LockOwner = 'Transaction', @LockTimeout = 60000; if @rc < 0 raiserror(N'the SimpleOrm migration lock is held by another run', 16, 1)`.
- PostgreSQL: begin a transaction, then
  `set local lock_timeout = '60s'; select pg_advisory_xact_lock(1399418224, 1818578802)`
  — the key pair is arbitrary but fixed (ASCII `Simp`, `leOr`); transaction-scoped,
  so it can never leak past a rollback.

A refusal to acquire the lock surfaces as the implementation's database error; the
run has not started.

## Storage types

What `CREATE TABLE` emits per neutral type token (spec/metadata-model.md), and what
snapshots and the diff generator compare on the dialects that have them.

| Token | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| `int16` | `INTEGER` | `smallint` | `smallint` |
| `int32` | `INTEGER` | `int` | `integer` |
| `int64` | `INTEGER` | `bigint` | `bigint` |
| `bool` | `INTEGER` | `bit` | `boolean` |
| `decimal` | `TEXT` | `decimal(38, 9)` — declare the real precision in migrations | `numeric` (unconstrained) |
| `double` | `REAL` | `float` | `double precision` |
| `float` | `REAL` | `real` | `real` |
| `string`, JSON, handler types | `TEXT` | `nvarchar(max)`; a key column `nvarchar(450)` (the indexable length), and `CREATE TABLE` also gives the length to a column of a declared index — `nvarchar(max)` cannot be an index key (error 1919) | `text` |
| `guid` | `TEXT` | `uniqueidentifier` | `uuid` |
| `bytes` | `BLOB` | `varbinary(max)`; a key column or a column of a declared index `varbinary(450)` (as for strings) | `bytea` |
| `datetime`, `datetimeoffset` | `TEXT` | `datetimeoffset` | `timestamp with time zone` |
| `date` | `TEXT` | `date` | `date` |
| `time` | `TEXT` | `time` | `time without time zone` |
| `enum_text` | `TEXT` | `nvarchar(100)` | `text` |
| `enum_int` | `INTEGER` | `int` | `integer` |

PostgreSQL types are spelled the way `format_type` reports them, so introspected
and rendered types compare cleanly.

## Declared-type compatibility (`VAL-011`)

The declared type is reduced to its **base name**: trimmed, lowercased, any
parenthesized length or precision removed — on PostgreSQL also mid-name
(`timestamp(3) with time zone` → `timestamp with time zone`) with whitespace
collapsed. An empty base name is compatible with everything on SQLite (an untyped
column enforces nothing) and with nothing elsewhere (those databases always declare
a type). A custom (handler) token is compatible only through a registered handler,
which the caller checks before consulting the table.

| Base name(s) | Compatible tokens |
|---|---|
| **SQLite** `int`, `integer` | `int16`, `int32`, `int64`, `bool`, `enum_int` |
| `real` | `double`, `float` |
| `blob` | `bytes`, `guid` |
| `text` | `string`, `decimal`, `guid`, `datetime`, `datetimeoffset`, `date`, `time`, `enum_text` |
| **SQL Server** `int`, `tinyint` | `int32`, `int64`, `int16`; `enum_int` |
| `bigint` | `int64`; `enum_int` |
| `smallint` | `int16`, `int32`, `int64`; `enum_int` |
| `bit` | `bool` |
| `decimal`, `numeric`, `money`, `smallmoney` | `decimal` |
| `float` | `double` |
| `real` | `float` |
| `nvarchar`, `varchar`, `nchar`, `char`, `ntext`, `text` | `string`; `enum_text` (`nvarchar`, `varchar`, `nchar`, `char` only) |
| `varbinary`, `binary`, `image` | `bytes` |
| `uniqueidentifier` | `guid` |
| `datetime`, `datetime2`, `smalldatetime` | `datetime` (type-compatible; the marker rule applies per value, below) |
| `datetimeoffset` | `datetime`, `datetimeoffset` |
| `date` | `datetime`, `date` |
| `time` | `time` |
| **PostgreSQL** `integer`, `int4` | `int32`, `int64`, `int16`; `enum_int` |
| `bigint`, `int8` | `int64`; `enum_int` |
| `smallint`, `int2` | `int16`, `int32`, `int64`; `enum_int` |
| `boolean`, `bool` | `bool` |
| `numeric`, `decimal`, `money` | `decimal` |
| `double precision`, `float8` | `double` |
| `real`, `float4` | `float` |
| `text`, `character varying`, `varchar`, `character`, `char`, `name` | `string`; `enum_text` (all but `name`) |
| `bytea` | `bytes` |
| `uuid` | `guid` |
| `timestamp with time zone`, `timestamptz` | `datetime`, `datetimeoffset` |
| `timestamp without time zone`, `timestamp` | `datetime` (type-compatible; the marker rule applies per value) |
| `date` | `datetime`, `date` |
| `time without time zone`, `time` | `time` |

## Temporals: the §7.9 rule on databases with real date types

SQLite stores ISO-8601 TEXT and the trailing `Z` is the UTC marker. On the other
dialects the rule "a stored value without UTC marking is `VAL-020`" reads:

| | Marker-carrying storage (reads back marked, UTC) | Markerless storage (type-compatible, **refuses `VAL-020` per value at read time**) |
|---|---|---|
| SQL Server | `datetimeoffset` | `datetime`, `datetime2`, `smalldatetime` |
| PostgreSQL | `timestamp with time zone` | `timestamp without time zone` |

The escape for legacy columns is the documented one: a registered type handler
declaring the column's actual kind (handlers win over the fixed table). `date` and
`time` columns carry no kind by design and never refuse.

How an implementation *detects* a markerless value is its own: by the value's
marker where the driver yields text or a kind-carrying value (ADO.NET `Kind`,
PDO's `+hh[:mm]`/`Z` suffix — the SQL Server driver spells the offset after a
space, PostgreSQL may spell it as `+07` with no minutes; both are markers), or by
the column's declared type from the driver's column metadata where its native
temporal type carries no marker at all. A port says which in its §10.

**Native binding** (`BindsTemporalsNatively`). PostgreSQL refuses
`timestamptz >= text`, so the reference binds temporals there as native UTC values
(`Kind=Unspecified` still refuses with `VAL-020`); SQLite stores the string and SQL
Server converts strings implicitly, so both bind ISO-8601 text. Where a driver has
no typed parameters (PDO), the ISO-8601 string under an *untyped* parameter is
equivalent — PostgreSQL infers the parameter's type from the comparison — and the
flag changes nothing observable; the port's §10 says so.

## Array parameters (`SupportsArrayParameters`, spec/session.md)

PostgreSQL only. The registry SQL is written dialect-natively (`where id = any(@ids)`);
the collection binds as **one** parameter and the SQL is untouched; an empty
collection matches no rows and never errors. "Typed" means the element's database
type travels with the parameter: natively where the driver has array parameters
(Npgsql's typed arrays, pgx's slices — the element type derives from the declared
element type, not from an absent first value), or as a PostgreSQL array literal
(`{1,2}`, elements quoted and escaped as the literal grammar requires, `{}` when
empty) under an untyped parameter where it has not (PDO) — PostgreSQL infers the
array type from the comparison. SQLite and SQL Server keep the expansion strategy
(`@ids_0…`; SQL Server's 2100-parameter cap is far above the 500 chunk size of
spec/loading.md).

Criteria `In` renders per-value placeholders on every dialect (valid everywhere;
an `= any` rendering for PostgreSQL is a possible later optimization, not a rule).

## Introspection result shapes

Parameter `@relation` on every query; an empty result means the relation is absent.
Flags are integers (`0`/`1`), ordinals zero-based. **Result columns carry the names
below** — every non-SQLite dialect aliases them (`as name`, `as "notnull"`, `as [pk]`,
…), so a consumer may read by name or by ordinal; SQLite's queries are the frozen
reference text and are read by ordinal.

- **Columns**: `name, type, notnull, pk` in column order, `pk` the key ordinal
  (0 = not part of the key); `type` is the declared type spelled as the storage
  type table above (SQL Server reconstructs `(precision,scale)` and `(length|max)`;
  PostgreSQL uses `format_type`).
- **Indexes** (explicitly created ones — never the primary key or a unique
  constraint's index): `index_name, unique, seqno, column, desc`, ordered by index
  name then position. Force sync matches indexes structurally (ADR-0017 add.2).
- **View definition**: one column, the stored definition (SQLite the `create view`
  text; SQL Server `sys.sql_modules.definition`; PostgreSQL `pg_get_viewdef(oid, true)`
  for views and materialized views). The `MIG-012` guard normalizes it against the
  step's expected definition.

The reference queries (the SQLite ones are pinned by the ports already):

```sql
-- SQL Server, columns
select c.name as [name],
       case
           when t.name in (N'decimal', N'numeric')
               then t.name + N'(' + cast(c.precision as nvarchar(8)) + N',' + cast(c.scale as nvarchar(8)) + N')'
           when t.name in (N'nvarchar', N'nchar')
               then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length / 2 as nvarchar(8)) end + N')'
           when t.name in (N'varchar', N'char', N'varbinary', N'binary')
               then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length as nvarchar(8)) end + N')'
           else t.name
       end as [type],
       cast(case when c.is_nullable = 1 then 0 else 1 end as bigint) as [notnull],
       cast(isnull(pk.key_ordinal, 0) as bigint) as [pk]
from sys.columns c
join sys.types t on t.user_type_id = c.user_type_id
left join (
    select ic.object_id, ic.column_id, ic.key_ordinal
    from sys.index_columns ic
    join sys.indexes i on i.object_id = ic.object_id and i.index_id = ic.index_id
    where i.is_primary_key = 1
) pk on pk.object_id = c.object_id and pk.column_id = c.column_id
where c.object_id = object_id(@relation)
order by c.column_id

-- SQL Server, indexes
select i.name as [index_name], cast(i.is_unique as bigint) as [unique], cast(ic.key_ordinal - 1 as bigint) as [seqno],
       c.name as [column], cast(ic.is_descending_key as bigint) as [desc]
from sys.indexes i
join sys.index_columns ic on ic.object_id = i.object_id and ic.index_id = i.index_id and ic.is_included_column = 0
join sys.columns c on c.object_id = ic.object_id and c.column_id = ic.column_id
where i.object_id = object_id(@relation) and i.type > 0 and i.is_primary_key = 0 and i.is_unique_constraint = 0
order by i.name, ic.key_ordinal

-- SQL Server, view definition
select m.definition as [definition] from sys.sql_modules m join sys.views v on v.object_id = m.object_id
where v.object_id = object_id(@relation)

-- PostgreSQL, columns
select a.attname as name,
       format_type(a.atttypid, a.atttypmod) as type,
       (case when a.attnotnull then 1 else 0 end)::bigint as "notnull",
       (case when exists (
           select 1 from pg_index i
           where i.indrelid = a.attrelid and i.indisprimary and a.attnum = any(i.indkey)
       ) then 1 else 0 end)::bigint as pk
from pg_attribute a
where a.attrelid = to_regclass(@relation) and a.attnum > 0 and not a.attisdropped
order by a.attnum

-- PostgreSQL, indexes
select ic.relname as index_name,
       (case when i.indisunique then 1 else 0 end)::bigint as "unique",
       k.n::bigint as seqno,
       a.attname as "column",
       (case when (i.indoption[k.n] & 1) <> 0 then 1 else 0 end)::bigint as "desc"
from pg_index i
join pg_class ic on ic.oid = i.indexrelid
cross join lateral generate_series(0, i.indnkeyatts - 1) as k(n)
join pg_attribute a on a.attrelid = i.indrelid and a.attnum = i.indkey[k.n]
where i.indrelid = to_regclass(@relation)
  and not i.indisprimary
  and not exists (select 1 from pg_constraint con where con.conindid = i.indexrelid)
order by ic.relname, k.n

-- PostgreSQL, view definition
select pg_get_viewdef(c.oid, true) as definition from pg_class c
where c.oid = to_regclass(@relation) and c.relkind in ('v', 'm')
```

(The PostgreSQL column query reports `pk` as a presence flag, not an ordinal;
SchemaGuard only asks whether the column is part of the key.)

## Connection strings and the CLI

A dialect takes its driver's **native** connection string; the SQLite dialect also
accepts a bare file path and the reference's `Data Source=<path>` form. The CLI
selects the dialect by name — `--dialect sqlite|sqlserver|postgres`, default
`sqlite` — for every database command; an unknown name refuses before anything
opens. `shadow` refuses every dialect but SQLite by name (it replays into a
throwaway SQLite database).

## Deferred — the same list in every implementation

Named here so that no implementation attempts it ahead of the reference:

- **Snapshot, shadow, diff, and force-sync tooling** against SQL Server or
  PostgreSQL: untested in the reference, not ported. Rollbacks there need `Down()`
  overrides until snapshot tooling learns the dialect — `MIG-020` says so when a
  derived rollback is asked for.
- The `MIG-012` view-guard normalization against `sys.sql_modules` /
  `pg_get_viewdef` output: untested beyond SQLite.
- Procedure execution (`SupportsProcedures` is declared, dormant); `REFRESH
  MATERIALIZED VIEW`; `jsonb` and array-typed columns; schema qualifiers beyond
  the metadata's `Schema`; batching (spec/unit-of-work.md "Batching") — all Level 4.
- `migrations-cases/` and `cases/` fixtures per dialect: the conformance folders
  that reach a database run on SQLite; `ast/` is the per-dialect pin.
- Found by the ports, not yet ruled on: `time`- and `decimal`-typed array
  parameters; composite `nvarchar(450)` keys exceeding SQL Server's 900-byte index
  limit (declare real lengths in migrations). (`bytes` writes through untyped
  drivers and pgx's statement cache after a schema change are ruled —
  Clarifications 14 and 15.)

**Waiting for a SQL Server engine** (owner rulings, ADR-0040 add.1: nothing
changes until a live run proves the hazard; the first machine with an engine
runs these first):

- **The first green run of the SQL Server live legs** in all three
  implementations — no engine here, and go-mssqldb cannot address LocalDB (a TCP
  server is its default).
- **go-mssqldb binding `nil` as an `nvarchar(1)` NULL**: a NULL into `varbinary`
  may refuse (error 257). The bytes round trip every SQL Server leg runs ends with
  a NULL written into `varbinary(max)` — that write is the probe, in every
  implementation (an untyped null may reach the server as a character type
  through ADO.NET or PDO ODBC too).
- **go-mssqldb summing trigger row counts into `RowsAffected`**: a `CRUD-010`
  hazard on schemas whose triggers lack `SET NOCOUNT ON`. To be tested against a
  schema with real triggers before any rule (for orientation: EF Core emits `SET
  NOCOUNT ON` plus `OUTPUT` and needs an explicit opt-out for tables with
  triggers; Hibernate trusts the raw count and asks trigger authors for `SET
  NOCOUNT ON`).

## Testing contract

Live tests for a non-SQLite dialect run against a **real server** — never a mock,
never an in-memory stand-in — and **skip as a group** where no server answers, so
every implementation's default test run still needs only its language toolchain.
Each fixture creates its own database, named `simpleorm_test_<random hex>`, on the
admin connection and drops it afterwards (the analog of the temp-file SQLite
database, ADR-0003).

| | Admin connection | Default where unset |
|---|---|---|
| PostgreSQL | environment variable `SIMPLEORM_POSTGRES` — the implementation's native admin connection string with CREATEDB rights | the conventional local server: `localhost:5432`, `postgres`/`postgres`, database `postgres` |
| SQL Server | environment variable `SIMPLEORM_SQLSERVER` — the implementation's native admin connection string | the LocalDB default instance `(localdb)\MSSQLLocalDB` where the platform has it; otherwise nothing answers and the leg skips |

Each live leg covers at least (ADR-0024/0025): generated-key write-back and a
temporal round-trip reading back marked; criteria paging with and without
orderings; reserved-word identifiers end to end (table `order`, column `desc`);
version-column concurrency on update and delete; transaction scopes; explicit,
batch, and link loading; a subselect include over a paged root; the composite-key
subselect (through the EXISTS rewrite on SQL Server, native row-value IN on
PostgreSQL); migrations up, re-run, and down under the run lock (with `Down()`
overrides); the `VAL-020` refusal on a markerless column; the bytes round trip
of Clarification 14 (insert, column-list and full update, a criteria and a
registry match, a NULL). PostgreSQL additionally: array parameters filled and
empty, a temporal criteria comparison against `timestamptz`, a materialized view
created and read, and — where the driver caches statements — the cached-plan
policy of Clarification 15 in and out of a transaction.

## Clarifications

Answers to the four port gap lists of ADR-0040 (PHP and Go, SQL Server and
PostgreSQL — 42 questions, consolidated), each the answer the implementations now
embody. No rule above changed; the ADR records the three reference deviations the
same lists surfaced. Clarifications 14 and 15 are owner rulings on items the
ports found (ADR-0040 add.1).

1. **Introspection columns are named.** Every non-SQLite dialect aliases its result
   columns to the names of "Introspection result shapes" (the reference queries
   above carry the aliases); a consumer may read by name or by ordinal. SQLite's
   frozen queries are read by ordinal. `to_regclass(@relation)` and
   `object_id(@relation)` take the relation name **unquoted**: `to_regclass`
   resolves reserved words and folds case (`Order` → `order`), so no quoting rule
   exists for the parameter.
2. **The generated key is the first column of the first row of the first result
   set that has columns.** SQL Server's `insert …; select cast(scope_identity() as
   bigint)` batch yields the insert's own row-count result before the scalar; a
   result-set API advances past column-less results (PDO `nextRowset()`; go-mssqldb
   skips them itself; ADO.NET's `ExecuteScalar` does so implicitly). The scalar is
   unnamed — read it by position, never by the key column's name.
3. **The run lock may be taken as one batch or as consecutive statements** inside
   the transaction (pgx's extended protocol refuses a multi-statement string with
   parameters). A refused or failed acquisition **rolls the transaction back before
   the error surfaces**, so the connection is left as it was found. The runner
   closes the run with its own `COMMIT`/`ROLLBACK`; the dialect opens the
   transaction in whatever form that pairs with (a raw `BEGIN` where the driver's
   transaction API would not — PDO).
4. **Describing on PostgreSQL happens under a savepoint.** PostgreSQL aborts the
   whole transaction on any statement error, so a describe that runs inside a
   transaction (SchemaGuard's shield, or a session transaction) opens a savepoint,
   rolls back to it on failure, and releases it — and the protection covers
   statement-backed entities as well as registry entries, so one `VAL-001` never
   poisons the entries after it. Statements PostgreSQL cannot type without a
   value (`select @a + @b`, `where @x is null`) fail to describe and report
   `VAL-001`; they would fail at run time too — cast the parameter.
5. **Calendar tokens on write.** A `date` property writes the value's **own**
   calendar date and a `time` property its own time of day — never the instant
   converted to UTC first (23:30 at −05:00 is still the 28th). Under
   `BindsTemporalsNatively` a `date` binds natively or as `yyyy-MM-dd` text and a
   `time` always as `HH:mm:ss.fffffff` text (the database infers the type from the
   comparison; a `time` value carries no kind). A `datetimeoffset` write keeps its
   offset (`+00:00`, never `Z`) and binds natively as the instant; `datetime` is
   the §7.9 rule unchanged.
6. **Marker detection by text** accepts the drivers' spellings: PostgreSQL renders
   `timestamptz` in the session's zone (`2026-10-17 08:49:19.462397+07`, minutes
   omitted when zero, `+05:30` otherwise, fewer fraction digits on a typed
   precision); the SQL Server driver spells ` +00:00` after a space with seven
   fraction digits. **Marker detection by column type** (Go) consults the driver's
   type name case-insensitively: go-mssqldb reports `DATETIME`, `DATETIME2`,
   `SMALLDATETIME` (markerless) and `DATETIMEOFFSET`; pgx reports `TIMESTAMP`
   (markerless) and `TIMESTAMPTZ`. A property with a registered handler is never
   judged — the handler is the legacy-column escape.
7. **Array parameters, the edges.** A `null` element is `NULL` in the literal (PDO)
   or a pointer-element slice (Go); an **empty** collection is typed from the
   declared element type (Go) or left to the comparison (PDO's `{}`); elements pass
   through the conversion pipeline first, so enums bind by name, temporals by the
   rules above, decimals as text (`numeric[]` relies on text input — untested). A
   nested collection or an object element is a binding error with no spec code.
8. **Connection strings and drivers.** PHP SQL Server takes a `sqlsrv:` **or**
   `odbc:` DSN verbatim and never emulates prepares (emulation would interpolate
   strings without the `N` prefix); PHP PostgreSQL a `pgsql:` DSN with credentials
   embedded. Go SQL Server parses the string with the driver's `msdsn` (URL, ADO,
   and `odbc:` forms) and sets `GuidConversion` so `uniqueidentifier` reads in RFC
   4122 order; Go PostgreSQL rewrites `@name` to `$n` inside its connector (first
   appearance numbers the parameter, names match case-insensitively, a repeated
   name shares one position, a placeholder without a value or a value without a
   placeholder is an error, SQL with no `@name` passes through untouched) — the
   seam gains no member for placeholders.
9. **Driver value shapes a port relies on.** PDO (`pgsql`): temporals, `numeric`,
   `uuid`, and arrays arrive as strings, `bytea` as a stream, `int2/4/8` as int,
   `float4/8` as float, `bool` as bool. go-mssqldb: `decimal`/`numeric`/`money`
   as ASCII `[]byte` (the converter accepts it), `datetime2` as a `time.Time`
   without marker, strings bind as `nvarchar`, `time.Time` as `datetimeoffset`;
   empty result sets of a batch are skipped. pgx: `numeric`, `uuid`, `time`, and
   unregistered types as `string`, `bytea` as `[]byte`, `timestamptz` in the local
   zone (`.UTC()` is the rule), `timestamp` as a UTC-labelled instant.
10. **Describing without executing, per driver.** SQLite: the column-origin API.
    SQL Server: `sp_describe_first_result_set @tsql, @params,
    @browse_information_mode = 1` with parameter types from
    `sp_describe_undeclared_parameters` (else `nvarchar(max)`); `source_table` /
    `source_column` give the origin, hidden browse columns are skipped, a `dbo`
    table is unqualified. PostgreSQL: prepare the (rewritten) statement as the
    unnamed server statement and resolve `TableOID`/`TableAttributeNumber`/
    `DataTypeOID` through one catalog query (`format_type(oid, -1)`, `regclass`,
    `attname`); `Table` is `oid::regclass::text`. A port whose driver exposes no
    origin table for a column (PDO `sqlsrv` `getColumnMeta`) falls back to the
    expression-column rule — nullable member or `-- notnull:` — and says so.
11. **The indexable length is a `CREATE TABLE` rule**, not a `StorageType` rule:
    string and bytes columns of a declared index render `nvarchar(450)` /
    `varbinary(450)` there, while `StorageType(property)` keeps the key-only rule
    (snapshots and diff are SQLite-only on these dialects). Two such key columns
    exceed SQL Server's 900-byte index limit — it creates with a warning; declare
    real lengths in migrations.
12. **Testing contract, precisely.** "A database per fixture" may be one database
    per test class with the schema reset per test, or one per test; both drop
    afterwards, including on failure. Beyond the list above every live leg pins
    that all three fetch modes load the same graph and that the run lock excludes
    a second connection and is released by rollback. The LocalDB default applies
    only where the driver can address a LocalDB instance.
13. **Unquoted names in tooling-only paths** (the derived rollback's `drop view if
    exists <name>`, the generator's drop statements) stay as they are: those paths
    are SQLite-only by the deferred list. The migration *actions* (`DropView`,
    `RecreateView`, `DropIndex`) render through the seam and quote.
14. **`bytes` bind as binary parameters, chosen by the token** (owner ruling,
    ADR-0040 add.1). A value whose property's metadata type token is `bytes`
    binds as the driver's binary parameter on every dialect — generated CRUD, key
    reads, criteria, and registry queries alike. Where the language's static type
    says it (C# `byte[]`, Go `[]byte`), the driver does so already; where one
    string type holds text and bytes alike (PHP), the binder chooses `PDO::PARAM_LOB`
    from the token, and a registry args property declares its token the way an
    entity property does (`#[Column(type: ColumnType::Bytes)]`). A text binding
    refuses or corrupts bytes: SQLite stores TEXT, which a STRICT `BLOB` column
    refuses; PostgreSQL parses a `bytea` payload as escaped text (a backslash
    starts an escape); SQL Server receives a character string for `varbinary`.
    The elements of a native array parameter stay text inside the array literal
    (bytes arrays are outside, with the other array-typed columns). Pinned by the
    fixture's nullable `UserProfile.Avatar` (migration V0012),
    `crud-cases/bytes_round_trip` (a NUL, a backslash, 0xff, invalid UTF-8, and
    bytes that spell a `bytea` escape literal), and the round trip every live leg
    runs. A nullable bytes member is `byte[]?` (C#), `?string` with the `bytes`
    token (PHP), `*[]byte` (Go — a bare `[]byte` is required, like `string`).
15. **PostgreSQL: a cached plan after a schema change** (owner ruling, ADR-0040
    add.1; a per-implementation driver matter, never a seam member). A driver that
    prepares and caches statements on a long-lived connection meets SQLSTATE
    `0A000` "cached plan must not change result type" when the table under a
    cached statement changes (a column's type, say). Where the driver caches, the
    behaviour is configurable, and the default **retries the statement once, only
    when no transaction is active on the connection**: the failed statement ran in
    its own implicit transaction, so nothing of it happened. Inside a transaction
    PostgreSQL has already aborted it — the error surfaces unchanged and the caller
    must roll back. The other setting surfaces the error always. Go: pgx caches
    every statement with arguments and invalidates the entry on any error but never
    retries, so the dialect's connection does —
    `postgres.New(postgres.WithCachedPlanPolicy(postgres.CachedPlanRetryOnce |
    postgres.CachedPlanSurface))`, retry by default, judged by the connection's
    transaction status; a statement the caller prepared explicitly is never
    retried (its server-side statement is what changed). C# is unaffected: Npgsql
    does not auto-prepare by default (`Max Auto Prepare=0`), so a connection string
    that turns it on is outside this rule. PHP is unaffected: PDO prepares each
    statement when the library runs it, and nothing prepared outlives the call.

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

The two non-SQLite dialects began **runtime-first** (ADR-0024/0025): session,
CRUD, criteria rendering, loading in every fetch mode, tracking, unit of work,
hooks, SchemaGuard, and the migration runner with the dialect's run lock. Since
ADR-0043 the migration tooling — snapshot, shadow, diff, derived rollbacks, and
force sync — works on them as on SQLite ("Migration tooling" below;
spec/migrations.md "Dialects").

## The seam, member by member

| Member | Purpose | Consulted by |
|---|---|---|
| `Name` | the dialect's name as the CLI spells it (`sqlite`, `sqlserver`, `postgres`; ADR-0043) — stamped into non-SQLite snapshots, compared on read (`MIG-014`) | snapshots, the diff, the shadow |
| `CreateConnection(connectionString)` | an unopened connection for the dialect's native connection string | the session, the runner, the CLI |
| `QuoteIdentifier(name)` | every relation and column name the library emits passes through it | renderer, generated CRUD, `Get`, DDL |
| `CreateTableSql(map)`, `CreateIndexSql(map)`, `CreateViewSql(map)` | metadata-rendered DDL, idempotent (`IF NOT EXISTS` or the dialect's guard) | `CreateTable`/`CreateView`, an object's initial migration |
| `CreateTableSql(schema)`, `CreateIndexSql(table, index)` | the same DDL from a **snapshot** shape (ADR-0043): storage types as recorded, one generated key inline as identity, any other key as the trailing constraint | the shadow's trusted baseline, the derived rollback (indexes), the generator |
| `CreateViewSql(view, body, materialized)`, `DropViewSql(view, materialized)` | a view from its neutral snapshot (name and body — the text `CreateViewSql(map)` renders for that shape), and its idempotent drop | the derived rollback, the shadow baseline, the `MIG-012` probe |
| `ViewDefinitionIsVerbatim` | true → `ViewDefinitionSql` returns the text as submitted and the `MIG-012` view guard compares normalized text; false → the engine reformats it (PostgreSQL) and the guard round-trips the expected body through a probe view (migrations.md "The view and trigger apply guard") | the runner |
| `InsertSql`, `UpdateSql`, `UpdateOnlySql`, `DeleteSql` | the generated CRUD of spec/crud.md | the session, the unit of work |
| `ReportsAffectedRowsBySelect` | true → the generated UPDATE and DELETE renderings end in a one-row, one-column select of the statement's own affected-row count, and the session judges that scalar instead of the driver's count (ADR-0040 add.3, Clarification 23) | the session's generated writes — never a registry command |
| `SupportsStatementBatching` | true → this implementation's driver for the dialect can carry several statements in one message and hand back each statement's result; false → under `Batching = Auto` the planned messages run one statement at a time (ADR-0044, "Statement batching" below) | the session's batching realization (`BatchingInEffect`) |
| `MaxParametersPerMessage` | the most bound parameters one message may carry, or none | the batching plan (spec/session.md "Batching", rule 3) |
| `LimitOffsetClause(limit, offset)` | the paging clause from pre-bound placeholders | the renderer |
| `PagingRequiresOrderBy` | a paged select without orderings gains `order by (select null)` | the renderer |
| `SupportsRowValueIn` | false → composite subquery membership rewrites as a correlated EXISTS (spec/query-ast.md) | the renderer |
| `SelectSql(ast, bind)` | the one rendering entry point; delegates to the reference renderer unless the dialect disagrees | criteria queries, loading |
| `SupportsArrayParameters` | true → a collection-typed argument binds as **one** array parameter with the SQL untouched (spec/session.md) | the parameter binder |
| `BindsTemporalsNatively` | true → temporals bind as native UTC values, not ISO-8601 strings | the type converter |
| `SupportsMaterializedViews`, `SupportsProcedures`, `SupportsTransactionalDdl` | capability flags; a `[MaterializedView]` on a dialect without them is `DDL-002` | `CreateView`, SchemaGuard, snapshots |
| `ColumnsInfoSql`, `IndexesInfoSql`, `ViewDefinitionSql` | introspection queries, parameter `@relation`, fixed result shapes (below; the columns shape carries `generated` since ADR-0043) | SchemaGuard, force sync, the shadow, the `MIG-012` guard |
| `IsDeclaredTypeCompatible(declaredType, type)` | the `VAL-011` table | SchemaGuard |
| `StorageType(map, property)` | the declared type `CREATE TABLE` emits, spelled exactly as `ColumnsInfoSql` reports it back (ADR-0043): the map tells whether the column is a key or belongs to a declared index (SQL Server's indexable length), the property's storage facets override the defaults | DDL, snapshots, diff, sync |
| `BeginMigrationRunLock(connection)` | one transaction held for the whole run, made exclusive the dialect's way | the runner |
| `VersionTableSql` | idempotent DDL for `schema_version` — same columns everywhere | the runner |
| `RenameTableSql`, `RenameColumnSql`, `AddColumnSql`, `DropColumnSql`, `DropTableSql`, `DropIndexSql` | the typed migration actions and the derived rollback | the runner, the down deriver, force sync |
| `CreateTriggerSql(relation, onView, trigger)` | the create statements of a trigger from its neutral declaration (ADR-0042): header, wrapper, token expansions; no drop; `DDL-005` for a form the dialect lacks | trigger steps, the derived rollback, `CreateTriggers`, the `MIG-012` guard (as the expectation) |
| `DropTriggerSql(relation, name)` | drops a trigger idempotently with whatever the dialect created beside it | trigger steps, the derived rollback, `CreateTriggers` |
| `TriggerDefinitionSql` | introspection: `@relation` = the trigger name, one column `definition`, one row per statement the dialect created — each, normalized, equal to the matching `CreateTriggerSql` statement | the `MIG-012` guard on trigger steps, the shadow replayer |

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
| `ReportsAffectedRowsBySelect` | false | **true** | false |
| `ViewDefinitionIsVerbatim` | true | true | **false** (`pg_get_viewdef` reformats) |
| `SupportsStatementBatching` | false (every implementation) | **true** (C# on `net10.0`; Go; PHP) | **true** in C# on `net10.0` and Go; **false** in PHP (`pdo_pgsql`) |
| `MaxParametersPerMessage` | none | **2098** | none |
| `Name` | `sqlite` | `sqlserver` | `postgres` |

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

### Update and delete — the affected-row count (spec/crud.md)

The generated full-row update, column-list update (the hook-stamped and
soft-delete forms are column-list updates), and delete differ between SQLite
and PostgreSQL only by quoting, and the session judges the driver's affected-row
count (spec/crud.md: zero rows is `CRUD-010` or `CRUD-001`).

SQL Server reports the count itself (`ReportsAffectedRowsBySelect`, ADR-0040
add.3): a table whose `AFTER` trigger lacks `SET NOCOUNT ON` makes the driver
add the trigger's rows to the statement's (Clarification 22), so every generated
UPDATE and DELETE renders as one batch

```sql
set nocount on; <statement>; select @@rowcount; set nocount off
```

— the counts suppressed, the write, the statement's own count as a one-row,
one-column result the session reads as a scalar, and the session setting
restored, because `SET NOCOUNT` outlives the batch on SQL Server and a registry
command on the same connection must keep the driver's count. Not `OUTPUT`: it is
refused without `INTO` on a triggered table (error 334). The `<statement>` is
the SQLite rendering under SQL Server quoting; `conformance/crud-sql/` pins
every form on every dialect. Inserts are unchanged: `scope_identity()` is already
trigger-safe.

Only the generated writes are affected. A registry command (`Execute`) returns
the driver's count on every dialect — hand-written SQL is the author's, and the
author of a trigger-bearing command adds its own `SET NOCOUNT ON`.

### Triggers (spec/migrations.md "Triggers", ADR-0042)

A trigger reaches the dialect as a neutral declaration — name, timing, events,
update-of columns, one body with the tokens `{new: cols}`, `{old: cols}`,
`{now}` — and the dialect renders **everything else**: the header, the
granularity, the wrapper, the token expansions. The create statements:

| | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| statements | one `create trigger` **per event** — single-event: the declared name; several: `<name>_<event>` (SQLite triggers are single-event) | one `create trigger`, the events listed `insert, update, delete` | `create or replace function "<name>_fn"() returns trigger language plpgsql as $$ … $$`, then `create trigger "<name>" … execute function "<name>_fn"()` |
| header | `create trigger <n> <before\|after\|instead of> <event>[ of a, b] on <t> for each row` | `create trigger [n] on [t] <after\|instead of> <events> as` | `create trigger "n" <before\|after\|instead of> <events joined " or ", "update" carrying " of "a", "b"> on "t" for each row execute function "n_fn"()` |
| wrapper | `\nbegin\n<body>\nend` | `\nbegin\n    set nocount on;\n[    if not (update([a]) or update([b])) return;\n]<body>\nend` | the function body `\nbegin\n<body>\n    return null;\nend\n` for `after`; for `before`/`instead of`: `    if tg_op = 'DELETE' then return old; end if;\n    return new;` |
| `{new: a, b}` | `(select new.a as a, new.b as b)` | `inserted` | `(select new."a" as "a", new."b" as "b")` |
| `{old: a, b}` | the same over `old.` | `deleted` | the same over `old` |
| `{now}` | `strftime('%Y-%m-%dT%H:%M:%fZ', 'now')` | `todatetimeoffset(sysutcdatetime(), 0)` | `now()` |
| `before` | native | **`DDL-005`** | native |
| `instead of` on a table | **`DDL-005`** (views only) | native | **`DDL-005`** (views only) |
| `instead of` on a view | native | native | native |
| update-of | `update of a, b` | the prologue `if not (update([a]) or update([b])) return;` — `if exists (select 1 from inserted) and not (…) return;` when the declaration also fires on delete, where `update()` is always false | `update of "a", "b"` on the update event (`after update of "a", "b" or delete`) |
| drop | `drop trigger if exists <n>`, `<n>_insert`, `<n>_update`, `<n>_delete` (every possible per-event name) | `if object_id(N'n', N'TR') is not null drop trigger [n]` | `drop trigger if exists "n" on "t"`, `drop function if exists "n_fn"()` |

The body is trimmed and inserted verbatim between the wrapper's lines; identifiers
inside it are the author's. `set nocount on` and the `return` are rendering,
never the author's. SQL Server's `create trigger` must open its batch, so the drop
is a separate statement and `create or alter` is not used (SQL Server 2016 SP1).
`conformance/trigger-sql/` pins every form above on every dialect, the `DDL-005`
refusals included.

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
| drop column | `alter table T drop column C` | one batch (ADR-0043): `declare @df sysname = (select dc.name from sys.default_constraints dc join sys.columns c on c.object_id = dc.parent_object_id and c.column_id = dc.parent_column_id where dc.parent_object_id = object_id(N'T') and c.name = N'C'); if @df is not null exec(N'alter table [T] drop constraint ' + quotename(@df)); alter table [T] drop column [C]` — a `not null … default` addition leaves a system-named default constraint behind, and `drop column` refuses while it exists (error 5074) | `alter table "T" drop column "C"` |
| drop table | `drop table T` | `drop table [T]` | `drop table "T"` |
| drop index | `drop index I` | `drop index [I] on [T]` (index names are table-scoped) | `drop index "I"` (schema-scoped) |

The snapshot-driven renderings (ADR-0043; the SQLite forms are `SnapshotDdl`'s
reference text):

| From a snapshot | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| table | `create table if not exists T (\n    c <type>[ NOT NULL]…\n) STRICT`, a lone generated key as `INTEGER PRIMARY KEY`, other keys as the trailing `primary key (…)` | `if object_id(N'T', N'U') is null\ncreate table [T] (\n    [c] <type> identity(1,1) primary key \| <type>[ not null]…[,\n    primary key ([a], [b])]\n)` | `create table if not exists "T" (\n    "c" <type> generated by default as identity primary key \| <type>[ not null]…[,\n    primary key ("a", "b")]\n)` |
| index | `create [unique ]index if not exists I on T (c[ desc], …)` | `if not exists (select 1 from sys.indexes where name = N'I' and object_id = object_id(N'T'))\ncreate [unique ]index [I] on [T] ([c][ desc], …)` | `create [unique ]index if not exists "I" on "T" ("c"[ desc], …)` |
| view | `create view if not exists V as\n<body>` | `create or alter view [V] as\n<body>` | `create or replace view "V" as\n<body>`; materialized: `create materialized view if not exists "V" as\n<body>` |
| drop view | `drop view if exists V` | `drop view if exists [V]` (2016+) | `drop view if exists "V"`; materialized: `drop materialized view if exists "V"` |

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

What `CREATE TABLE` emits per neutral type token (spec/metadata-model.md), what
introspection reports back, and what snapshots, the diff generator, and force
sync compare — one spelling, the introspection's (ADR-0043).

| Token | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| `int16` | `INTEGER` | `smallint` | `smallint` |
| `int32` | `INTEGER` | `int` | `integer` |
| `int64` | `INTEGER` | `bigint` | `bigint` |
| `bool` | `INTEGER` | `bit` | `boolean` |
| `decimal` | `TEXT` | `decimal(38,9)`; a `Precision`/`Scale` facet renders `decimal(p,s)` | `numeric` (unconstrained); a facet renders `numeric(p,s)` |
| `double` | `REAL` | `float` | `double precision` |
| `float` | `REAL` | `real` | `real` |
| `string`, JSON, handler types | `TEXT` | `nvarchar(max)`; a key column **or a column of a declared index** `nvarchar(450)` (the indexable length — `nvarchar(max)` cannot be an index key, error 1919); a `Length` facet renders `nvarchar(n)` and wins over both | `text`; a `Length` facet renders `character varying(n)` |
| `guid` | `TEXT` | `uniqueidentifier` | `uuid` |
| `bytes` | `BLOB` | `varbinary(max)`; a key column or a column of a declared index `varbinary(450)` (as for strings); a `Length` facet `varbinary(n)` | `bytea` (no length; a facet is ignored) |
| `datetime`, `datetimeoffset` | `TEXT` | `datetimeoffset` | `timestamp with time zone` |
| `date` | `TEXT` | `date` | `date` |
| `time` | `TEXT` | `time` | `time without time zone` |
| `enum_text` | `TEXT` | `nvarchar(100)` | `text` |
| `enum_int` | `INTEGER` | `int` | `integer` |

PostgreSQL types are spelled the way `format_type` reports them, SQL Server's
the way the columns query reconstructs them (`decimal(38,9)`, no space), so
introspected and rendered types compare cleanly. The **storage facets**
(ADR-0043, metadata-model.md "Storage facets") are dialect-neutral size
declarations the dialect renders its own way — `Length` for strings and bytes,
`Precision`/`Scale` for decimals; SQLite's storage classes have none and ignore
them. Whether a declared length fits an engine's index limits is the author's
responsibility, as for composite `nvarchar(450)` keys.

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

- **Columns**: `name, type, notnull, pk, generated` in column order, `pk` the key
  ordinal (0 = not part of the key), `generated` whether the database generates
  the value (ADR-0043: SQL Server `is_identity`, PostgreSQL `attidentity`,
  SQLite the rowid alias — a lone `INTEGER` primary key); `type` is the declared
  type spelled as the storage type table above (SQL Server reconstructs
  `(precision,scale)` and `(length|max)`; PostgreSQL uses `format_type`).
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
       cast(isnull(pk.key_ordinal, 0) as bigint) as [pk],
       cast(c.is_identity as bigint) as [generated]
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
       ) then 1 else 0 end)::bigint as pk,
       (case when a.attidentity in ('a', 'd') then 1 else 0 end)::bigint as generated
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
SchemaGuard only asks whether the column is part of the key.) The SQLite
reference query is now

```sql
select name, type, "notnull", pk,
       case when pk = 1 and upper(type) = 'INTEGER'
                 and (select count(*) from pragma_table_info(@relation) where pk > 0) = 1
            then 1 else 0 end as generated
from pragma_table_info(@relation)
```

- **Trigger definition** (ADR-0042): `@relation` is the declared trigger name;
  one column `definition`, one row per statement the dialect created for it;
  no rows when absent. SQLite returns the stored text of the declared name and
  its per-event forms (`name in (@relation, @relation || '_insert', …)`,
  ordered by name) — SQLite keeps the text as submitted, the leading keywords
  recased; SQL Server `sys.sql_modules.definition` joined to `sys.triggers`
  (the text as submitted); PostgreSQL **reconstructs** both statements from the
  catalog in the dialect's own spelling — the function from `pg_proc.prosrc`
  (the body verbatim) and the trigger from `pg_trigger.tgtype` (bit 2 before,
  bit 64 instead of, else after; bits 4/16/8 insert/update/delete) and
  `tgattr` (the update-of columns through `pg_attribute`), internal triggers
  excluded — so that each row, whitespace-collapsed, equals the matching
  `CreateTriggerSql` statement. That equality is the symmetric contract every
  live leg checks.

```sql
-- SQLite, trigger definition
select sql as definition from sqlite_master where type = 'trigger'
and name in (@relation, @relation || '_insert', @relation || '_update', @relation || '_delete') order by name

-- SQL Server, trigger definition
select m.definition as [definition] from sys.sql_modules m join sys.triggers t on t.object_id = m.object_id
where t.object_id = object_id(@relation)

-- PostgreSQL, trigger definition
select 'create or replace function "' || p.proname || '"() returns trigger language plpgsql as $$' || p.prosrc || '$$' as definition
from pg_trigger t
join pg_proc p on p.oid = t.tgfoid
where t.tgname = @relation and not t.tgisinternal
union all
select 'create trigger "' || t.tgname || '" '
    || case when (t.tgtype & 2) <> 0 then 'before' when (t.tgtype & 64) <> 0 then 'instead of' else 'after' end
    || ' '
    || array_to_string(array_remove(array[
           case when (t.tgtype & 4) <> 0 then 'insert' end,
           case when (t.tgtype & 16) <> 0 then 'update' || coalesce((
               select ' of ' || string_agg('"' || a.attname || '"', ', ' order by k.ord)
               from unnest(t.tgattr::smallint[]) with ordinality as k(attnum, ord)
               join pg_attribute a on a.attrelid = t.tgrelid and a.attnum = k.attnum), '') end,
           case when (t.tgtype & 8) <> 0 then 'delete' end], null), ' or ')
    || ' on "' || c.relname || '" for each row execute function "' || p.proname || '"()' as definition
from pg_trigger t
join pg_class c on c.oid = t.tgrelid
join pg_proc p on p.oid = t.tgfoid
where t.tgname = @relation and not t.tgisinternal
```

## Statement batching (ADR-0044)

The plan — which statements share a message — is the session's and the same
everywhere (spec/session.md "Batching"); this section is the realization: how a
planned message reaches the database. A realization may always take **more**
round trips than the plan's messages (sending a statement its driver cannot
carry in a message on its own), never fewer, and never reorders.

| | SQLite | SQL Server | PostgreSQL |
|---|---|---|---|
| C# (`net10.0`) | sequential — `Microsoft.Data.Sqlite` has no batch (`CanCreateBatch` false) | `DbBatch` → `SqlBatch`: one TDS message, one RPC per statement, each statement's text exactly as unbatched | `DbBatch` → `NpgsqlBatch`: the statements pipelined, one `Sync` |
| C# (`netstandard2.0`) | sequential | sequential (no `DbBatch` type) | sequential |
| Go | sequential (modernc has no batch) | one multi-statement text, placeholders renamed apart | pgx `Batch` / `SendBatch` on the session's connection |
| PHP | sequential (`pdo_sqlite` prepares one statement) | one multi-statement text with `nextRowset()`, placeholders renamed apart | sequential — measured: `pdo_pgsql` refuses several commands in a prepared statement (42601), and its emulated form returns only the last result (`nextRowset` is IM001) |

- **Sequential** means the planned message's statements run one at a time, in
  order, each judged before the next runs, reusing one prepared command per
  distinct statement text within the call. `SupportsStatementBatching` is false
  for it; on SQLite there is no network round trip to save and the reuse is the
  gain.
- **Reading results.** The statements that return a result set — an insert
  whose key the database generates (`returning <key>`, SQL Server's
  `select cast(scope_identity() as bigint)`) and, on SQL Server, every generated
  update and delete (`select @@rowcount`) — return exactly one each, one row and
  one column, in statement order; the session reads them in that order. Every
  other statement's count is the driver's count **for that statement**
  (ADO.NET `DbBatchCommand.RecordsAffected`, pgx's command tag per queued
  query). Plan rule 2 keeps a registry command's own result sets behind every
  result the session reads in its message, so they are never mistaken for one.
- **Multi-statement text** (Go and PHP on SQL Server): the message is one
  command text, its statements joined by `;` in order, sent as one
  `sp_executesql`; each statement's placeholders are renamed apart by the
  session before joining — `@name` in the *i*-th statement (from 0) becomes
  `@p<i>_name` (literals and comments untouched, as for IN-list expansion), and
  the parameters are bound under the new names. SQL Server caps one
  `sp_executesql` at 2100 parameters, two of them its own — hence
  `MaxParametersPerMessage` 2098, applied by every implementation so the plan
  stays the same. A statement whose own count the driver cannot tell apart from
  its neighbours' (a registry command's count among other counted statements)
  is sent on its own.
- **A statement the driver cannot carry in a message**: Npgsql refuses more than
  one SQL statement in one batch command, so the C# realization sends a
  registry command whose SQL holds several statements on its own (the
  generated statements are single statements on PostgreSQL).
- **Transactions.** Every batched call runs in a transaction (the session's or
  its own), so a message is never a set of separately committed statements; a
  PostgreSQL pipeline's implicit transaction and SQL Server's per-RPC autocommit
  never come into play.

## Connection strings and the CLI

A dialect takes its driver's **native** connection string; the SQLite dialect also
accepts a bare file path and the reference's `Data Source=<path>` form. The CLI
selects the dialect by name — `--dialect sqlite|sqlserver|postgres`, default
`sqlite` — for every database command; an unknown name refuses before anything
opens. For `shadow` on SQL Server or PostgreSQL, `--db` is the **admin**
connection (CREATE DATABASE rights): the throwaway `simpleorm_shadow_<hex>` is
created and dropped there; on SQLite it is ignored (ADR-0043).

## Migration tooling (ADR-0043)

The rules live in spec/migrations.md "Dialects"; this is the seam side.

- **The shadow host** is a tooling contract beside the seam, never consulted
  at runtime (C# `IShadowHost`; a port shapes it its own way and says so in
  its §10): *provision* a throwaway database from the admin connection and
  return the connection string that opens it; *drop* it afterwards, failure
  included; and an optional *trigger read-back* — the inverse of
  `CreateTriggerSql` over the catalog's text, present where the catalog is
  lossless. SQLite: a temp file; read-back from `sqlite_master`. SQL Server:
  `create database [simpleorm_shadow_<hex>]`, dropped after `alter database
  … set single_user with rollback immediate`; **no read-back** (`inserted`
  has lost the token's column list). PostgreSQL: `create database
  "simpleorm_shadow_<hex>"`, `drop database … with (force)` after the pools
  are cleared; read-back from the reconstructed `TriggerDefinitionSql` rows —
  the header's `before|after|instead of`, `insert`/`update[ of "a", "b"]`/
  `delete` joined by ` or `, the relation; the function body between `begin`
  and the rendered `return` tail, `(select new."a" as "a", …)` folded to
  `{new: a, …}`, `now()` to `{now}`.
- **The view guard's mode** is `ViewDefinitionIsVerbatim`. The probe (false):
  `savepoint simpleorm_probe`; `CreateViewSql("simpleorm_expect_<hex>",
  <expected body>, false)`; `ViewDefinitionSql` on the probe and on the live
  view, compared trimmed; `DropViewSql(probe)`; `release savepoint` — or
  `rollback to savepoint` when the probe fails to create, which counts as
  drift. Standard savepoint grammar: the probe path runs only on dialects
  that have it; a dialect with another grammar and a reformatting catalog
  would need a member (§7.25).
- **The stamp.** `Name` is what `snapshot`/`shadow` write as `"dialect"` for
  every dialect but `sqlite` and what every reader compares (`MIG-014`).

## Deferred — the same list in every implementation

Named here so that no implementation attempts it ahead of the reference:

- **A SQL Server trigger read-back** for the shadow's self-check: the stored
  text renders `{new: cols}` as `inserted`, so the declaration is not
  recoverable; the shadow snapshots triggers from the step there (ADR-0043 —
  the tooling itself is no longer deferred on any dialect).
- **A per-dialect body override** for a trigger (a body that must say what the
  tokens cannot, such as aborting a write): not provided — the three dialects
  and the fixture need none; a real case decides its form.
- The `MIG-012` view guard against `sys.sql_modules` output: built (text,
  normalized) and pinned by `migrations-cases/sqlserver/`, unrun until the
  owner's engine is back; against `pg_get_viewdef` it is the probe round trip,
  verified live.
- Procedure execution (`SupportsProcedures` is declared, dormant); `REFRESH
  MATERIALIZED VIEW`; `jsonb` and array-typed columns; schema qualifiers beyond
  the metadata's `Schema` — all Level 4. Batching itself is built (ADR-0044,
  "Statement batching"); **batched reads** (a read queued in an explicit batch,
  filled at send) and **multi-row inserts** (`values (…), (…)` — only for keys the
  database does not generate: no engine promises the order of a multi-row
  `returning`) are deferred by the owner's ruling.
- `migrations-cases/` and `cases/` fixtures per dialect: the conformance folders
  that reach a database run on SQLite; `ast/` is the per-dialect pin.
- Found by the ports, not yet ruled on: `time`- and `decimal`-typed array
  parameters; composite `nvarchar(450)` keys exceeding SQL Server's 900-byte index
  limit (declare real lengths in migrations). (`bytes` writes through untyped
  drivers, pgx's statement cache after a schema change, and go-mssqldb's untyped
  NULL are ruled — Clarifications 14, 15, and 16.)

**Measured on a SQL Server engine** (ADR-0040 add.2, 2026-10-19 — SQL Server
2016 on the owner's LAN, reached by every implementation's live leg):

- **The SQL Server live legs ran green in all three implementations** — C#
  through `Microsoft.Data.SqlClient`, PHP through `pdo_odbc` with ODBC Driver
  17, Go through go-mssqldb — after the fixes recorded as Clarifications 17–21.
  Clarification 16 (typed nulls) is verified: the NULL avatar write passed in
  Go as built, exposed the reference's untyped `DBNull` (fixed, Clarification
  18), and needed nothing in PHP.
- **`RowsAffected` under triggers** is measured (Clarification 22) and ruled
  (Clarification 23, ADR-0040 add.3): the SQL Server dialect reports the count
  itself (`ReportsAffectedRowsBySelect`, "Update and delete — the affected-row
  count" above), in every implementation.


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
version-column concurrency on update and delete (SQL Server additionally under
an `AFTER` trigger without `SET NOCOUNT ON`, Clarification 23); the declared
fixture trigger created from metadata, firing once per updated row, and read
back through `TriggerDefinitionSql` as exactly its rendering, plus a
multi-event, narrowed, `before` declaration where the dialect has the form
(ADR-0042); transaction scopes; explicit,
batch, and link loading; a subselect include over a paged root; the composite-key
subselect (through the EXISTS rewrite on SQL Server, native row-value IN on
PostgreSQL); migrations up, re-run, and down under the run lock (with `Down()`
overrides); the `VAL-020` refusal on a markerless column; the bytes round trip
of Clarification 14 (insert, column-list and full update, a criteria and a
registry match, a NULL). PostgreSQL additionally: array parameters filled and
empty, a temporal criteria comparison against `timestamptz`, a materialized view
created and read, and — where the driver caches statements — the cached-plan
policy of Clarification 15 in and out of a transaction. Both legs, where the
implementation batches (ADR-0044): a batched flush whose generated keys flow
across messages, an explicit batch carrying every operation, a stale version
in the middle of a message (`CRUD-010`, nothing applied, every instance as it
was), and a failing statement in the middle of a message (the provider's error,
nothing applied).

## Clarifications

Answers to the four port gap lists of ADR-0040 (PHP and Go, SQL Server and
PostgreSQL — 42 questions, consolidated), each the answer the implementations now
embody. No rule above changed; the ADR records the three reference deviations the
same lists surfaced. Clarifications 14, 15, and 16 are owner rulings on items the
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
11. ~~**The indexable length is a `CREATE TABLE` rule**, not a `StorageType`
    rule~~ — **superseded by ADR-0043**: `StorageType(map, property)` applies
    the indexable length to a key column and to a column of a declared index
    alike, so snapshots, diff, and sync see what `CREATE TABLE` emits and
    introspection reports (`nvarchar(450)`). Two such key columns exceed SQL
    Server's 900-byte index limit — it creates with a warning; declare real
    lengths with a `Length` facet.
12. **Testing contract, precisely.** "A database per fixture" may be one database
    per test class with the schema reset per test, or one per test; both drop
    afterwards, including on failure. Beyond the list above every live leg pins
    that all three fetch modes load the same graph and that the run lock excludes
    a second connection and is released by rollback. The LocalDB default applies
    only where the driver can address a LocalDB instance.
13. ~~**Unquoted names in tooling-only paths** stay as they are~~ — **superseded
    by ADR-0043**: the derived rollback, the shadow baseline, and the generator
    render through `DropViewSql`, `CreateViewSql(name, body, …)`,
    `CreateTableSql(schema)`, and `CreateIndexSql(table, index)`, quoting as
    the dialect does. The migration *actions* (`DropView`, `RecreateView`,
    `DropIndex`) rendered through the seam already.
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
    PostgreSQL has already aborted it, and the error surfaces. The other setting
    surfaces the error always. **A surfaced cached-plan error is `TX-002`**, never
    the bare provider error: it names the statement, keeps the provider error as
    its wrapped cause, and says what to do — inside a transaction, roll it back
    and rerun the whole transaction; outside one, rerun the statement; or open a
    new session. **Who clears the cache:** the driver, per connection, on the
    error itself — the library holds no statement cache of its own. That is why
    the rerun works: it re-prepares against the new table shape. The retry-once
    default handles the no-transaction case transparently; inside a transaction
    the application's rerun is the fix. Go: pgx caches every statement with
    arguments and invalidates the entry on any error (`Conn.exec`'s deferred
    `Invalidate`, `Rows.Close`) but never retries, so the dialect's connection
    does —
    `postgres.New(postgres.WithCachedPlanPolicy(postgres.CachedPlanRetryOnce |
    postgres.CachedPlanSurface))`, retry by default, judged by the connection's
    transaction status; a statement the caller prepared explicitly is never
    retried (its server-side statement is what changed). C# is unaffected: Npgsql
    does not auto-prepare by default (`Max Auto Prepare=0`), so a connection string
    that turns it on is outside this rule. PHP is unaffected: PDO prepares each
    statement when the library runs it, and nothing prepared outlives the call.
16. **Typed nulls from the metadata token** (owner ruling, ADR-0040 add.1). A
    NULL parameter carries the SQL type its property's metadata token names,
    never an untyped null: a driver that types parameters from the bound value
    (go-mssqldb announces an untyped nil as `nvarchar(1)`, which SQL Server
    refuses to convert into `varbinary` — error 257) then declares the right
    type. The binder chooses it, metadata-driven and dialect-neutral, for
    generated CRUD, key reads, criteria, and registry queries (a registry
    argument's token comes from its declared type). Go: `TypeConverter.
    ToParameter` turns a NULL into `mapping.TypedNull(token)` — `[]byte(nil)`
    for `bytes`, `sql.NullInt16/32/64`, `NullFloat64`, `NullBool`, `NullTime`
    for the numeric, `bool`, and instant tokens, `sql.NullString` for `string`,
    `decimal`, `guid`, `date`, `time`, and text enums (SQL Server converts a
    character type implicitly into each of those), an untyped nil only for a
    custom handler type; the snapshot form (`ToDatabase`) keeps the plain nil.
    SQLite and PostgreSQL bind exactly what they bound before — every typed null
    reaches them as SQL NULL (modernc binds a nil `[]byte` as NULL; pgx encodes
    both forms as NULL and the server types the parameter). Built and pinned by
    unit tests on the binder's output; **unverified against SQL Server** until an
    engine exists — the SQL Server leg's NULL bytes write (insert and update) is
    the verification. C# and PHP: whether ADO.NET's untyped `DBNull` and PDO
    ODBC's `PARAM_NULL` reach SQL Server as a character type is the same live
    question, answered by the same NULL write in their SQL Server legs.

17. **A typed getter follows the provider's field type, never the target's alone**
    (ADR-0040 add.2, the first SQL Server run). The reference compiled
    `GetInt64` for a `long` target, and SQL Server returns `count(...)` as
    `int`: the typed getter threw where the converter would have widened. The
    rule: a provider-native read (`GetInt64`, `GetString`, …) is chosen only when
    the reader's field type **is** the target type; otherwise the value goes
    through the converter, which widens integers losslessly (`int16` → `int32` →
    `int64`) and refuses the rest with `MAP-031`. A plan is keyed by the
    provider's field types as well as the column names, so one statement
    returning different shapes on two dialects compiles two plans.
18. **Typed nulls are the reference's rule too.** Clarification 16's Go rule
    held the first time the SQL Server legs ran: ADO.NET's untyped `DBNull`
    reaches SQL Server as `nvarchar`, which it refuses into `varbinary(max)`
    (the NULL avatar write). The reference now binds every null with the
    `DbType` of what its non-null value would have bound as — `Binary` for
    `bytes`, `String` for a temporal bound as text, the numeric type otherwise —
    derived from the property's metadata type, at every bind site (CRUD, the
    column-list update, hooks, criteria, registry arguments). A value whose
    storage type is unknowable (a handler-registered type) stays untyped. PDO
    ODBC's `PARAM_NULL` needed nothing: it binds a typeless NULL the server
    accepts.
19. **PDO ODBC conventions (PHP, SQL Server through `pdo_odbc`).** ODBC Driver
    17 converts parameter text on the client with the SQL Server ODBC parser,
    which takes only `yyyy-mm-dd hh:mm:ss[.fffffff] [+|-]hh:mm` — a `T`
    separator or a `Z` is SQLSTATE 22018 — and it fetches every column as
    character data, so a `varbinary` arrives as upper-case hexadecimal text. The
    PHP port carries both as an optional dialect capability beside the mirrored
    seam, `PdoConventions` (`temporalText()`: `Iso8601` | `Odbc`;
    `binaryText()`: `Raw` | `Hex`), declared by `SqlServerDialect` and honoured
    by the converter: temporals bind in the ODBC form (the stored value is the
    same instant; what the server returns and what the markers mean are
    unchanged), and a hexadecimal binary read is decoded. `pdo_sqlite` and
    `pdo_pgsql` declare nothing and keep ISO-8601 and raw bytes. A `pdo_sqlsrv`
    driver, where installed, converts server-side and would declare nothing
    either.
20. **Globalization-invariant mode and SqlClient.** `Microsoft.Data.SqlClient`
    refuses to open any connection in a process running with
    `InvariantGlobalization=true` ("Globalization Invariant Mode is not
    supported"): collations need culture data. The reference's CLI and tests
    therefore run with it off; an application using the SQL Server dialect must
    too. SQLite and PostgreSQL are unaffected.
21. **An indexed `nvarchar` column reads back as `nvarchar(450)`** from
    introspection, because that is what `CREATE TABLE` writes (and, since
    ADR-0043, what `StorageType` says too); a live expectation of
    `nvarchar(max)` for `users.display_name` (indexed) was the Go leg's
    mistake, not the dialect's.
22. **`RowsAffected` under a trigger, measured** (SQL Server 2016, 2026-10-19):
    a one-row `UPDATE` on a table whose `AFTER UPDATE` trigger inserts two log
    rows without `SET NOCOUNT ON` reports **3** rows affected through the
    driver; `SET NOCOUNT ON; <update>; SELECT @@ROWCOUNT` reports **1**; an
    `OUTPUT inserted.id` without `INTO` is refused outright on the triggered
    table (error 334). The hazard for the "zero rows affected" rule of
    spec/crud.md runs the other way from a false `CRUD-010`: an `AFTER` trigger
    fires once per statement, rows or not, so a **stale** version-checked write
    affects no row of its own and the driver still reports the trigger's two —
    the conflict passes for a success, in every implementation as they stood.
    Ruled by Clarification 23.
23. **The SQL Server dialect reports the affected-row count itself** (owner
    ruling, ADR-0040 add.3, closing item 3). `ReportsAffectedRowsBySelect` is a
    seam member — true on SQL Server, false on SQLite and PostgreSQL, whose
    drivers report the statement's own count triggers or not — under which the
    dialect's `UpdateSql`, `UpdateOnlySql`, and `DeleteSql` render
    `set nocount on; <statement>; select @@rowcount; set nocount off` and the
    session reads the batch's first scalar as the count on exactly the paths
    that judge it (the full-row, column-list, dirty, hook-stamped, and
    soft-delete updates; the by-key and version-checked deletes), leaving the
    `CRUD-010`/`CRUD-001` rules untouched. The trailing `set nocount off`
    exists because the setting is session-scoped: without it every later
    registry command on the connection would report no count at all (−1
    through ADO.NET). Registry commands keep the driver's count on every
    dialect. Not chosen:
    `OUTPUT` (refused on triggered tables), leaving the burden with trigger
    authors (Hibernate's stance — a silent lost update is not a convenience),
    and keying the rendering off declared triggers (Level 4's triggers in the
    mapping may narrow it to the tables that need it; today every generated
    write pays one round trip of the same size). Pinned by
    `conformance/crud-sql/` (every generated form, every dialect) and by each
    implementation's SQL Server leg: an `AFTER UPDATE, DELETE` trigger inserting
    two log rows without `SET NOCOUNT ON`, under which the version-checked
    update, column-list update, and delete of one row succeed, a stale update
    and delete are `CRUD-010`, a by-key delete of the gone row is `CRUD-001`,
    and a registry command on the same session still reports its own count.
24. **Reading the reported count** (the PHP and Go port gap lists of ADR-0040
    add.3). The count is the first column of the first row of the first result
    that has columns; a count-only result a driver surfaces before it (PDO can)
    is skipped. It is read as an integer whatever the driver's shape (go-mssqldb
    returns `@@rowcount` as a 32-bit integer, `pdo_odbc` as a string). A batch
    that yields no such row, or a non-numeric value, is a dialect bug, not a
    conflict: the session fails with an internal or driver error naming the
    statement — the absence is **never zero**, so it is never `CRUD-010` or
    `CRUD-001`. There is no error code for it, as for any other broken seam
    contract.
25. **`SET NOCOUNT` residue after an aborted batch.** The trailing `set nocount
    off` runs only when the batch completes. A batch-aborting error inside the
    wrapped write (a deadlock victim, a conversion error under `XACT_ABORT ON`)
    leaves `NOCOUNT` on for the connection, so a registry command run on that
    session afterwards reports no count (−1 through ADO.NET, 0 or −1
    elsewhere) until the next generated write restores it or the connection is
    reset. The session does not repair this: the error is the caller's to
    handle, and the generated writes themselves are unaffected (they never read
    the driver's count on SQL Server). Hand-written SQL that must count under
    such a failure adds its own `set nocount off`.
26. **The trigger of the SQL Server leg** is the same shape in every
    implementation: `AFTER UPDATE, DELETE` on `transactions`, two single-row
    `INSERT` statements into a log table, no `SET NOCOUNT ON`, created with
    `create trigger` as the batch's only statement (`create or alter` needs
    2016 SP1) and dropped afterwards. Six statements fire it — the full update,
    the column-list update, the stale update, the stale delete, the entity
    delete, and the by-key delete of the gone row (an `AFTER` trigger fires
    once per statement, rows or not) — so the log holds 12 rows; the stale
    instance's version stays what it was.
27. **Overload-less ports name the snapshot forms** (ADR-0043): where a
    language has no overloading, `CreateTableSql(schema)`,
    `CreateIndexSql(table, index)`, and `CreateViewSql(view, body,
    materialized)` are `createTableSqlFromSchema` / `createIndexSqlFromSchema`
    / `createViewSqlFromBody` (PHP) and `CreateTableFromSchemaSQL` /
    `CreateIndexFromSchemaSQL` / `CreateViewFromBodySQL` (Go); the map forms
    keep their names. The shadow host's shape is the port's (PHP a
    `ShadowHost` interface with `canReadTriggers()`/`readTrigger()`, Go a
    `ShadowHost` plus an optional `TriggerReadBack`), recorded in its §10.
    The five introspection columns may be read by ordinal or by name.

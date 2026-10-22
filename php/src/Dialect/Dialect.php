<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Query\SelectAst;

/**
 * The seam between the provider-neutral core and a database provider (§7.25).
 * The member set is exactly the C# `IDialect`'s — same names, same contracts —
 * so a fix or a new member in one implementation is findable in the other.
 * Minimal and capability-based: members are added only when a second dialect
 * needs them, and SQLite's renderings then stay byte-identical (they are pinned
 * by conformance and hashed into migration checksums).
 */
interface Dialect
{
    /**
     * The dialect's name as the CLI spells it — `sqlite`, `sqlserver`, `postgres` (ADR-0043): stamped into every non-SQLite
     * snapshot (`"dialect"`, format v3) and compared by every snapshot reader (`MIG-014`).
     */
    public function name(): string;

    /** An unopened-until-used PDO connection for the given connection string (a PDO DSN, or a bare SQLite path). */
    public function createConnection(string $connectionString): PDO;

    /** Quotes one identifier (ADR-0024). SQLite returns the name unquoted — the reference renderings stay byte-identical. */
    public function quoteIdentifier(string $identifier): string;

    // --- generated DDL and CRUD from the EntityMap (ADR-0011, ADR-0008 add.3) ------------

    /** `CREATE TABLE IF NOT EXISTS` for a table-backed map: column types from the metadata, NOT NULL from nullability, the key per its strategy, STRICT on SQLite. */
    public function createTableSql(EntityMap $map): string;

    /**
     * `CREATE INDEX IF NOT EXISTS` for each declared index.
     *
     * @return list<string>
     */
    public function createIndexSql(EntityMap $map): array;

    /** `CREATE (MATERIALIZED) VIEW` from the map's defining SQL. */
    public function createViewSql(EntityMap $map): string;

    /**
     * The same table DDL from a **snapshot** shape (ADR-0043; spec/dialects.md "Migration-action DDL"): storage types as
     * recorded, one generated key inline as the dialect's identity, any other key as the trailing constraint. Backs the
     * shadow's trusted baseline. (PHP has no overloading: the C# `CreateTableSql(schema)` is a separate name here.)
     */
    public function createTableSqlFromSchema(TableSchema $schema): string;

    /**
     * One index of a snapshot (ADR-0043) — `CreateIndexSql(table, index)`: the derived rollback restores previous
     * indexes with it, the generator emits added ones.
     */
    public function createIndexSqlFromSchema(string $table, TableSchemaIndex $index): string;

    /**
     * A view from its neutral snapshot parts (ADR-0043) — `CreateViewSql(view, body, materialized)`: the text
     * {@see self::createViewSql()} renders for the same name and body. Backs the derived rollback, the shadow baseline,
     * and the `MIG-012` probe.
     */
    public function createViewSqlFromBody(string $view, string $body, bool $materialized): string;

    /** The idempotent drop of a view (ADR-0043); `$materialized` names a materialized view where the dialect has them. */
    public function dropViewSql(string $view, bool $materialized): string;

    /**
     * Whether {@see self::viewDefinitionSql()} returns the view text as submitted (SQLite, SQL Server) or reformats it
     * (PostgreSQL's `pg_get_viewdef`) — decides the `MIG-012` view guard's mode: normalized text, or the probe round trip.
     */
    public function viewDefinitionIsVerbatim(): bool;

    /** The generated INSERT (§7.14): explicit column list, every non-generated column, RETURNING the key when the database generates it. Placeholders are `@<column>`. */
    public function insertSql(EntityMap $map): string;

    /** The generated full-row UPDATE by key (§7.15/§7.16): version set to `version + 1` and required in the WHERE when mapped. */
    public function updateSql(EntityMap $map): string;

    /**
     * The update-by-column-list UPDATE (ADR-0028): the SET holds exactly `$properties` (validated by the
     * session: mapped, non-key, non-version, non-generated, no repeats); version and WHERE rules as `updateSql`.
     *
     * @param list<PropertyMap> $properties
     */
    public function updateOnlySql(EntityMap $map, array $properties): string;

    /** The generated DELETE by key; with `$checkVersion`, the WHERE also requires the version (§7.16). */
    public function deleteSql(EntityMap $map, bool $checkVersion): string;

    /**
     * Whether the generated UPDATE and DELETE renderings end in a one-row, one-column select of the statement's own
     * affected-row count, which the session reads instead of the driver's count (ADR-0040 add.3, spec/dialects.md
     * Clarification 23). True on SQL Server, whose driver adds an AFTER trigger's rows to the statement's; never
     * consulted for registry commands or inserts.
     */
    public function reportsAffectedRowsBySelect(): bool;

    // --- criteria rendering (§10.4, ADR-0020) ---------------------------------------------

    /** Renders the limit/offset clause from pre-bound parameter names; either may be null. */
    public function limitOffsetClause(?string $limitParameter, ?string $offsetParameter): string;

    /**
     * Renders the SELECT for a criteria query AST. `$bindParameter` binds a value
     * (with the mapped property it compares against, so per-column conversion
     * applies; null for paging values) and returns its placeholder; call it in
     * render order. Delegate to `AnsiSelectRenderer` unless this dialect's SQL
     * disagrees with the reference rendering.
     *
     * @param callable(mixed $value, ?PropertyMap $property): string $bindParameter
     */
    public function selectSql(SelectAst $select, callable $bindParameter): string;

    /** Whether the paging clause is only legal after ORDER BY (ADR-0024; SQL Server). */
    public function pagingRequiresOrderBy(): bool;

    /** Whether `(a, b) in (select …)` row-value membership parses (ADR-0024; SQLite: yes). */
    public function supportsRowValueIn(): bool;

    // --- capability flags ------------------------------------------------------------------

    /** Real array parameters (§7.12; SQLite: no — the IN-expansion strategy applies). */
    public function supportsArrayParameters(): bool;

    /** Temporals bind as native values instead of §7.9 ISO-8601 strings (ADR-0025; SQLite: no). */
    public function bindsTemporalsNatively(): bool;

    public function supportsMaterializedViews(): bool;

    public function supportsProcedures(): bool;

    /** Whether DDL participates in transactions (§7.23; SQLite: yes). */
    public function supportsTransactionalDdl(): bool;

    // --- introspection (§7.25) --------------------------------------------------------------

    /** Columns of a relation: parameter `@relation`; result columns `name, type, notnull, pk, generated` (empty = relation missing; `generated`, ADR-0043). */
    public function columnsInfoSql(): string;

    /** A view's stored create DDL: parameter `@relation`; no rows = view absent (backs `MIG-012` and view snapshots). */
    public function viewDefinitionSql(): string;

    /** A table's explicitly created indexes: parameter `@relation`; columns `index_name, unique, seqno, column, desc`, ordered by index then position. */
    public function indexesInfoSql(): string;

    /** The declared-type → neutral-type compatibility table (§7.19 `VAL-011`). */
    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool;

    /**
     * The storage (declared) type a mapped property renders to — what CREATE TABLE emits and what `columnsInfoSql`
     * reports back, spelled identically (ADR-0017, ADR-0043). The map says whether the column is a key or belongs to a
     * declared index (SQL Server's indexable length); the property's storage facets override the defaults.
     */
    public function storageType(EntityMap $map, PropertyMap $property): string;

    // --- migrations (§7.23) -----------------------------------------------------------------

    /** Starts the migration run lock on the connection: one transaction held for the whole run (SQLite: `BEGIN IMMEDIATE`). */
    public function beginMigrationRunLock(PDO $connection): void;

    /** Idempotent DDL for the `schema_version` table (ADR-0024): same columns everywhere, dialect-native types and guard. */
    public function versionTableSql(): string;

    /** Typed migration actions render through the dialect (ADR-0024); SQLite's strings are frozen — recorded checksums hash them. */
    public function renameTableSql(string $fromName, string $toName): string;

    public function renameColumnSql(string $table, string $fromName, string $toName): string;

    public function addColumnSql(string $table, string $column, string $storageType, bool $nullable, ?string $defaultSql): string;

    public function dropColumnSql(string $table, string $column): string;

    public function dropTableSql(string $table): string;

    public function dropIndexSql(string $table, string $index): string;

    // --- triggers (Level 4, ADR-0042; spec/dialects.md "Triggers") ------------------------------

    /**
     * The create statements of a trigger from its neutral declaration: the header, the granularity, the wrapper, and
     * the token expansions (`{new: …}`, `{old: …}`, `{now}`) are the dialect's; no drop. One statement on SQL Server,
     * one per event on SQLite, the function then the trigger on PostgreSQL. `DDL-005` for a form the dialect lacks
     * (`before` on SQL Server, `instead of` on a table elsewhere).
     *
     * @param string $relation the table or view the trigger is on (unquoted)
     * @return list<string>
     */
    public function createTriggerSql(string $relation, bool $onView, TriggerDefinition $trigger): array;

    /**
     * Drops a trigger idempotently with whatever the dialect created beside it (SQLite: every possible per-event name;
     * PostgreSQL: the trigger then its function).
     *
     * @return list<string>
     */
    public function dropTriggerSql(string $relation, string $name): array;

    /**
     * Introspection: parameter `@relation` is the **trigger name**; one column `definition`, one row per statement the
     * dialect created for it — each, normalized, equal to the matching {@see self::createTriggerSql()} statement; no
     * rows when absent (backs the `MIG-012` trigger guard and the shadow's read-back).
     */
    public function triggerDefinitionSql(): string;
}

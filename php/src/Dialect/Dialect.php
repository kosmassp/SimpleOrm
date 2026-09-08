<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
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

    /** Columns of a relation: parameter `@relation`; result columns `name, type, notnull, pk` (empty = relation missing). */
    public function columnsInfoSql(): string;

    /** A view's stored create DDL: parameter `@relation`; no rows = view absent (backs `MIG-012` and view snapshots). */
    public function viewDefinitionSql(): string;

    /** A table's explicitly created indexes: parameter `@relation`; columns `index_name, unique, seqno, column, desc`, ordered by index then position. */
    public function indexesInfoSql(): string;

    /** The declared-type → neutral-type compatibility table (§7.19 `VAL-011`). */
    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool;

    /** The storage (declared) type a mapped property renders to — what CREATE TABLE emits (ADR-0017). */
    public function storageType(PropertyMap $property): string;

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
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use PDOException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\AnsiSelectRenderer;
use SimpleOrm\Query\SelectAst;
use Throwable;

/**
 * SQL Server implementation of {@see Dialect} over PDO (ADR-0024, spec/dialects.md): identifiers in
 * `[brackets]`, `offset … rows fetch next … rows only` paging that requires an ORDER BY, no row-value `IN`
 * (the renderer rewrites composite membership as a correlated EXISTS), `datetimeoffset` for every temporal,
 * `create or alter view`, `sp_rename`, and a migration run lock held as one transaction around
 * `sp_getapplock`. Runtime-first like the reference: snapshot, shadow, diff, and force-sync tooling stays
 * SQLite-only (spec/dialects.md "Deferred").
 *
 * **Drivers (CODING-STANDARD §10).** The connection string is a PDO DSN taken verbatim: `sqlsrv:` (`pdo_sqlsrv`)
 * or `odbc:` (`pdo_odbc` with "ODBC Driver 17/18 for SQL Server") — the SQL is the same over either. Prepared
 * statements are **never emulated**, over `odbc:` as over `sqlsrv:`: PDO core rewrites the library's `:name`
 * placeholders to the positional `?` that `pdo_odbc` needs, so no emulation switch is required, and emulation
 * would be worse (it interpolates string values as plain `'…'` literals with no `N` prefix, mangling non-ASCII
 * text on the way to an `nvarchar` column). The `ATTR_EMULATE_PREPARES` attribute is set best-effort — a driver
 * that refuses the attribute is left at its default, which is native preparation.
 *
 * Declared-type compatibility, storage types, and introspection are pure functions of the strings in
 * spec/dialects.md; only {@see self::createConnection()} and {@see self::beginMigrationRunLock()} touch a server.
 */
final class SqlServerDialect implements Dialect, PdoConventions
{
    /** The indexable `nvarchar`/`varbinary` length of a key column (SQL Server's 900-byte index key limit, spec/dialects.md). */
    private const KEY_LENGTH = '450';

    public function createConnection(string $connectionString): PDO
    {
        $pdo = new PDO($connectionString);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $pdo->setAttribute(PDO::ATTR_STRINGIFY_FETCHES, false);
        $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);

        try {
            $pdo->setAttribute(PDO::ATTR_EMULATE_PREPARES, false);
        } catch (PDOException) {
            // The driver does not expose the switch; it prepares natively already.
        }

        return $pdo;
    }

    /** `[name]`, a `]` doubled (spec/dialects.md "Capability flags"). */
    public function quoteIdentifier(string $identifier): string
    {
        return '[' . str_replace(']', ']]', $identifier) . ']';
    }

    /** A Unicode string literal, every single quote doubled. */
    private static function nLiteral(string $value): string
    {
        return "N'" . str_replace("'", "''", $value) . "'";
    }

    public function createTableSql(EntityMap $map): string
    {
        $table = (string) $map->relationName;
        $indexed = self::indexedColumns($map);

        $lines = [];
        foreach ($map->properties as $property) {
            $column = $this->quoteIdentifier($property->columnName);
            if ($map->keyStrategy === KeyStrategy::DatabaseGenerated && $property->key) {
                $lines[] = $column . ' ' . self::columnType($property, $indexed) . ' identity(1,1) primary key';
                continue;
            }

            $line = $column . ' ' . self::columnType($property, $indexed);
            if (!$property->nullable) {
                $line .= ' not null';
            }

            if ($map->keyStrategy === KeyStrategy::ClientGuid && $property->key) {
                $line .= ' primary key';
            }

            $lines[] = $line;
        }

        if ($map->keyStrategy === KeyStrategy::Natural && $map->keyProperties !== []) {
            $lines[] = 'primary key (' . implode(', ', array_map(
                fn (PropertyMap $k): string => $this->quoteIdentifier($k->columnName),
                $map->keyProperties,
            )) . ')';
        }

        return 'if object_id(' . self::nLiteral($table) . ", N'U') is null\n"
            . 'create table ' . $this->quoteIdentifier($table) . " (\n    " . implode(",\n    ", $lines) . "\n)";
    }

    /** @return list<string> */
    public function createIndexSql(EntityMap $map): array
    {
        $table = (string) $map->relationName;

        return array_map(
            fn (EntityIndex $index): string => 'if not exists (select 1 from sys.indexes where name = ' . self::nLiteral($index->name)
                . ' and object_id = object_id(' . self::nLiteral($table) . "))\n"
                . 'create ' . ($index->unique ? 'unique ' : '') . 'index ' . $this->quoteIdentifier($index->name)
                . ' on ' . $this->quoteIdentifier($table) . ' (' . implode(', ', array_map(
                    fn (IndexColumn $c): string => $this->quoteIdentifier($c->columnName) . ($c->descending ? ' desc' : ''),
                    $index->columns,
                )) . ')',
            $map->indexes,
        );
    }

    public function createViewSql(EntityMap $map): string
    {
        return 'create or alter view ' . $this->quoteIdentifier((string) $map->relationName) . " as\n" . $map->definingSql;
    }

    /**
     * The generated key is read back with `select cast(scope_identity() as bigint)` in the same batch — never an
     * `OUTPUT` clause, which fails on tables with triggers (spec/dialects.md "Generated keys").
     */
    public function insertSql(EntityMap $map): string
    {
        $columns = array_values(array_filter($map->properties, static fn (PropertyMap $p): bool => !$p->generated));
        $sql = 'insert into ' . $this->quoteIdentifier((string) $map->relationName)
            . ' (' . implode(', ', array_map(fn (PropertyMap $c): string => $this->quoteIdentifier($c->columnName), $columns)) . ')'
            . ' values (' . implode(', ', array_map(static fn (PropertyMap $c): string => '@' . $c->columnName, $columns)) . ')';

        if ($map->keyStrategy === KeyStrategy::DatabaseGenerated) {
            $sql .= '; select cast(scope_identity() as bigint)';
        }

        return $sql;
    }

    public function updateSql(EntityMap $map): string
    {
        // Generated non-key columns are database-owned: never in SET (mirrors the insert exclusion).
        return $this->renderUpdate($map, array_values(array_filter(
            $map->properties,
            static fn (PropertyMap $p): bool => !$p->key && !$p->version && !$p->generated,
        )));
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        return $this->renderUpdate($map, $properties);
    }

    /** @param list<PropertyMap> $set */
    private function renderUpdate(EntityMap $map, array $set): string
    {
        $assignments = array_map(
            fn (PropertyMap $p): string => $this->quoteIdentifier($p->columnName) . ' = @' . $p->columnName,
            $set,
        );
        if ($map->versionProperty !== null) {
            $version = $this->quoteIdentifier($map->versionProperty->columnName);
            $assignments[] = $version . ' = ' . $version . ' + 1';
        }

        $sql = 'update ' . $this->quoteIdentifier((string) $map->relationName)
            . ' set ' . implode(', ', $assignments)
            . ' where ' . $this->keyPredicate($map);
        if ($map->versionProperty !== null) {
            $sql .= ' and ' . $this->quoteIdentifier($map->versionProperty->columnName) . ' = @' . $map->versionProperty->columnName;
        }

        return $sql;
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        $sql = 'delete from ' . $this->quoteIdentifier((string) $map->relationName) . ' where ' . $this->keyPredicate($map);
        if ($checkVersion && $map->versionProperty !== null) {
            $sql .= ' and ' . $this->quoteIdentifier($map->versionProperty->columnName) . ' = @' . $map->versionProperty->columnName;
        }

        return $sql;
    }

    private function keyPredicate(EntityMap $map): string
    {
        return implode(' and ', array_map(
            fn (PropertyMap $k): string => $this->quoteIdentifier($k->columnName) . ' = @' . $k->columnName,
            $map->keyProperties,
        ));
    }

    /** The placeholders arrive bound WHERE first, then limit, then offset — the clause only reorders them (spec/dialects.md "Paging clause"). */
    public function limitOffsetClause(?string $limitParameter, ?string $offsetParameter): string
    {
        return match (true) {
            $limitParameter !== null && $offsetParameter !== null => "offset {$offsetParameter} rows fetch next {$limitParameter} rows only",
            $limitParameter !== null => "offset 0 rows fetch next {$limitParameter} rows only",
            $offsetParameter !== null => "offset {$offsetParameter} rows",
            default => '',
        };
    }

    public function selectSql(SelectAst $select, callable $bindParameter): string
    {
        return AnsiSelectRenderer::selectSql($this, $select, $bindParameter);
    }

    public function pagingRequiresOrderBy(): bool
    {
        return true;
    }

    public function supportsRowValueIn(): bool
    {
        return false;
    }

    public function supportsArrayParameters(): bool
    {
        return false;
    }

    public function bindsTemporalsNatively(): bool
    {
        return false;   // SQL Server converts the ISO-8601 string to datetimeoffset implicitly (spec/dialects.md "Temporals")
    }

    /** ODBC Driver 17 converts temporal text on the client and takes only the space-separated, explicit-offset form (ADR-0040 add.2). */
    public function temporalText(): TemporalText
    {
        return TemporalText::Odbc;
    }

    /** `pdo_odbc` fetches a `varbinary` as its hexadecimal text (ADR-0040 add.2). */
    public function binaryText(): BinaryText
    {
        return BinaryText::Hex;
    }

    public function supportsMaterializedViews(): bool
    {
        return false;
    }

    public function supportsProcedures(): bool
    {
        return true;   // dormant: no execution machinery before Level 4
    }

    public function supportsTransactionalDdl(): bool
    {
        return true;
    }

    /**
     * The introspection result columns carry the spec's names (`name, type, notnull, pk`; `index_name, unique,
     * seqno, column, desc`; `definition`) on every non-SQLite dialect of every implementation — SQLite's pragmas
     * name them so already — so a consumer may read by name (PDO does) or by ordinal (the reference does)
     * (spec/dialects.md "Introspection result shapes").
     */
    public function columnsInfoSql(): string
    {
        return <<<'SQL'
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
            SQL;
    }

    public function viewDefinitionSql(): string
    {
        return <<<'SQL'
            select m.definition as [definition] from sys.sql_modules m join sys.views v on v.object_id = m.object_id
            where v.object_id = object_id(@relation)
            SQL;
    }

    public function indexesInfoSql(): string
    {
        return <<<'SQL'
            select i.name as [index_name], cast(i.is_unique as bigint) as [unique], cast(ic.key_ordinal - 1 as bigint) as [seqno],
                   c.name as [column], cast(ic.is_descending_key as bigint) as [desc]
            from sys.indexes i
            join sys.index_columns ic on ic.object_id = i.object_id and ic.index_id = i.index_id and ic.is_included_column = 0
            join sys.columns c on c.object_id = ic.object_id and c.column_id = ic.column_id
            where i.object_id = object_id(@relation) and i.type > 0 and i.is_primary_key = 0 and i.is_unique_constraint = 0
            order by i.name, ic.key_ordinal
            SQL;
    }

    /**
     * The `VAL-011` table of spec/dialects.md. The declared type is reduced to its base name (trimmed, lowercased,
     * any parenthesized length or precision removed); an empty base name is compatible with nothing (SQL Server
     * always declares a type), and a custom token only through a handler, which the caller checks first.
     */
    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool
    {
        $base = strtolower(trim(preg_replace('/\([^)]*\)/', '', $declaredType) ?? $declaredType));

        $compatible = match ($base) {
            'int', 'tinyint' => [ColumnType::Int32, ColumnType::Int64, ColumnType::Int16, ColumnType::EnumInt],
            'bigint' => [ColumnType::Int64, ColumnType::EnumInt],
            'smallint' => [ColumnType::Int16, ColumnType::Int32, ColumnType::Int64, ColumnType::EnumInt],
            'bit' => [ColumnType::Bool],
            'decimal', 'numeric', 'money', 'smallmoney' => [ColumnType::Decimal],
            'float' => [ColumnType::Double],
            'real' => [ColumnType::Float],
            'nvarchar', 'varchar', 'nchar', 'char' => [ColumnType::String, ColumnType::EnumText],
            'ntext', 'text' => [ColumnType::String],
            'varbinary', 'binary', 'image' => [ColumnType::Bytes],
            'uniqueidentifier' => [ColumnType::Guid],
            'datetime', 'datetime2', 'smalldatetime' => [ColumnType::DateTime],
            'datetimeoffset' => [ColumnType::DateTime, ColumnType::DateTimeOffset],
            'date' => [ColumnType::DateTime, ColumnType::Date],
            'time' => [ColumnType::Time],
            default => [],
        };

        return in_array($type, $compatible, true);
    }

    /**
     * The declared type `CREATE TABLE` emits for one property: the key columns of a `string`/`bytes` property
     * get the indexable `(450)` length. {@see self::createTableSql()} extends that to columns of declared indexes,
     * which only the whole map can tell.
     */
    public function storageType(PropertyMap $property): string
    {
        return self::columnType($property, []);
    }

    /**
     * @param array<string, true> $indexed column names that belong to a declared index (they need the indexable length too)
     */
    private static function columnType(PropertyMap $property, array $indexed): string
    {
        $indexable = $property->key || isset($indexed[$property->columnName]);

        return match ($property->type) {
            ColumnType::Int16 => 'smallint',
            ColumnType::Int32, ColumnType::EnumInt => 'int',
            ColumnType::Int64 => 'bigint',
            ColumnType::Bool => 'bit',
            ColumnType::Decimal => 'decimal(38, 9)',
            ColumnType::Double => 'float',
            ColumnType::Float => 'real',
            ColumnType::Guid => 'uniqueidentifier',
            ColumnType::Bytes => $indexable ? 'varbinary(' . self::KEY_LENGTH . ')' : 'varbinary(max)',
            ColumnType::DateTime, ColumnType::DateTimeOffset => 'datetimeoffset',
            ColumnType::Date => 'date',
            ColumnType::Time => 'time',
            ColumnType::EnumText => 'nvarchar(100)',
            // string, JSON, and handler (custom) types
            ColumnType::String, ColumnType::Custom => $indexable ? 'nvarchar(' . self::KEY_LENGTH . ')' : 'nvarchar(max)',
        };
    }

    /** @return array<string, true> the column names every declared index covers */
    private static function indexedColumns(EntityMap $map): array
    {
        $columns = [];
        foreach ($map->indexes as $index) {
            foreach ($index->columns as $column) {
                $columns[$column->columnName] = true;
            }
        }

        return $columns;
    }

    /**
     * One transaction for the whole run, made exclusive by an application lock that lives and dies with it
     * (spec/dialects.md "The run lock"). A refused lock rolls the transaction back before the error surfaces, so
     * the connection is left as it was.
     */
    public function beginMigrationRunLock(PDO $connection): void
    {
        $connection->exec('BEGIN TRANSACTION');

        try {
            $connection->exec(
                "declare @rc int; exec @rc = sp_getapplock @Resource = N'simpleorm:migrations', @LockMode = 'Exclusive', "
                . "@LockOwner = 'Transaction', @LockTimeout = 60000; "
                . "if @rc < 0 raiserror(N'the SimpleOrm migration lock is held by another run', 16, 1)",
            );
        } catch (Throwable $failure) {
            try {
                $connection->exec('ROLLBACK');
            } catch (PDOException) {
                // The transaction is already gone; the original failure is the one to report.
            }

            throw $failure;
        }
    }

    public function versionTableSql(): string
    {
        return <<<'SQL'
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
            SQL;
    }

    public function renameTableSql(string $fromName, string $toName): string
    {
        return 'exec sp_rename ' . self::nLiteral($fromName) . ', ' . self::nLiteral($toName);
    }

    public function renameColumnSql(string $table, string $fromName, string $toName): string
    {
        return 'exec sp_rename ' . self::nLiteral("{$table}.{$fromName}") . ', ' . self::nLiteral($toName) . ", 'COLUMN'";
    }

    public function addColumnSql(string $table, string $column, string $storageType, bool $nullable, ?string $defaultSql): string
    {
        return 'alter table ' . $this->quoteIdentifier($table) . ' add ' . $this->quoteIdentifier($column) . ' ' . $storageType
            . ($nullable ? '' : ' not null')
            . ($defaultSql === null ? '' : ' default ' . $defaultSql);
    }

    public function dropColumnSql(string $table, string $column): string
    {
        return 'alter table ' . $this->quoteIdentifier($table) . ' drop column ' . $this->quoteIdentifier($column);
    }

    public function dropTableSql(string $table): string
    {
        return 'drop table ' . $this->quoteIdentifier($table);
    }

    /** Index names are table-scoped on SQL Server, so the statement names the table. */
    public function dropIndexSql(string $table, string $index): string
    {
        return 'drop index ' . $this->quoteIdentifier($index) . ' on ' . $this->quoteIdentifier($table);
    }
}

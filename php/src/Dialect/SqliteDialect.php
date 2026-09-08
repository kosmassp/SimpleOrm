<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\AnsiSelectRenderer;
use SimpleOrm\Query\SelectAst;

/**
 * SQLite implementation of {@see Dialect} backed by PDO's `pdo_sqlite` driver
 * (ADR-0003). The reference dialect (CLAUDE.md §7.25): every rendered DDL/CRUD/
 * action string below is byte-identical to `dotnet/src/SimpleOrm.Sqlite/SqliteDialect.cs`
 * — checksums and conformance pins depend on it.
 */
final class SqliteDialect implements Dialect
{
    public function createConnection(string $connectionString): PDO
    {
        $pdo = new PDO('sqlite:' . self::resolvePath($connectionString));
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $pdo->setAttribute(PDO::ATTR_EMULATE_PREPARES, false);
        $pdo->setAttribute(PDO::ATTR_STRINGIFY_FETCHES, false);
        $pdo->setAttribute(PDO::ATTR_DEFAULT_FETCH_MODE, PDO::FETCH_ASSOC);

        return $pdo;
    }

    /** Accepts a bare file path, a `sqlite:<path>` PDO DSN, or C#-style `Data Source=<path>`. */
    private static function resolvePath(string $connectionString): string
    {
        if (stripos($connectionString, 'sqlite:') === 0) {
            return substr($connectionString, strlen('sqlite:'));
        }

        foreach (explode(';', $connectionString) as $part) {
            $part = trim($part);
            if (stripos($part, 'Data Source=') === 0) {
                return trim(substr($part, strlen('Data Source=')));
            }
        }

        return $connectionString;
    }

    /** Unquoted (ADR-0024): snake_case names need nothing, and the reference renderings stay byte-identical. */
    public function quoteIdentifier(string $identifier): string
    {
        return $identifier;
    }

    public function createTableSql(EntityMap $map): string
    {
        $lines = [];
        foreach ($map->properties as $property) {
            if ($map->keyStrategy === KeyStrategy::DatabaseGenerated && $property->key) {
                // The exact spelling that makes the column the rowid alias.
                $lines[] = $property->columnName . ' INTEGER PRIMARY KEY';
                continue;
            }

            $line = $property->columnName . ' ' . self::columnType($property);
            if (!$property->nullable) {
                $line .= ' NOT NULL';
            }

            if ($map->keyStrategy === KeyStrategy::ClientGuid && $property->key) {
                $line .= ' PRIMARY KEY';
            }

            $lines[] = $line;
        }

        if ($map->keyStrategy === KeyStrategy::Natural && $map->keyProperties !== []) {
            $lines[] = 'primary key (' . implode(', ', array_map(
                static fn (PropertyMap $k): string => $k->columnName,
                $map->keyProperties,
            )) . ')';
        }

        return "create table if not exists {$map->relationName} (\n    " . implode(",\n    ", $lines) . "\n) STRICT";
    }

    /** @return list<string> */
    public function createIndexSql(EntityMap $map): array
    {
        return array_map(
            static fn (EntityIndex $index): string => 'create ' . ($index->unique ? 'unique ' : '') . 'index if not exists '
                . $index->name . ' on ' . $map->relationName . ' (' . implode(', ', array_map(
                    static fn (IndexColumn $c): string => $c->columnName . ($c->descending ? ' desc' : ''),
                    $index->columns,
                )) . ')',
            $map->indexes,
        );
    }

    public function createViewSql(EntityMap $map): string
    {
        return 'create view if not exists ' . $map->relationName . " as\n" . $map->definingSql;
    }

    public function insertSql(EntityMap $map): string
    {
        $columns = array_values(array_filter($map->properties, static fn (PropertyMap $p): bool => !$p->generated));
        $sql = 'insert into ' . $map->relationName
            . ' (' . implode(', ', array_map(static fn (PropertyMap $c): string => $c->columnName, $columns)) . ')'
            . ' values (' . implode(', ', array_map(static fn (PropertyMap $c): string => '@' . $c->columnName, $columns)) . ')';

        if ($map->keyStrategy === KeyStrategy::DatabaseGenerated) {
            $sql .= ' returning ' . $map->keyProperties[0]->columnName;
        }

        return $sql;
    }

    public function updateSql(EntityMap $map): string
    {
        // Generated non-key columns are database-owned: never in SET (mirrors
        // the insert exclusion).
        return self::renderUpdate($map, array_values(array_filter(
            $map->properties,
            static fn (PropertyMap $p): bool => !$p->key && !$p->version && !$p->generated,
        )));
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        return self::renderUpdate($map, $properties);
    }

    /** @param list<PropertyMap> $set */
    private static function renderUpdate(EntityMap $map, array $set): string
    {
        $assignments = array_map(
            static fn (PropertyMap $p): string => $p->columnName . ' = @' . $p->columnName,
            $set,
        );
        if ($map->versionProperty !== null) {
            $assignments[] = $map->versionProperty->columnName . ' = ' . $map->versionProperty->columnName . ' + 1';
        }

        $sql = 'update ' . $map->relationName
            . ' set ' . implode(', ', $assignments)
            . ' where ' . self::keyPredicate($map);
        if ($map->versionProperty !== null) {
            $sql .= ' and ' . $map->versionProperty->columnName . ' = @' . $map->versionProperty->columnName;
        }

        return $sql;
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        $sql = 'delete from ' . $map->relationName . ' where ' . self::keyPredicate($map);
        if ($checkVersion && $map->versionProperty !== null) {
            $sql .= ' and ' . $map->versionProperty->columnName . ' = @' . $map->versionProperty->columnName;
        }

        return $sql;
    }

    private static function keyPredicate(EntityMap $map): string
    {
        return implode(' and ', array_map(
            static fn (PropertyMap $k): string => $k->columnName . ' = @' . $k->columnName,
            $map->keyProperties,
        ));
    }

    public function limitOffsetClause(?string $limitParameter, ?string $offsetParameter): string
    {
        return match (true) {
            $limitParameter !== null && $offsetParameter !== null => "limit {$limitParameter} offset {$offsetParameter}",
            $limitParameter !== null => "limit {$limitParameter}",
            // SQLite requires LIMIT before OFFSET.
            $offsetParameter !== null => "limit -1 offset {$offsetParameter}",
            default => '',
        };
    }

    public function selectSql(SelectAst $select, callable $bindParameter): string
    {
        return AnsiSelectRenderer::selectSql($this, $select, $bindParameter);
    }

    public function pagingRequiresOrderBy(): bool
    {
        return false;
    }

    public function supportsRowValueIn(): bool
    {
        return true;
    }

    public function supportsArrayParameters(): bool
    {
        return false;
    }

    public function bindsTemporalsNatively(): bool
    {
        return false;   // TEXT storage: the ISO-8601 string IS the value (§7.9)
    }

    public function supportsMaterializedViews(): bool
    {
        return false;
    }

    public function supportsProcedures(): bool
    {
        return false;
    }

    public function supportsTransactionalDdl(): bool
    {
        return true;
    }

    public function columnsInfoSql(): string
    {
        return 'select name, type, "notnull", pk from pragma_table_info(@relation)';
    }

    public function viewDefinitionSql(): string
    {
        return "select sql from sqlite_master where type = 'view' and name = @relation";
    }

    public function indexesInfoSql(): string
    {
        return 'select il.name, il."unique", ii.seqno, ii.name, ii."desc" '
            . 'from pragma_index_list(@relation) il, pragma_index_xinfo(il.name) ii '
            . "where il.origin = 'c' and ii.key = 1 order by il.name, ii.seqno";
    }

    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool
    {
        $declared = strtoupper(trim($declaredType));
        if ($declared === '' || $declared === 'ANY') {
            return true;   // untyped: SQLite enforces nothing, nothing to contradict
        }

        return match ($declared) {
            'INT', 'INTEGER' => in_array(
                $type,
                [ColumnType::Int16, ColumnType::Int32, ColumnType::Int64, ColumnType::Bool, ColumnType::EnumInt],
                true,
            ),
            'REAL' => $type === ColumnType::Double || $type === ColumnType::Float,
            'BLOB' => $type === ColumnType::Bytes || $type === ColumnType::Guid,
            'TEXT' => in_array(
                $type,
                [
                    ColumnType::String, ColumnType::Decimal, ColumnType::Guid, ColumnType::DateTime,
                    ColumnType::DateTimeOffset, ColumnType::Date, ColumnType::Time, ColumnType::EnumText,
                ],
                true,
            ),
            default => false,
        };
    }

    public function storageType(PropertyMap $property): string
    {
        return self::columnType($property);
    }

    /** CLR → SQLite storage type per the §7.9 conventions (dates/decimals/GUIDs as TEXT). */
    private static function columnType(PropertyMap $property): string
    {
        return match ($property->type) {
            ColumnType::Int16, ColumnType::Int32, ColumnType::Int64, ColumnType::Bool, ColumnType::EnumInt => 'INTEGER',
            ColumnType::Double, ColumnType::Float => 'REAL',
            ColumnType::Bytes => 'BLOB',
            default => 'TEXT',   // string, decimal, DateTime/Offset, date, time, guid, enum_text, custom
        };
    }

    public function beginMigrationRunLock(PDO $connection): void
    {
        $connection->exec('BEGIN IMMEDIATE');
    }

    public function versionTableSql(): string
    {
        return <<<'SQL'
            create table if not exists schema_version (
                version      INTEGER NOT NULL,
                object       TEXT NOT NULL,
                description  TEXT NOT NULL,
                checksum     TEXT NOT NULL,
                applied_at   TEXT NOT NULL,
                execution_ms INTEGER NOT NULL,
                primary key (version, object)
            ) STRICT
            SQL;
    }

    // Migration-action DDL (ADR-0024): these strings are frozen — recorded checksums hash them.

    public function renameTableSql(string $fromName, string $toName): string
    {
        return "alter table {$fromName} rename to {$toName}";
    }

    public function renameColumnSql(string $table, string $fromName, string $toName): string
    {
        return "alter table {$table} rename column {$fromName} to {$toName}";
    }

    public function addColumnSql(string $table, string $column, string $storageType, bool $nullable, ?string $defaultSql): string
    {
        return "alter table {$table} add column {$column} {$storageType}"
            . ($nullable ? '' : ' not null')
            . ($defaultSql === null ? '' : ' default ' . $defaultSql);
    }

    public function dropColumnSql(string $table, string $column): string
    {
        return "alter table {$table} drop column {$column}";
    }

    public function dropTableSql(string $table): string
    {
        return 'drop table ' . $table;
    }

    public function dropIndexSql(string $table, string $index): string
    {
        return 'drop index ' . $index;
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * Renders DDL straight from a snapshot — what makes the recorded history
 * executable: the derived-rollback deriver restores columns and indexes from
 * it (ADR-0018), and a future trusted `shadow --from` baseline would rebuild
 * whole tables from it (ADR-0017).
 */
final class SnapshotDdl
{
    private function __construct()
    {
    }

    public static function createTableSql(TableSchema $schema): string
    {
        $sql = 'create table if not exists ' . $schema->name . ' (';
        $first = true;
        foreach ($schema->columns as $column) {
            $sql .= ($first ? "\n    " : ",\n    ") . $column->name . ' ';
            $first = false;
            if ($column->key && $column->generated) {
                $sql .= 'INTEGER PRIMARY KEY';   // the rowid alias spelling

                continue;
            }

            $sql .= $column->storageType;
            if (!$column->nullable) {
                $sql .= ' NOT NULL';
            }
        }

        $hasGeneratedKey = false;
        $plainKeys = [];
        foreach ($schema->columns as $column) {
            if ($column->key && $column->generated) {
                $hasGeneratedKey = true;
            } elseif ($column->key) {
                $plainKeys[] = $column->name;
            }
        }

        if ($plainKeys !== [] && !$hasGeneratedKey) {
            $sql .= ",\n    primary key (" . implode(', ', $plainKeys) . ')';
        }

        return $sql . "\n) STRICT";
    }

    /** The DDL for one index — used at the single-index granularity the diff and derived-rollback algorithms need. */
    public static function createIndexSql(string $objectName, TableSchemaIndex $index): string
    {
        $columns = array_map(
            static fn (TableSchemaIndexPart $part): string => $part->columnName . ($part->descending ? ' desc' : ''),
            $index->columns,
        );

        return 'create ' . ($index->unique ? 'unique ' : '') . 'index if not exists ' . $index->name
            . ' on ' . $objectName . ' (' . implode(', ', $columns) . ')';
    }

    /** @return list<string> every declared index of the schema, rendered */
    public static function createAllIndexSql(TableSchema $schema): array
    {
        return array_map(
            static fn (TableSchemaIndex $index): string => self::createIndexSql($schema->name, $index),
            $schema->indexes,
        );
    }
}

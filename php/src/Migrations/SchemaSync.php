<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use PDO;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * Force-sync planning (ADR-0013 add.3 / ADR-0017), mirrors `SchemaSync.cs`:
 * after migrations complete, the real database is compared against the model.
 * Additive fixes (missing tables, missing nullable columns, missing indexes on
 * new tables) are safe; deletions (extra columns) are gated behind an explicit
 * flag by the caller; type/nullability changes and non-nullable additions are
 * never auto-applied. Renames are never inferred.
 */
final class SchemaSync
{
    private function __construct()
    {
    }

    /** @param list<class-string> $tableEntities */
    public static function plan(PDO $connection, Dialect $dialect, EntityMapLoader $maps, array $tableEntities): SyncPlan
    {
        $plan = new SyncPlan();

        foreach ($tableEntities as $type) {
            $map = $maps->load($type);
            if ($map->kind !== RelationKind::Table) {
                continue;
            }

            /** @var string $relation table-kind maps always carry a relation name */
            $relation = $map->relationName;
            $live = self::readColumns($connection, $dialect, $relation);
            if ($live === []) {
                $plan->additive[] = $dialect->createTableSql($map);
                foreach ($dialect->createIndexSql($map) as $indexSql) {
                    $plan->additive[] = $indexSql;
                }

                continue;
            }

            foreach ($map->properties as $property) {
                $column = self::findColumn($live, $property->columnName);
                if ($column === null) {
                    if ($property->nullable) {
                        $plan->additive[] = $dialect->addColumnSql(
                            $relation,
                            $property->columnName,
                            $dialect->storageType($property),
                            true,
                            null,
                        );
                    } else {
                        $plan->unsupported[] = sprintf(
                            '%s.%s: adding a non-nullable column needs a default/backfill — write a migration',
                            $relation,
                            $property->columnName,
                        );
                    }

                    continue;
                }

                $expected = $dialect->storageType($property);
                if (strcasecmp($column['declaredType'], $expected) !== 0 || $column['notNull'] === $property->nullable) {
                    $plan->unsupported[] = sprintf(
                        '%s.%s: is %s%s, model wants %s%s — write a migration',
                        $relation,
                        $property->columnName,
                        $column['declaredType'],
                        $column['notNull'] ? ' not null' : '',
                        $expected,
                        $property->nullable ? '' : ' not null',
                    );
                }
            }

            $mappedColumns = array_map(
                static fn (PropertyMap $p): string => strtolower($p->columnName),
                $map->properties,
            );
            foreach (array_keys($live) as $name) {
                if (!in_array(strtolower($name), $mappedColumns, true)) {
                    $plan->deletions[] = $dialect->dropColumnSql($relation, $name);
                }
            }

            // Indexes match structurally (ADR-0017 add.2): what matters is the indexed
            // columns (order, direction, uniqueness), never the name — indexes get added
            // directly to the database in urgencies, and one already there under another
            // name counts as implemented.
            $liveIndexes = self::readIndexes($connection, $dialect, $relation);
            $liveSignatures = array_map(static fn (array $i): string => $i['signature'], $liveIndexes);
            $modelSignatures = array_map(
                static fn (EntityIndex $i): string => MigrationGenerator::indexSignature($i),
                $map->indexes,
            );
            $createIndexSql = $dialect->createIndexSql($map);
            foreach ($map->indexes as $i => $index) {
                if (!in_array(MigrationGenerator::indexSignature($index), $liveSignatures, true)) {
                    $plan->additive[] = $createIndexSql[$i];
                }
            }

            foreach ($liveIndexes as $liveIndex) {
                if (!in_array($liveIndex['signature'], $modelSignatures, true)) {
                    $plan->deletions[] = $dialect->dropIndexSql($relation, $liveIndex['name']);
                }
            }
        }

        return $plan;
    }

    /** Executes plan statements in order (the CLI's force-sync apply step). @param iterable<string> $statements */
    public static function apply(PDO $connection, iterable $statements): void
    {
        foreach ($statements as $sql) {
            $connection->exec($sql);
        }
    }

    /** @return array<string, array{declaredType: string, notNull: bool}> keyed by column name, as returned */
    private static function readColumns(PDO $connection, Dialect $dialect, string $relation): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->columnsInfoSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $relation]);

        $columns = [];
        foreach ($statement as $row) {
            $notNull = ((int) $row['notnull']) !== 0 || ((int) $row['pk']) !== 0;
            $columns[(string) $row['name']] = ['declaredType' => (string) $row['type'], 'notNull' => $notNull];
        }

        return $columns;
    }

    /** @param array<string, array{declaredType: string, notNull: bool}> $live @return array{declaredType: string, notNull: bool}|null */
    private static function findColumn(array $live, string $name): ?array
    {
        foreach ($live as $liveName => $column) {
            if (strcasecmp($liveName, $name) === 0) {
                return $column;
            }
        }

        return null;
    }

    /** @return list<array{name: string, signature: string}> */
    private static function readIndexes(PDO $connection, Dialect $dialect, string $relation): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->indexesInfoSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $relation]);

        /** @var array<string, array{unique: bool, columns: list<array{0: string, 1: bool}>}> $parts */
        $parts = [];
        $order = [];
        foreach ($statement->fetchAll(PDO::FETCH_NUM) as $row) {
            $name = (string) $row[0];
            if (!isset($parts[$name])) {
                $parts[$name] = ['unique' => ((int) $row[1]) !== 0, 'columns' => []];
                $order[] = $name;
            }

            $parts[$name]['columns'][] = [(string) $row[3], ((int) $row[4]) !== 0];
        }

        return array_map(
            static fn (string $name): array => [
                'name' => $name,
                'signature' => MigrationGenerator::signature($parts[$name]['unique'], $parts[$name]['columns']),
            ],
            $order,
        );
    }
}

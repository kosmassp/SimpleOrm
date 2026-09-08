<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;

/**
 * Derives a step's rollback DDL from the versioned snapshots at
 * `migrate down` time (ADR-0018): nobody writes `down()` — the previous schema
 * is recorded, so the reverse is deduced. The step's typed renames are the one
 * piece snapshots can't recover (a rename and a drop+add look identical
 * between two shapes), so they invert from the step's own actions,
 * data-preservingly; everything else — restored columns, dropped columns,
 * index changes, a view's previous definition — comes from the snapshot diff.
 * `down()` remains the manual override; a change the snapshots can't express
 * (type/nullability) still refuses with `MIG-020`.
 */
final class DownDeriver
{
    private function __construct()
    {
    }

    /**
     * The derived rollback for (object, version), or null when the snapshots
     * can't support it (no snapshot at the version). Throws `MIG-020` for a
     * same-name type/nullability change — override `down()` for those.
     *
     * @param list<ColumnRename> $upRenames
     * @param list<string> $notices appended to, by reference, with human-readable derivation caveats
     * @return list<MigrationStatement>|null
     */
    public static function derive(
        string $objectName,
        int $version,
        SnapshotSet $snapshots,
        array $upRenames,
        array &$notices,
        Dialect $dialect,
    ): ?array {
        $at = $snapshots->at($objectName, $version);
        if ($at === null) {
            return null;
        }

        $before = $snapshots->latestBefore($objectName, $version);

        return $at->ddl !== null
            ? self::deriveView($objectName, $at->ddl, $before?->ddl)
            : self::deriveTable($objectName, $version, $at->table, $before?->table, $upRenames, $notices, $dialect);
    }

    /** @return list<MigrationStatement> */
    private static function deriveView(string $objectName, string $ddlAt, ?string $ddlBefore): array
    {
        $statements = [
            // The apply guard in reverse (MIG-012): a definition hotfixed
            // outside the code is not silently destroyed by a rollback either.
            new MigrationStatement($ddlAt, 'derived expect ' . $objectName, $objectName),
            new MigrationStatement('drop view if exists ' . $objectName, 'derived drop ' . $objectName),
        ];
        if ($ddlBefore !== null) {
            $statements[] = new MigrationStatement($ddlBefore, 'derived restore ' . $objectName);
        }

        return $statements;
    }

    /**
     * @param list<ColumnRename> $upRenames
     * @param list<string> $notices
     * @return list<MigrationStatement>
     */
    private static function deriveTable(
        string $objectName,
        int $version,
        ?TableSchema $at,
        ?TableSchema $before,
        array $upRenames,
        array &$notices,
        Dialect $dialect,
    ): array {
        if ($at === null) {
            // Unreachable via derive(): a null `$at->ddl` for a resolved entry means a table schema is present.
            return [];
        }

        if ($before === null) {
            return [new MigrationStatement($dialect->dropTableSql($objectName), 'derived drop ' . $objectName)];
        }

        $statements = [];

        // Renames invert first (rename -> add -> remove ordering, §7.22) and map
        // the current names back before the shapes are compared.
        /** @var array<string, string> $nameAtToBefore lowercased "at" name => original-cased "before" name */
        $nameAtToBefore = [];
        foreach (array_reverse($upRenames) as $rename) {
            $statements[] = new MigrationStatement(
                $dialect->renameColumnSql($objectName, $rename->to, $rename->from),
                'derived rename ' . $objectName,
            );
            $nameAtToBefore[strtolower($rename->to)] = $rename->from;
        }

        /** @var array<string, TableSchemaColumn> $current keyed by lowercased "before"-side name */
        $current = [];
        foreach ($at->columns as $column) {
            $key = $nameAtToBefore[strtolower($column->name)] ?? $column->name;
            $current[strtolower($key)] = $column;
        }

        /** @var array<string, TableSchemaColumn> $previous */
        $previous = [];
        foreach ($before->columns as $column) {
            $previous[strtolower($column->name)] = $column;
        }

        foreach ($previous as $key => $column) {
            if (isset($current[$key])) {
                continue;
            }

            $sql = $dialect->addColumnSql($objectName, $column->name, $column->storageType, true, null);
            if (!$column->nullable) {
                // The database refuses a bare NOT NULL addition; the structure
                // returns nullable, and the data a preDown/postDown hook restores.
                $notices[] = sprintf(
                    'V%04d %s.%s: restored nullable — the NOT NULL constraint (and the data) are not derivable',
                    $version,
                    $objectName,
                    $column->name,
                );
            }

            $statements[] = new MigrationStatement($sql, 'derived add ' . $objectName);
        }

        // Keys carry the renamed-back names — by the time these run, the
        // reverse renames above have already been applied.
        foreach ($current as $key => $column) {
            if (isset($previous[$key])) {
                continue;
            }

            $statements[] = new MigrationStatement($dialect->dropColumnSql($objectName, $column->name), 'derived remove ' . $objectName);
        }

        foreach ($current as $key => $now) {
            if (!isset($previous[$key])) {
                continue;
            }

            $then = $previous[$key];
            if (strcasecmp($now->storageType, $then->storageType) !== 0 || $now->nullable !== $then->nullable) {
                throw new SimpleOrmException(
                    'MIG-020',
                    sprintf('V%04d %s', $version, $objectName),
                    "column {$now->name} changed type/nullability at this version; that rollback cannot be derived — override down()",
                );
            }
        }

        // Indexes match structurally (ADR-0017 add.2) here too.
        /** @var array<string, TableSchemaIndex> $atIndexes */
        $atIndexes = [];
        foreach ($at->indexes as $index) {
            $atIndexes[MigrationGenerator::indexSignature($index)] = $index;
        }

        /** @var array<string, TableSchemaIndex> $beforeIndexes */
        $beforeIndexes = [];
        foreach ($before->indexes as $index) {
            $beforeIndexes[MigrationGenerator::indexSignature($index)] = $index;
        }

        foreach ($atIndexes as $signature => $index) {
            if (!isset($beforeIndexes[self::applyRenames($signature, $nameAtToBefore)])) {
                $statements[] = new MigrationStatement($dialect->dropIndexSql($objectName, $index->name), 'derived drop index');
            }
        }

        foreach ($beforeIndexes as $signature => $index) {
            $stillPresent = false;
            foreach (array_keys($atIndexes) as $atSignature) {
                if (self::applyRenames($atSignature, $nameAtToBefore) === $signature) {
                    $stillPresent = true;

                    break;
                }
            }

            if (!$stillPresent) {
                $statements[] = new MigrationStatement(SnapshotDdl::createIndexSql($objectName, $index), 'derived add index');
            }
        }

        return $statements;
    }

    /**
     * Rewrites an index signature's column names through the reverse-rename map.
     *
     * @param array<string, string> $nameAtToBefore
     */
    private static function applyRenames(string $signature, array $nameAtToBefore): string
    {
        if ($nameAtToBefore === []) {
            return $signature;
        }

        [$flag, $columnsPart] = explode('|', $signature, 2);
        $parts = $columnsPart === '' ? [] : explode(',', $columnsPart);
        $rewritten = array_map(static function (string $part) use ($nameAtToBefore): string {
            $descending = str_ends_with($part, ' desc');
            $column = $descending ? substr($part, 0, -5) : $part;
            $renamed = $nameAtToBefore[$column] ?? $column;

            return strtolower($renamed) . ($descending ? ' desc' : '');
        }, $parts);

        return $flag . '|' . implode(',', $rewritten);
    }
}

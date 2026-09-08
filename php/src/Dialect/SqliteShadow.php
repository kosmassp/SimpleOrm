<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationStep;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotDdl;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Migrations\ViewMigration;

/**
 * Rebuilds the versioned schema snapshots (`V000N.schema.json`) by replaying
 * the migration history into a throwaway shadow SQLite database and
 * introspecting the touched tables/views after each version (ADR-0017),
 * mirroring `dotnet/src/SimpleOrm.Sqlite/SqliteShadow.cs`. The full rebuild
 * replays from empty; the range form (`$fromVersion`/`$toVersion`) **trusts
 * version `$fromVersion` as correct** — the baseline state is reconstructed
 * from the committed snapshots at or below it, without verifying anything
 * below it, and only `($fromVersion, $toVersion]` replays and re-snapshots.
 *
 * **PHP adaptation:** the C# reference resolves each touched relation back to
 * its mapped entity by scanning the whole assembly (`MapEntities`). PHP has no
 * assembly to scan (CODING-STANDARD §10), so this walks the other direction —
 * `TableMigration`/`ViewMigration` steps already carry `entityClass()`
 * (needed to render their own SQL), so the entity, and therefore its relation
 * name and snapshot folder, come directly from the step composing each
 * version, not from a separate assembly-wide lookup.
 */
final class SqliteShadow
{
    public static function rebuildSnapshots(
        MigrationSet $set,
        EntityMapLoader $maps,
        string $migrationsDir,
        int $fromVersion = 0,
        int $toVersion = PHP_INT_MAX,
    ): ShadowResult {
        $result = new ShadowResult();
        $versions = $set->versions();
        if ($versions === []) {
            $result->notes[] = 'no migration versions found';

            return $result;
        }

        $dialect = new SqliteDialect();
        $shadowFile = sys_get_temp_dir() . '/simpleorm-shadow-' . bin2hex(random_bytes(8)) . '.db';
        $generatedAt = new DateTimeImmutable('now', new DateTimeZone('UTC'));
        try {
            $connection = $dialect->createConnection($shadowFile);

            if ($fromVersion > 0) {
                // Trust: rebuild the state at fromVersion from the committed
                // snapshots, verify nothing below it.
                self::restoreBaseline($connection, $migrationsDir, $fromVersion, $result);
                (new MigrationRunner($connection, $dialect, $maps, MigrationSet::of(...$versions)))->baseline($fromVersion);
            }

            foreach ($versions as $version) {
                if ($version->version() <= $fromVersion || $version->version() > $toVersion) {
                    continue;
                }

                $subset = array_values(array_filter($versions, static fn ($v): bool => $v->version() <= $version->version()));
                $runner = new MigrationRunner($connection, $dialect, $maps, MigrationSet::of(...$subset));
                $runner->migrate();

                $builder = new VersionBuilder();
                $version->compose($builder);
                foreach ($builder->steps() as $step) {
                    self::snapshotStep($connection, $maps, $step, $version->version(), $migrationsDir, $generatedAt, $result);
                }
            }
        } finally {
            self::tryDelete($shadowFile);
            self::tryDelete($shadowFile . '-wal');
            self::tryDelete($shadowFile . '-shm');
            self::tryDelete($shadowFile . '-journal');
        }

        return $result;
    }

    // --- per-step snapshotting -------------------------------------------------------

    private static function snapshotStep(
        PDO $connection,
        EntityMapLoader $maps,
        MigrationStep $step,
        int $version,
        string $migrationsDir,
        DateTimeImmutable $generatedAt,
        ShadowResult $result,
    ): void {
        if ($step instanceof TableMigration) {
            $map = $maps->load($step->entityClass());
            $schema = self::introspectTable($connection, (string) $map->relationName);
            if ($schema === null) {
                return;   // dropped at this version — its snapshot history ends here
            }

            self::writeSnapshot(
                $migrationsDir,
                'Table',
                self::shortName($step->entityClass()),
                $version,
                SchemaSnapshot::export($schema, $version, $generatedAt),
                $result,
            );

            return;
        }

        if ($step instanceof ViewMigration) {
            $map = $maps->load($step->entityClass());
            $ddl = self::viewDdl($connection, (string) $map->relationName);
            if ($ddl === null) {
                return;
            }

            $materialized = $map->kind === RelationKind::MaterializedView;
            self::writeSnapshot(
                $migrationsDir,
                $materialized ? 'MaterializedView' : 'View',
                self::shortName($step->entityClass()),
                $version,
                SchemaSnapshot::exportDdl((string) $map->relationName, $materialized ? 'materialized_view' : 'view', $ddl, $version, $generatedAt),
                $result,
            );
        }
    }

    private static function writeSnapshot(
        string $migrationsDir,
        string $kindFolder,
        string $typeName,
        int $version,
        string $content,
        ShadowResult $result,
    ): void {
        $directory = "{$migrationsDir}/{$kindFolder}/{$typeName}";
        if (!is_dir($directory)) {
            mkdir($directory, 0777, true);
        }

        $file = $directory . '/' . sprintf('V%04d.schema.json', $version);
        file_put_contents($file, $content . "\n");
        $result->writtenFiles[] = $file;
    }

    // --- trusted baseline (--from) -----------------------------------------------------

    private static function restoreBaseline(PDO $connection, string $migrationsDir, int $fromVersion, ShadowResult $result): void
    {
        $tableRoot = "{$migrationsDir}/Table";
        if (is_dir($tableRoot)) {
            foreach (SnapshotSet::subdirectories($tableRoot) as $objectDir) {
                $latest = self::latestTableSnapshotAtOrBelow($objectDir, $fromVersion);
                if ($latest === null) {
                    continue;   // table born after the trusted version
                }

                [$schema, $atVersion] = $latest;
                self::execute($connection, SnapshotDdl::createTableSql($schema));
                foreach (SnapshotDdl::createAllIndexSql($schema) as $indexSql) {
                    self::execute($connection, $indexSql);
                }

                $result->notes[] = sprintf('baseline: %s restored from V%04d snapshot (trusted)', $schema->name, $atVersion);
            }
        } else {
            $result->notes[] = sprintf('--from V%04d: no committed snapshots under %s; starting empty', $fromVersion, $tableRoot);
        }

        // Views restore after tables — their DDL snapshots are executable as stored.
        foreach (['View', 'MaterializedView'] as $kindFolder) {
            $kindRoot = "{$migrationsDir}/{$kindFolder}";
            if (!is_dir($kindRoot)) {
                continue;
            }

            foreach (SnapshotSet::subdirectories($kindRoot) as $objectDir) {
                $latest = self::latestDdlSnapshotAtOrBelow($objectDir, $fromVersion);
                if ($latest === null) {
                    continue;
                }

                [$ddl, $objectName, $atVersion] = $latest;
                self::execute($connection, $ddl);
                $result->notes[] = sprintf('baseline: %s restored from V%04d snapshot (trusted)', $objectName, $atVersion);
            }
        }
    }

    /** @return array{0: TableSchema, 1: int}|null */
    private static function latestTableSnapshotAtOrBelow(string $dir, int $version): ?array
    {
        $best = null;
        $bestVersion = -1;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $content = file_get_contents($file);
            if ($content === false) {
                continue;
            }

            $parsed = SchemaSnapshot::parse($content);
            if ($parsed->asOfVersion <= $version && $parsed->asOfVersion > $bestVersion) {
                $bestVersion = $parsed->asOfVersion;
                $best = $parsed->schema;
            }
        }

        return $best === null ? null : [$best, $bestVersion];
    }

    /** @return array{0: string, 1: string, 2: int}|null [ddl, objectName, asOfVersion] */
    private static function latestDdlSnapshotAtOrBelow(string $dir, int $version): ?array
    {
        $best = null;
        $bestVersion = -1;
        $bestObject = null;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $content = file_get_contents($file);
            if ($content === false) {
                continue;
            }

            $parsed = SchemaSnapshot::parseDdl($content);
            if ($parsed->asOfVersion <= $version && $parsed->asOfVersion > $bestVersion) {
                $bestVersion = $parsed->asOfVersion;
                $best = $parsed->ddl;
                $bestObject = $parsed->object;
            }
        }

        return $best === null || $bestObject === null ? null : [$best, $bestObject, $bestVersion];
    }

    // --- introspection -------------------------------------------------------------------

    /** The live shape of a table, or null when the relation is not a table (view, or dropped). */
    private static function introspectTable(PDO $connection, string $relation): ?TableSchema
    {
        $exists = self::scalar($connection, "select count(*) from sqlite_master where type = 'table' and name = :name", $relation);
        if ($exists === 0) {
            return null;
        }

        $raw = [];
        $statement = $connection->prepare('select name, type, "notnull", pk from pragma_table_info(:name)');
        $statement->bindValue(':name', $relation, PDO::PARAM_STR);
        $statement->execute();
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $raw[] = [
                'name' => (string) $row['name'],
                'type' => (string) $row['type'],
                'notnull' => ((int) $row['notnull']) !== 0,
                'pk' => (int) $row['pk'],
            ];
        }

        $keyCount = count(array_filter($raw, static fn (array $c): bool => $c['pk'] > 0));
        $columns = [];
        foreach ($raw as $c) {
            $isKey = $c['pk'] > 0;
            // The rowid alias: a lone INTEGER PRIMARY KEY column is database-generated.
            $generated = $isKey && $keyCount === 1 && strcasecmp($c['type'], 'INTEGER') === 0;
            $columns[] = new TableSchemaColumn($c['name'], $c['type'], !$c['notnull'] && !$isKey, $isKey, $generated);
        }

        $indexes = [];
        $listStatement = $connection->prepare("select name, \"unique\" from pragma_index_list(:name) where origin = 'c'");
        $listStatement->bindValue(':name', $relation, PDO::PARAM_STR);
        $listStatement->execute();
        $found = [];
        while (($row = $listStatement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $found[] = ['name' => (string) $row['name'], 'unique' => ((int) $row['unique']) !== 0];
        }

        foreach ($found as $index) {
            $partsStatement = $connection->prepare('select name, "desc" from pragma_index_xinfo(:name) where key = 1 order by seqno');
            $partsStatement->bindValue(':name', $index['name'], PDO::PARAM_STR);
            $partsStatement->execute();
            $parts = [];
            while (($row = $partsStatement->fetch(PDO::FETCH_ASSOC)) !== false) {
                $parts[] = new TableSchemaIndexPart((string) $row['name'], ((int) $row['desc']) !== 0);
            }

            $indexes[] = new TableSchemaIndex($index['name'], $parts, $index['unique']);
        }

        return new TableSchema($relation, $columns, $indexes);
    }

    /** The stored create statement of a view, normalized — null when absent (dropped). */
    private static function viewDdl(PDO $connection, string $relation): ?string
    {
        $statement = $connection->prepare("select sql from sqlite_master where type = 'view' and name = :name");
        $statement->bindValue(':name', $relation, PDO::PARAM_STR);
        $statement->execute();
        $sql = $statement->fetchColumn();

        return $sql === false ? null : SchemaSnapshot::normalizeDdl((string) $sql);
    }

    // --- helpers ---------------------------------------------------------------------------

    private static function scalar(PDO $connection, string $sql, string $name): int
    {
        $statement = $connection->prepare($sql);
        $statement->bindValue(':name', $name, PDO::PARAM_STR);
        $statement->execute();

        return (int) $statement->fetchColumn();
    }

    private static function execute(PDO $connection, string $sql): void
    {
        $connection->exec($sql);
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }

    /** Best-effort cleanup of the throwaway shadow database: Windows can briefly hold a delete lock after SQLite closes its handle, so a failure here is not reported (never `@` — the warning is caught and discarded explicitly, scoped to this one call). */
    private static function tryDelete(string $path): void
    {
        if (!is_file($path)) {
            return;
        }

        set_error_handler(static fn (): bool => true);
        try {
            unlink($path);
        } finally {
            restore_error_handler();
        }
    }
}

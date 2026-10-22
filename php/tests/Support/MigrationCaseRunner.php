<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use FilesystemIterator;
use PDO;
use PDOException;
use PHPUnit\Framework\Assert;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use RuntimeException;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\ShadowHost;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\ColumnRename;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SchemaSync;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Migrations\SqlVersionTrigger;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * Replays one `conformance/migrations-cases/` file against an open database of a dialect (§9): the one runner the
 * SQLite root folder and the live `postgres/` / `sqlserver/` subfolders share (ADR-0043). A case builds its migration
 * set as data ({@see SqlVersion}) and replays the commands, comparing the recorded `(version, object)` rows or the error
 * code; the deep expects read through the dialect — `columns` through `columnsInfoSql`, `ddl` only where the dialect
 * stores view text verbatim, `triggers` only where the host reads a trigger back.
 */
final class MigrationCaseRunner
{
    private function __construct()
    {
    }

    /**
     * @param string $caseFile absolute path of the case JSON
     * @param string $connectionString opens the (fresh) database the case runs in
     */
    public static function run(string $caseFile, Dialect $dialect, ShadowHost $host, string $connectionString): void
    {
        $spec = self::readJson($caseFile);
        $defaultVersions = self::parseVersions($spec['versions']);
        $snapshotDir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_migcase_' . bin2hex(random_bytes(8));

        try {
            $snapshots = self::loadSnapshots($spec, $snapshotDir);
            $connection = $dialect->createConnection($connectionString);
            $maps = new EntityMapLoader();

            foreach ($spec['run'] as $step) {
                $versions = isset($step['versions']) ? self::parseVersions($step['versions']) : $defaultVersions;
                $runner = new MigrationRunner($connection, $dialect, $maps, MigrationSet::of(...$versions), $snapshots);
                $force = (bool) ($step['force'] ?? false);
                $expect = $step['expect'];

                $error = null;
                try {
                    match ($step['command']) {
                        'migrate' => $runner->migrate($force),
                        'down' => $runner->migrateDown((int) $step['to'], $force),
                        'baseline' => $runner->baseline((int) $step['version']),
                        // The urgency hotfix: statements applied outside migrations.
                        'sql' => SchemaSync::apply($connection, $step['statements']),
                        default => throw new RuntimeException('unknown command'),
                    };
                } catch (SimpleOrmException $exception) {
                    $error = $exception->errorCode;
                }

                if (isset($expect['error'])) {
                    Assert::assertSame($expect['error'], $error);
                } else {
                    Assert::assertNull($error);
                    if (isset($expect['applied'])) {
                        Assert::assertSame(self::normalizeRows($expect['applied']), self::readApplied($connection));
                    }
                }

                foreach ($expect['columns'] ?? [] as $table => $columns) {
                    $expectedColumns = $columns;
                    sort($expectedColumns, SORT_STRING);
                    Assert::assertSame($expectedColumns, self::readColumns($connection, $dialect, (string) $table));
                }

                if (isset($expect['ddl']) && $dialect->viewDefinitionIsVerbatim()) {
                    foreach ($expect['ddl'] as $view => $ddl) {
                        Assert::assertSame(SchemaSnapshot::normalizeDdl($ddl), self::readViewDdl($connection, $dialect, (string) $view));
                    }
                }

                if (isset($expect['triggers']) && $host->canReadTriggers()) {
                    self::assertTriggers($connection, $host, $expect['triggers']);
                }
            }
        } finally {
            $connection = null;
            if (is_dir($snapshotDir)) {
                self::removeDirectory($snapshotDir);
            }
        }
    }

    /**
     * @param list<array<string, mixed>> $versionsJson
     * @return list<MigrationVersion>
     */
    private static function parseVersions(array $versionsJson): array
    {
        return array_map(static function (array $v): MigrationVersion {
            $steps = array_map(static function (array $s): SqlVersionStep {
                $renames = array_map(
                    static fn (array $r): ColumnRename => new ColumnRename($r['from'], $r['to']),
                    $s['renames'] ?? [],
                );

                return new SqlVersionStep(
                    objectName: $s['object'],
                    description: $s['description'],
                    up: $s['up'] ?? [],
                    down: $s['down'] ?? [],
                    renames: $renames,
                    expectDefinition: $s['expectDefinition'] ?? null,
                    trigger: isset($s['trigger']) ? self::triggerStep($s['trigger']) : null,
                    expectTrigger: isset($s['expectTrigger']) ? self::triggerStep($s['expectTrigger']) : null,
                );
            }, $v['steps']);

            // "draft": true is the data-driven form of a root carrying the draft marker (Level 3 M5).
            return ($v['draft'] ?? false)
                ? SqlVersion::draft((int) $v['version'], ...$steps)
                : new SqlVersion((int) $v['version'], ...$steps);
        }, $versionsJson);
    }

    /** A `trigger` / `expectTrigger` step object (ADR-0042): the declaration as data. @param array<string, mixed> $json */
    private static function triggerStep(array $json): SqlVersionTrigger
    {
        $declaration = TriggerCaseData::definition('', $json);

        return new SqlVersionTrigger(
            (string) $json['relation'],
            ($json['relationKind'] ?? 'table') === 'view',
            $declaration->timing,
            $declaration->events,
            $declaration->sql,
            $declaration->updateOf,
        );
    }

    /**
     * The deep `triggers` expect (ADR-0042): each live trigger read back through the host and parsed to its declaration —
     * relation, timing, events, `updateOf` when declared, body with its tokens unexpanded — or `null` for absent.
     *
     * @param array<string, array<string, mixed>|null> $expected
     */
    private static function assertTriggers(PDO $connection, ShadowHost $host, array $expected): void
    {
        foreach ($expected as $name => $declaration) {
            $schema = $host->readTrigger($connection, (string) $name);
            if ($declaration === null) {
                Assert::assertNull($schema, "trigger {$name} should be absent");

                continue;
            }

            Assert::assertNotNull($schema, "trigger {$name} should exist and parse");
            $wanted = TriggerCaseData::definition((string) $name, $declaration);
            Assert::assertSame((string) $declaration['relation'], $schema->relation, "trigger {$name}: relation");
            Assert::assertSame($wanted->timing, $schema->trigger->timing, "trigger {$name}: timing");
            Assert::assertSame($wanted->events, $schema->trigger->events, "trigger {$name}: events");
            Assert::assertSame($wanted->updateOf, $schema->trigger->updateOf, "trigger {$name}: updateOf");
            Assert::assertSame($wanted->normalizedSql(), $schema->trigger->normalizedSql(), "trigger {$name}: body");
        }
    }

    /** @param array<string, mixed> $spec */
    private static function loadSnapshots(array $spec, string $snapshotDir): ?SnapshotSet
    {
        if (!isset($spec['snapshots'])) {
            return null;
        }

        mkdir($snapshotDir, 0777, true);
        foreach ($spec['snapshots'] as $index => $snapshot) {
            file_put_contents(
                $snapshotDir . DIRECTORY_SEPARATOR . "s{$index}.schema.json",
                json_encode($snapshot, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR),
            );
        }

        return SnapshotSet::fromDirectory($snapshotDir);
    }

    /** @param list<array{0: int|string, 1: string}> $rows @return list<array{0: int, 1: string}> */
    private static function normalizeRows(array $rows): array
    {
        $normalized = array_map(static fn (array $row): array => [(int) $row[0], (string) $row[1]], $rows);
        usort($normalized, self::compareRows(...));

        return $normalized;
    }

    /** @return list<array{0: int, 1: string}> */
    private static function readApplied(PDO $connection): array
    {
        try {
            $statement = $connection->query('select version, object from schema_version');
        } catch (PDOException) {
            return [];   // no migration has ever run (PostgreSQL has no such table to read)
        }

        $rows = [];
        foreach ($statement as $row) {
            $rows[] = [(int) $row['version'], (string) $row['object']];
        }

        usort($rows, self::compareRows(...));

        return $rows;
    }

    /** @param array{0: int, 1: string} $a @param array{0: int, 1: string} $b */
    private static function compareRows(array $a, array $b): int
    {
        return $a[0] <=> $b[0] ?: strcmp($a[1], $b[1]);
    }

    /** The table's column names through the dialect's own introspection query, name-sorted. @return list<string> */
    private static function readColumns(PDO $connection, Dialect $dialect, string $table): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->columnsInfoSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $table]);
        $names = array_map(static fn (array $row): string => (string) $row[0], $statement->fetchAll(PDO::FETCH_NUM));
        sort($names, SORT_STRING);

        return $names;
    }

    private static function readViewDdl(PDO $connection, Dialect $dialect, string $view): ?string
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->viewDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $view]);
        $sql = $statement->fetchColumn();

        return $sql === false ? null : SchemaSnapshot::normalizeDdl((string) $sql);
    }

    /** @return array<string, mixed> */
    private static function readJson(string $path): array
    {
        /** @var array<string, mixed> $decoded */
        $decoded = json_decode(file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);

        return $decoded;
    }

    private static function removeDirectory(string $dir): void
    {
        $iterator = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($iterator as $fileInfo) {
            $fileInfo->isDir() ? rmdir($fileInfo->getPathname()) : unlink($fileInfo->getPathname());
        }

        rmdir($dir);
    }
}

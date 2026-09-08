<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use FilesystemIterator;
use PDO;
use PDOException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\SqliteDialect;
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
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The migrations-case runner (§9): mirrors
 * `dotnet/tests/SimpleOrm.Tests/ConformanceMigrationTests.cs`. Each
 * conformance/migrations-cases/*.json builds its migration set as data
 * ({@see SqlVersion}) and replays the commands against a fresh SQLite database,
 * comparing the recorded (version, object) rows or the error code. A case may
 * carry `snapshots` (the derived-rollback history, ADR-0018), step
 * `renames`/`expectDefinition` (typed renames; the MIG-012 view guard), a raw
 * `sql` command (the outside hotfix), `force` on migrate/down, and deep
 * expects (`columns` per table, `ddl` per view).
 */
final class MigrationsCasesTest extends TestCase
{
    /** @return list<array{0: string}> */
    public static function caseFiles(): array
    {
        return array_map(static fn (string $file): array => [$file], ConformancePaths::cases('migrations-cases'));
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        $spec = self::readJson(ConformancePaths::dir('migrations-cases') . DIRECTORY_SEPARATOR . $fileName);
        $defaultVersions = self::parseVersions($spec['versions']);

        $dbPath = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_migcase_' . bin2hex(random_bytes(8)) . '.db';
        $snapshotDir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_migcase_' . bin2hex(random_bytes(8));

        try {
            $snapshots = self::loadSnapshots($spec, $snapshotDir);
            $dialect = new SqliteDialect();
            $connection = $dialect->createConnection($dbPath);
            $maps = new EntityMapLoader();

            foreach ($spec['run'] as $step) {
                $versions = isset($step['versions']) ? self::parseVersions($step['versions']) : $defaultVersions;
                $set = MigrationSet::of(...$versions);
                $runner = new MigrationRunner($connection, $dialect, $maps, $set, $snapshots);
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
                        default => throw new \RuntimeException('unknown command'),
                    };
                } catch (SimpleOrmException $exception) {
                    $error = $exception->errorCode;
                }

                if (isset($expect['error'])) {
                    self::assertSame($expect['error'], $error);
                } else {
                    self::assertNull($error);
                    if (isset($expect['applied'])) {
                        self::assertSame(self::normalizeRows($expect['applied']), self::readApplied($connection));
                    }
                }

                if (isset($expect['columns'])) {
                    foreach ($expect['columns'] as $table => $columns) {
                        $expectedColumns = $columns;
                        sort($expectedColumns, SORT_STRING);
                        self::assertSame($expectedColumns, self::readColumns($dbPath, $table));
                    }
                }

                if (isset($expect['ddl'])) {
                    foreach ($expect['ddl'] as $view => $ddl) {
                        self::assertSame(SchemaSnapshot::normalizeDdl($ddl), self::readViewDdl($dbPath, $view));
                    }
                }
            }
        } finally {
            unset($connection);
            foreach ([$dbPath, $dbPath . '-wal', $dbPath . '-shm', $dbPath . '-journal'] as $file) {
                if (is_file($file)) {
                    @unlink($file);
                }
            }

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
                    up: $s['up'],
                    down: $s['down'] ?? [],
                    renames: $renames,
                    expectDefinition: $s['expectDefinition'] ?? null,
                );
            }, $v['steps']);

            return new SqlVersion((int) $v['version'], ...$steps);
        }, $versionsJson);
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
            return [];
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

    /** @return list<string> */
    private static function readColumns(string $databasePath, string $table): array
    {
        $pdo = self::readOnlyConnection($databasePath);
        $statement = $pdo->prepare('select name from pragma_table_info(:t) order by name');
        PdoBinder::bindAndExecute($statement, ['t' => $table]);

        return array_map(static fn (array $row): string => (string) $row['name'], $statement->fetchAll(PDO::FETCH_ASSOC));
    }

    private static function readViewDdl(string $databasePath, string $view): ?string
    {
        $pdo = self::readOnlyConnection($databasePath);
        $statement = $pdo->prepare("select sql from sqlite_master where type = 'view' and name = :v");
        PdoBinder::bindAndExecute($statement, ['v' => $view]);
        $sql = $statement->fetchColumn();

        return $sql === false ? null : SchemaSnapshot::normalizeDdl((string) $sql);
    }

    private static function readOnlyConnection(string $databasePath): PDO
    {
        $pdo = new PDO('sqlite:' . $databasePath);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);

        return $pdo;
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

<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use FilesystemIterator;
use PDO;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SchemaShadow;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadget;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadgetCodes;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Postgres;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Sqlite;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\SqlServer;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\V0002;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\V0003;
use SimpleOrm\Tests\Support\LiveServer;
use SimpleOrm\Tests\Support\PostgresDatabase;
use SimpleOrm\Tests\Support\SqlServerDatabase;

/**
 * The shadow on every dialect (ADR-0043, spec/migrations.md "Dialects", last paragraph): a short tree — a literal create
 * table with a unique index in the dialect's SQL, a view step, a trigger step, a literal add-column step — is replayed
 * into a throwaway database the dialect's shadow host provisions, and the snapshots it writes must equal the
 * metadata-side export apart from `generatedAt`. SQLite always runs; PostgreSQL and SQL Server run against a real server
 * and skip as a group where none answers (`composer test-live`).
 */
final class ShadowRoundTripTest extends TestCase
{
    private const string GENERATED_AT = '2026-10-22T12:00:00Z';

    /** @var list<string> */
    private array $tempDirs = [];

    protected function tearDown(): void
    {
        foreach ($this->tempDirs as $dir) {
            self::removeDirectory($dir);
        }
    }

    /** @return iterable<string, array{string}> */
    public static function dialects(): iterable
    {
        foreach (Dialects::names() as $name) {
            yield $name => [$name];
        }
    }

    #[Test]
    #[DataProvider('dialects')]
    public function the_replayed_snapshots_equal_the_metadata_side_export_apart_from_generated_at(string $name): void
    {
        $admin = self::adminFor($name);
        $dialect = Dialects::create($name);
        $maps = new EntityMapLoader();
        $out = $this->tempDir();

        $result = SchemaShadow::for($dialect, $admin)->rebuildSnapshots(MigrationSet::of(...self::tree($name)), $maps, $out);

        self::assertCount(4, $result->writtenFiles);
        self::assertSame([], self::disagreements($result->notes), 'the catalog and the steps agree: ' . implode('; ', $result->notes));

        $generatedAt = new DateTimeImmutable(self::GENERATED_AT);
        $gadget = $maps->load(ShadowGadget::class);
        $codes = $maps->load(ShadowGadgetCodes::class);
        $expected = [
            'Table/ShadowGadget/V0004.schema.json' => SchemaSnapshot::export(SchemaSnapshot::fromMap($gadget, $dialect), 4, $generatedAt, $name),
            'View/ShadowGadgetCodes/V0002.schema.json' => SchemaSnapshot::exportDdl(
                'shadow_gadget_codes',
                'view',
                $dialect->createViewSql($codes),
                2,
                $generatedAt,
                $name,
            ),
            'Trigger/ShadowGadgetTrigger/V0003.schema.json' => SchemaSnapshot::exportTrigger(
                SchemaSnapshot::fromTrigger($gadget, $gadget->trigger('trg_shadow_gadgets') ?? self::fail('the fixture links its trigger')),
                3,
                $generatedAt,
                $name,
            ),
        ];

        foreach ($expected as $path => $document) {
            self::assertFileExists("{$out}/{$path}");
            self::assertSame(
                self::withoutGeneratedAt($document),
                self::withoutGeneratedAt((string) file_get_contents("{$out}/{$path}")),
                "{$name}: {$path} differs from the metadata-side export",
            );
        }

        // V0001: the table as literally created, before `note` — introspected through the dialect's own columns query.
        $first = json_decode((string) file_get_contents("{$out}/Table/ShadowGadget/V0001.schema.json"), true, flags: JSON_THROW_ON_ERROR);
        self::assertSame(['code', 'id', 'name'], array_column($first['columns'], 'column'));
        self::assertSame($name === Dialects::SQLITE ? null : $name, $first['dialect'] ?? null, 'every non-SQLite snapshot carries the stamp, SQLite none');
    }

    #[Test]
    #[DataProvider('dialects')]
    public function from_trusts_the_committed_snapshots_and_restores_the_baseline_through_the_dialect(string $name): void
    {
        $admin = self::adminFor($name);
        $dialect = Dialects::create($name);
        $maps = new EntityMapLoader();
        $set = MigrationSet::of(...self::tree($name));
        $committed = $this->tempDir();
        $out = $this->tempDir();

        SchemaShadow::for($dialect, $admin)->rebuildSnapshots($set, $maps, $committed);
        self::copySnapshotsAtOrBelow($committed, $out, 2);

        $result = SchemaShadow::for($dialect, $admin)->rebuildSnapshots($set, $maps, $out, fromVersion: 2, toVersion: 4);

        // Only (2, 4] replayed: the trigger (V0003) and the add-column (V0004); the baseline came from V0001/V0002's snapshots.
        self::assertCount(2, $result->writtenFiles);
        foreach (['Trigger/ShadowGadgetTrigger/V0003.schema.json', 'Table/ShadowGadget/V0004.schema.json'] as $path) {
            self::assertSame(
                self::withoutGeneratedAt((string) file_get_contents("{$committed}/{$path}")),
                self::withoutGeneratedAt((string) file_get_contents("{$out}/{$path}")),
                "{$name}: {$path} replayed on the trusted baseline equals the full replay",
            );
        }
    }

    #[Test]
    public function a_snapshot_of_another_dialect_is_refused_by_the_trusted_baseline(): void
    {
        $out = $this->tempDir();
        mkdir("{$out}/Table/ShadowGadget", 0777, true);
        file_put_contents(
            "{$out}/Table/ShadowGadget/V0001.schema.json",
            '{"object": "shadow_gadgets", "dialect": "postgres", "asOfVersion": 1, "generatedAt": "2026-10-22T12:00:00.0000000Z", '
                . '"columns": [{"column": "id", "type": "bigint", "nullable": false, "key": true, "generated": true}], "indexes": []}',
        );

        try {
            SchemaShadow::for(Dialects::create(Dialects::SQLITE))->rebuildSnapshots(
                MigrationSet::of(...self::tree(Dialects::SQLITE)),
                new EntityMapLoader(),
                $out,
                fromVersion: 1,
            );
            self::fail('a postgres-stamped baseline is refused by the sqlite shadow');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-014', $exception->errorCode);
        }
    }

    #[Test]
    public function a_server_dialect_needs_the_admin_connection(): void
    {
        $this->expectException(SimpleOrmException::class);

        SchemaShadow::for(Dialects::create(Dialects::POSTGRES))->rebuildSnapshots(
            MigrationSet::of(...self::tree(Dialects::POSTGRES)),
            new EntityMapLoader(),
            $this->tempDir(),
        );
    }

    #[Test]
    #[DataProvider('dialects')]
    public function a_failed_replay_still_drops_the_throwaway_database(string $name): void
    {
        $admin = self::adminFor($name);
        $dialect = Dialects::create($name);
        $before = self::shadowDatabases($name, $admin);
        $broken = MigrationSet::of(new SqlVersion(1, new SqlVersionStep('broken', 'not_sql', ['this is not sql'])));

        try {
            SchemaShadow::for($dialect, $admin)->rebuildSnapshots($broken, new EntityMapLoader(), $this->tempDir());
            self::fail('the broken step fails the replay');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-021', $exception->errorCode);
        }

        self::assertSame($before, self::shadowDatabases($name, $admin), 'the throwaway database is gone, failure included');
    }

    // --- helpers -----------------------------------------------------------------------

    /** The admin connection of a dialect ('' for SQLite); skips the test where no server answers. */
    private static function adminFor(string $name): string
    {
        $admin = match ($name) {
            Dialects::SQLITE => '',
            Dialects::POSTGRES => PostgresDatabase::adminDsn(),
            default => SqlServerDatabase::adminDsn(),
        };
        LiveServer::skipUnless($admin, "{$name} is not available (see spec/dialects.md \"Testing contract\")");

        return (string) $admin;
    }

    /** @return list<MigrationVersion> the dialect's tree: its literal create, the shared view and trigger steps, its literal add-column */
    private static function tree(string $name): array
    {
        return match ($name) {
            Dialects::SQLITE => [new Sqlite\V0001(), new V0002(), new V0003(), new Sqlite\V0004()],
            Dialects::POSTGRES => [new Postgres\V0001(), new V0002(), new V0003(), new Postgres\V0004()],
            default => [new SqlServer\V0001(), new V0002(), new V0003(), new SqlServer\V0004()],
        };
    }

    /**
     * Notes that report a catalog/step disagreement (everything else — a "skipped" trigger on SQL Server, a trusted-baseline
     * line — is progress).
     *
     * @param list<string> $notes
     * @return list<string>
     */
    private static function disagreements(array $notes): array
    {
        return array_values(array_filter(
            $notes,
            static fn (string $note): bool => str_contains($note, 'differs') || str_contains($note, 'is absent, unlike'),
        ));
    }

    /** @return list<string> the shadow databases present on the server, sorted */
    private static function shadowDatabases(string $name, string $admin): array
    {
        if ($name === Dialects::SQLITE) {
            return [];
        }

        $pdo = Dialects::create($name)->createConnection($admin);
        $query = $name === Dialects::POSTGRES
            ? "select datname from pg_database where datname like 'simpleorm_shadow_%'"
            : "select name from sys.databases where name like 'simpleorm_shadow_%'";
        $names = array_map(static fn (mixed $value): string => (string) $value, $pdo->query($query)->fetchAll(PDO::FETCH_COLUMN));
        sort($names, SORT_STRING);

        return $names;
    }

    private static function withoutGeneratedAt(string $json): string
    {
        /** @var array<string, mixed> $document */
        $document = json_decode($json, true, flags: JSON_THROW_ON_ERROR);
        unset($document['generatedAt']);

        return json_encode($document, JSON_THROW_ON_ERROR);
    }

    private static function copySnapshotsAtOrBelow(string $from, string $to, int $version): void
    {
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($from, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $file) {
            if (!$file->isFile() || !str_ends_with($file->getFilename(), '.schema.json')) {
                continue;
            }

            $document = json_decode((string) file_get_contents($file->getPathname()), true, flags: JSON_THROW_ON_ERROR);
            if ((int) $document['asOfVersion'] > $version) {
                continue;
            }

            $target = $to . substr(str_replace('\\', '/', $file->getPathname()), strlen(str_replace('\\', '/', $from)));
            if (!is_dir(dirname($target))) {
                mkdir(dirname($target), 0777, true);
            }

            copy($file->getPathname(), $target);
        }
    }

    private function tempDir(): string
    {
        $dir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_shadow_rt_' . bin2hex(random_bytes(8));
        mkdir($dir, 0777, true);
        $this->tempDirs[] = $dir;

        return $dir;
    }

    private static function removeDirectory(string $dir): void
    {
        if (!is_dir($dir)) {
            return;
        }

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

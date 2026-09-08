<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use FilesystemIterator;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\SqliteShadow;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;

/**
 * ADR-0017: the shadow replayer, mirroring `dotnet/tests/SimpleOrm.Tests/ShadowTests.cs`.
 * A full rebuild replays every version into a throwaway database and must
 * reproduce the historically-accurate shape at each version (proving
 * snapshots derive from history, not from the current model — `V0001`'s
 * `widgets` never had a `note` column even though the entity does today). The
 * range form trusts the committed snapshots at `--from` and regenerates only
 * the requested slice.
 */
final class SqliteShadowTest extends TestCase
{
    private const string NAMESPACE = 'SimpleOrm\\Tests\\Dialect\\Fixtures\\ShadowApp';

    /** @var list<string> */
    private array $tempDirs = [];

    protected function tearDown(): void
    {
        foreach ($this->tempDirs as $dir) {
            self::removeDirectory($dir);
        }
    }

    #[Test]
    public function full_rebuild_reproduces_the_historical_shape_at_each_version(): void
    {
        $outDir = $this->tempDir('full');
        $set = MigrationSet::fromDirectory($this->fixtureDir(), self::NAMESPACE);

        $result = SqliteShadow::rebuildSnapshots($set, new EntityMapLoader(), $outDir);

        self::assertCount(3, $result->writtenFiles);

        $generatedAt = new DateTimeImmutable('2020-01-01T00:00:00Z');

        // V0001: widgets as literally created — no `note` yet, even though the
        // current entity declares it (added by V0003).
        $widgetV1 = new TableSchema('widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('name', 'TEXT', false),
        ], []);
        self::assertSnapshotMatches(
            $outDir . '/Table/Widget/V0001.schema.json',
            SchemaSnapshot::export($widgetV1, 1, $generatedAt),
        );

        // V0002: gadgets, created once, with its declared index.
        $gadgetV2 = new TableSchema('gadgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('widget_id', 'INTEGER', false),
            new TableSchemaColumn('label', 'TEXT', false),
        ], [
            new TableSchemaIndex('ix_gadgets_widget_id', [new TableSchemaIndexPart('widget_id')]),
        ]);
        self::assertSnapshotMatches(
            $outDir . '/Table/Gadget/V0002.schema.json',
            SchemaSnapshot::export($gadgetV2, 2, $generatedAt),
        );

        // V0003: widgets after the add-column step.
        $widgetV3 = new TableSchema('widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('name', 'TEXT', false),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);
        self::assertSnapshotMatches(
            $outDir . '/Table/Widget/V0003.schema.json',
            SchemaSnapshot::export($widgetV3, 3, $generatedAt),
        );
    }

    #[Test]
    public function from_trusts_the_baseline_and_regenerates_only_the_slice(): void
    {
        $committedDir = $this->tempDir('committed');
        $outDir = $this->tempDir('range');
        $set = MigrationSet::fromDirectory($this->fixtureDir(), self::NAMESPACE);
        $maps = new EntityMapLoader();

        // Seed the trusted history with a full rebuild, then hand the out dir
        // only the snapshots at or below the trusted version.
        $full = SqliteShadow::rebuildSnapshots($set, $maps, $committedDir);
        self::assertCount(3, $full->writtenFiles);
        self::copySnapshotsAtOrBelow($committedDir, $outDir, 2);

        $result = SqliteShadow::rebuildSnapshots($set, $maps, $outDir, fromVersion: 2, toVersion: 3);

        // Only V0003 (widgets) is in the slice; V0001/V0002 were neither replayed nor rewritten.
        self::assertCount(1, $result->writtenFiles);
        $written = str_replace('\\', '/', $result->writtenFiles[0]);
        self::assertStringEndsWith('Table/Widget/V0003.schema.json', $written);
        self::assertSame(
            self::normalize((string) file_get_contents($committedDir . '/Table/Widget/V0003.schema.json')),
            self::normalize((string) file_get_contents($written)),
        );

        // The trusted baseline files themselves were left exactly as seeded.
        self::assertSame(
            self::normalize((string) file_get_contents($committedDir . '/Table/Widget/V0001.schema.json')),
            self::normalize((string) file_get_contents($outDir . '/Table/Widget/V0001.schema.json')),
        );
    }

    private function fixtureDir(): string
    {
        return __DIR__ . '/Fixtures/ShadowApp';
    }

    private function tempDir(string $label): string
    {
        $dir = sys_get_temp_dir() . '/simpleorm_shadow_' . $label . '_' . bin2hex(random_bytes(6));
        $this->tempDirs[] = $dir;

        return $dir;
    }

    private static function assertSnapshotMatches(string $file, string $expected): void
    {
        self::assertFileExists($file);
        self::assertSame(self::normalize($expected), self::normalize((string) file_get_contents($file)));
    }

    private static function normalize(string $json): string
    {
        return trim((string) preg_replace('/"generatedAt": "[^"]+"/', '"generatedAt": "-"', $json));
    }

    private static function copySnapshotsAtOrBelow(string $sourceDir, string $destDir, int $maxVersion): void
    {
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($sourceDir, FilesystemIterator::SKIP_DOTS));
        $base = rtrim(str_replace('\\', '/', $sourceDir), '/');
        foreach ($iterator as $fileInfo) {
            if (!$fileInfo->isFile() || !str_ends_with($fileInfo->getFilename(), '.schema.json')) {
                continue;
            }

            $content = (string) file_get_contents($fileInfo->getPathname());
            $data = json_decode($content, true, flags: JSON_THROW_ON_ERROR);
            if ((int) $data['asOfVersion'] > $maxVersion) {
                continue;
            }

            $relative = ltrim(substr(str_replace('\\', '/', $fileInfo->getPathname()), strlen($base)), '/');
            $target = $destDir . '/' . $relative;
            if (!is_dir(dirname($target))) {
                mkdir(dirname($target), 0777, true);
            }

            file_put_contents($target, $content);
        }
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
            $fileInfo->isDir() ? rmdir((string) $fileInfo->getRealPath()) : unlink((string) $fileInfo->getRealPath());
        }

        rmdir($dir);
    }
}

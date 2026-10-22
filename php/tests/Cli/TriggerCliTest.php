<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli;

use FilesystemIterator;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Cli\Application;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SchemaShadow;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Triggers through the CLI (Level 4, ADR-0042), against the sample project (V0013 creates the audit trigger):
 * `migrate` applies the trigger step, `snapshot` writes `Trigger/<Class>/V000N.schema.json` from metadata, `shadow`
 * writes the same document from replayed history — they agree byte for byte apart from `generatedAt`, and equal the
 * committed one — `diff` finds nothing, `export-metadata` carries the `triggers` block without exporting the trigger
 * class as an entity, and `validate` passes.
 */
final class TriggerCliTest extends TestCase
{
    private const string SRC = __DIR__ . '/../../samples/SimpleOrm.Sample/src';

    private const string NAMESPACE = 'SimpleOrm\\Sample';

    private TempDatabase $database;

    private string $workDir;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->workDir = sys_get_temp_dir() . '/simpleorm_trigger_cli_' . bin2hex(random_bytes(6));
        self::copyDirectory(self::SRC . '/Migrations', $this->workDir . '/Migrations');
    }

    protected function tearDown(): void
    {
        $this->database->delete();
        self::removeDirectory($this->workDir);
    }

    #[Test]
    public function snapshot_and_shadow_write_the_same_trigger_document_the_sample_commits(): void
    {
        [$exit, $out] = $this->exec(['migrate']);
        self::assertSame(0, $exit, $out);
        self::assertStringContainsString('applied 13 version(s)', $out);

        $committed = (string) file_get_contents(self::SRC . '/Migrations/Trigger/RoleAuditTrigger/V0013.schema.json');

        $snapshotDir = $this->workDir . '/snapshot';
        [$exit, $out] = $this->exec(['snapshot', '--out', $snapshotDir]);
        self::assertSame(0, $exit, $out);
        self::assertStringContainsString('Trigger/RoleAuditTrigger/V0013.schema.json', str_replace('\\', '/', $out));
        self::assertSame(self::normalize($committed), self::normalize((string) file_get_contents($snapshotDir . '/Trigger/RoleAuditTrigger/V0013.schema.json')));

        $shadowDir = $this->workDir . '/shadow';
        [$exit, $out] = $this->exec(['shadow', '--out', $shadowDir]);
        self::assertSame(0, $exit, $out);
        self::assertSame(self::normalize($committed), self::normalize((string) file_get_contents($shadowDir . '/Trigger/RoleAuditTrigger/V0013.schema.json')));
        self::assertSame(
            self::normalize((string) file_get_contents($snapshotDir . '/Table/RoleChange/V0013.schema.json')),
            self::normalize((string) file_get_contents($shadowDir . '/Table/RoleChange/V0013.schema.json')),
        );
    }

    #[Test]
    public function shadow_from_a_trusted_version_restores_the_baseline_and_replays_only_the_trigger_slice(): void
    {
        $out = $this->workDir . '/range';
        $this->copyCommittedSnapshotsAtOrBelow($out, 12);

        $result = SchemaShadow::for(new SqliteDialect())->rebuildSnapshots(
            MigrationSet::fromDirectory($this->workDir . '/Migrations', self::NAMESPACE . '\\Migrations'),
            new EntityMapLoader(),
            $out,
            fromVersion: 12,
        );

        $written = array_map(static fn (string $file): string => str_replace('\\', '/', $file), $result->writtenFiles);
        sort($written);
        self::assertCount(2, $written);
        self::assertStringEndsWith('Table/RoleChange/V0013.schema.json', $written[0]);
        self::assertStringEndsWith('Trigger/RoleAuditTrigger/V0013.schema.json', $written[1]);
        self::assertSame(
            self::normalize((string) file_get_contents(self::SRC . '/Migrations/Trigger/RoleAuditTrigger/V0013.schema.json')),
            self::normalize((string) file_get_contents($out . '/Trigger/RoleAuditTrigger/V0013.schema.json')),
        );
    }

    #[Test]
    public function diff_finds_nothing_and_validate_passes_with_the_trigger_in_the_model(): void
    {
        [$exit, $out] = $this->exec(['migrate']);
        self::assertSame(0, $exit, $out);

        [$exit, $out] = $this->exec(['diff', '--name', 'NoOp']);
        self::assertSame(0, $exit, $out);
        self::assertStringContainsString('no schema changes', $out);

        [$exit, $out] = $this->exec(['validate']);
        self::assertSame(0, $exit, $out);
    }

    #[Test]
    public function export_metadata_carries_the_triggers_block_and_skips_the_trigger_class(): void
    {
        $exportDir = $this->workDir . '/export';

        [$exit, $out] = $this->exec(['export-metadata', '--out', $exportDir]);

        self::assertSame(0, $exit, $out);
        self::assertStringContainsString('"triggers"', (string) file_get_contents($exportDir . '/role.json'));
        self::assertFileExists($exportDir . '/role_change.json');
        self::assertFileDoesNotExist($exportDir . '/role_audit_trigger.json');
    }

    /** @param list<string> $args @return array{0: int, 1: string} */
    private function exec(array $args): array
    {
        $full = [
            '--src', self::SRC,
            '--namespace', self::NAMESPACE,
            '--migrations', $this->workDir . '/Migrations',
            '--db', $this->database->connectionString(),
            ...$args,
        ];

        ob_start();
        $exit = Application::main($full);
        $output = (string) ob_get_clean();

        return [$exit, $output];
    }

    private function copyCommittedSnapshotsAtOrBelow(string $destination, int $version): void
    {
        $base = $this->workDir . '/Migrations';
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($base, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $file) {
            $name = $file->getFilename();
            if (preg_match('/^V(\d+)\.schema\.json$/', $name, $matches) !== 1 || (int) $matches[1] > $version) {
                continue;
            }

            $relative = ltrim(str_replace('\\', '/', substr($file->getPathname(), strlen($base))), '/');
            if (!is_dir(dirname("{$destination}/{$relative}"))) {
                mkdir(dirname("{$destination}/{$relative}"), 0777, true);
            }

            copy($file->getPathname(), "{$destination}/{$relative}");
        }
    }

    private static function normalize(string $json): string
    {
        return trim((string) preg_replace('/"generatedAt": "[^"]+"/', '"generatedAt": "-"', $json));
    }

    private static function copyDirectory(string $source, string $destination): void
    {
        mkdir($destination, 0777, true);
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($source, FilesystemIterator::SKIP_DOTS));
        $base = rtrim(str_replace('\\', '/', $source), '/');
        foreach ($iterator as $fileInfo) {
            if (!$fileInfo->isFile()) {
                continue;
            }

            $relative = ltrim(substr(str_replace('\\', '/', $fileInfo->getPathname()), strlen($base)), '/');
            $target = $destination . '/' . $relative;
            if (!is_dir(dirname($target))) {
                mkdir(dirname($target), 0777, true);
            }

            copy($fileInfo->getPathname(), $target);
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
            $fileInfo->isDir() ? rmdir($fileInfo->getPathname()) : unlink($fileInfo->getPathname());
        }

        rmdir($dir);
    }
}

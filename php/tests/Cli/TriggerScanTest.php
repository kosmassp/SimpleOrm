<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Cli\Application;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;

/**
 * `MAP-029` in the assembly scan (ADR-0042): a trigger class linked by two entities is a conflict no lone load can
 * see, so `export-metadata` refuses and SchemaGuard reports it — the same rule as `MAP-028`'s scan
 * ({@see BaseClassScanTest}), through {@see \SimpleOrm\Metadata\TriggerRules}.
 */
final class TriggerScanTest extends TestCase
{
    private const string SRC = __DIR__ . '/Fixtures/TriggerApp';

    private const string NAMESPACE = 'SimpleOrm\Tests\Cli\Fixtures\TriggerApp';

    private TempDatabase $database;

    private string $outDir;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->outDir = sys_get_temp_dir() . '/simpleorm_cli_trigger_scan_' . bin2hex(random_bytes(6));
    }

    protected function tearDown(): void
    {
        $this->database->delete();
        foreach (glob($this->outDir . '/*') ?: [] as $file) {
            unlink($file);
        }

        if (is_dir($this->outDir)) {
            rmdir($this->outDir);
        }
    }

    #[Test]
    public function export_metadata_refuses_a_trigger_class_linked_twice_and_writes_nothing(): void
    {
        ob_start();
        $exit = Application::main([
            '--src', self::SRC,
            '--namespace', self::NAMESPACE,
            '--migrations', $this->outDir . '/none',
            '--db', $this->database->connectionString(),
            'export-metadata', '--out', $this->outDir,
        ]);
        $output = (string) ob_get_clean();

        self::assertSame(1, $exit);
        self::assertStringNotContainsString('wrote', $output);
        self::assertSame([], glob($this->outDir . '/*') ?: []);
    }

    #[Test]
    public function schema_guard_reports_the_conflict_as_map029_and_never_validates_the_trigger_class_as_an_entity(): void
    {
        $db = Db::open($this->database->connectionString(), SampleDatabase::options());

        $report = SchemaGuard::report($db, ClassScanner::classes(self::SRC, self::NAMESPACE));

        $map029 = array_values(array_filter($report, static fn ($error): bool => $error->code === 'MAP-029'));
        self::assertCount(1, $map029);
        self::assertStringContainsString('SharedTrigger', $map029[0]->target);
        foreach ($report as $error) {
            self::assertStringNotContainsString('SharedTrigger', $error->code === 'MAP-029' ? '' : $error->target, 'the trigger class is no entity');
        }
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Cli\Application;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * `MAP-028` in the CLI's assembly scan (ADR-0040): `export-metadata` and `validate` refuse a scanned concrete class
 * without a relation source that another scanned class extends, via the same {@see \SimpleOrm\Metadata\BaseClassRules}
 * SchemaGuard uses; the clean fixture application (whose abstract `AuditedModel` is the declared convention) is
 * covered by {@see ApplicationTest}.
 */
final class BaseClassScanTest extends TestCase
{
    private const string SRC = __DIR__ . '/Fixtures/BaseApp';

    private const string NAMESPACE = 'SimpleOrm\Tests\Cli\Fixtures\BaseApp';

    private TempDatabase $database;

    private string $outDir;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->outDir = sys_get_temp_dir() . '/simpleorm_cli_base_' . bin2hex(random_bytes(6));
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
    public function export_metadata_refuses_a_scanned_base_class_and_writes_nothing(): void
    {
        [$exit, $out] = $this->exec(['export-metadata', '--out', $this->outDir]);

        self::assertSame(1, $exit);
        self::assertStringNotContainsString('wrote', $out);
        self::assertSame([], glob($this->outDir . '/*') ?: []);
    }

    #[Test]
    public function validate_reports_the_base_class_as_map028(): void
    {
        [$exit] = $this->exec(['validate']);

        self::assertSame(1, $exit);
    }

    /** @param list<string> $args */
    private function exec(array $args): array
    {
        $full = [
            '--src', self::SRC,
            '--namespace', self::NAMESPACE,
            '--migrations', $this->outDir . '/none',
            '--db', $this->database->connectionString(),
            ...$args,
        ];

        ob_start();
        $exit = Application::main($full);
        $output = (string) ob_get_clean();

        return [$exit, $output];
    }
}

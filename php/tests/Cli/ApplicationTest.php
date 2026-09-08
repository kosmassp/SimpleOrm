<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli;

use FilesystemIterator;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Cli\Application;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * End-to-end smoke test of `bin/simpleorm`, mirroring the milestone flow of
 * `dotnet/src/SimpleOrm.Cli/Program.cs`: `migrate` -> `status` -> `validate`
 * -> `export-metadata` -> `snapshot` -> `diff` (no-op) -> `shadow` ->
 * `migrate down` -> `baseline`, in-process against a temp database and the
 * fixture application under `tests/Cli/Fixtures/App/`.
 *
 * The fixture's `Migrations` directory (compiled steps *and* its two
 * committed `.schema.json` files) is copied into a per-test temp directory
 * before every test: `snapshot`/`diff`/`shadow` write into `--migrations`
 * (or `--out`), and the checked-in fixture must never be mutated by a test
 * run. Class discovery still resolves correctly from the copy — the
 * classes' namespace, fixed by `composer.json`'s PSR-4 map, is what
 * Composer autoload actually uses; `ClassScanner` only reads the directory
 * to learn *which* class names to ask for.
 */
final class ApplicationTest extends TestCase
{
    private const string SRC = __DIR__ . '/Fixtures/App';

    private const string NAMESPACE = 'SimpleOrm\\Tests\\Cli\\Fixtures\\App';

    private TempDatabase $database;

    private string $workDir;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->workDir = sys_get_temp_dir() . '/simpleorm_cli_' . bin2hex(random_bytes(6));
        self::copyDirectory(self::SRC . '/Migrations', $this->workDir . '/Migrations');
    }

    protected function tearDown(): void
    {
        $this->database->delete();
        self::removeDirectory($this->workDir);
    }

    #[Test]
    public function full_workflow_migrate_through_baseline(): void
    {
        [$exit, $out] = $this->exec(['migrate']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('applied 2 version(s)', $out);

        [$exit, $out] = $this->exec(['status']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('app_widgets', $out);
        self::assertStringContainsString('app_gadgets', $out);
        self::assertStringContainsString('applied', $out);

        [$exit, $out] = $this->exec(['validate']);
        self::assertSame(0, $exit, $out);
        self::assertStringContainsString('valid', $out);

        $exportDir = $this->workDir . '/export';
        [$exit] = $this->exec(['export-metadata', '--out', $exportDir]);
        self::assertSame(0, $exit);
        self::assertFileExists($exportDir . '/widget.json');
        self::assertFileExists($exportDir . '/gadget.json');

        [$exit, $out] = $this->exec(['snapshot']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('wrote', $out);
        self::assertFileExists($this->workDir . '/Migrations/Table/Widget/V0001.schema.json');
        self::assertFileExists($this->workDir . '/Migrations/Table/Gadget/V0002.schema.json');

        // The fixture is static: the model already matches the snapshots.
        [$exit, $out] = $this->exec(['diff', '--name', 'NoOp']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('no schema changes', $out);

        $shadowDir = $this->workDir . '/shadow';
        [$exit, $out] = $this->exec(['shadow', '--out', $shadowDir]);
        self::assertSame(0, $exit);
        self::assertStringContainsString('regenerated 2 snapshot(s)', $out);
        self::assertSame(
            self::normalize((string) file_get_contents($this->workDir . '/Migrations/Table/Widget/V0001.schema.json')),
            self::normalize((string) file_get_contents($shadowDir . '/Table/Widget/V0001.schema.json')),
        );
        self::assertSame(
            self::normalize((string) file_get_contents($this->workDir . '/Migrations/Table/Gadget/V0002.schema.json')),
            self::normalize((string) file_get_contents($shadowDir . '/Table/Gadget/V0002.schema.json')),
        );

        [$exit, $out] = $this->exec(['migrate', 'down', '--to', '1']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('reverted 1 version(s)', $out);
        self::assertStringContainsString('V0001', $out);

        [$exit, $out] = $this->exec(['baseline', '--version', '2']);
        self::assertSame(0, $exit);
        self::assertStringContainsString('baselined at V0002', $out);
    }

    #[Test]
    public function no_arguments_prints_usage_and_exits_2(): void
    {
        [$exit, $out] = $this->exec([]);

        self::assertSame(2, $exit);
        self::assertStringContainsString('simpleorm', $out);
    }

    #[Test]
    public function an_unknown_dialect_refuses_naming_adr_0026(): void
    {
        [$exit] = $this->exec(['migrate', '--dialect', 'postgres']);

        self::assertSame(1, $exit);
    }

    /** @param list<string> $args the command and its options, without the shared --src/--namespace/--migrations/--db */
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
            $fileInfo->isDir() ? rmdir((string) $fileInfo->getRealPath()) : unlink((string) $fileInfo->getRealPath());
        }

        rmdir($dir);
    }
}

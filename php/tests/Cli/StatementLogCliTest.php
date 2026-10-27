<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * The built-in sink and the CLI flag (ADR-0045): `DbLog::stdout()` writes each line to the process's standard output, and
 * `--log` puts every statement of a CLI run through it. Both are proved in a child process, because a line written to the
 * real standard output never passes PHP's output buffers — which is also what makes it the console in the CLI.
 */
final class StatementLogCliTest extends TestCase
{
    private const string SRC = __DIR__ . '/../../samples/SimpleOrm.Sample/src';

    private TempDatabase $database;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
    }

    /**
     * @param list<string> $arguments
     * @return array{0: int, 1: string, 2: string} exit code, standard output, standard error
     */
    private static function php(array $arguments): array
    {
        $process = proc_open(
            [PHP_BINARY, '-d', 'extension=pdo_sqlite', '-d', 'extension=sqlite3', ...$arguments],
            [1 => ['pipe', 'w'], 2 => ['pipe', 'w']],
            $pipes,
        );
        self::assertIsResource($process);
        $out = (string) stream_get_contents($pipes[1]);
        $err = (string) stream_get_contents($pipes[2]);
        fclose($pipes[1]);
        fclose($pipes[2]);

        return [proc_close($process), $out, $err];
    }

    #[Test]
    public function the_built_in_sink_writes_each_line_and_a_newline_to_standard_output(): void
    {
        $script = sys_get_temp_dir() . '/simpleorm_sink_' . bin2hex(random_bytes(6)) . '.php';
        file_put_contents($script, '<?php require ' . var_export(__DIR__ . '/../../vendor/autoload.php', true)
            . '; $log = SimpleOrm\Session\DbLog::stdout(); $log("User.get: select 1"); $log("second line");');
        try {
            [$exit, $out, $err] = self::php([$script]);
        } finally {
            unlink($script);
        }

        self::assertSame(0, $exit, $err);
        self::assertSame("User.get: select 1\nsecond line\n", str_replace("\r\n", "\n", $out));
    }

    #[Test]
    public function the_log_flag_writes_every_statement_of_a_cli_run_to_standard_output(): void
    {
        $common = [
            __DIR__ . '/../../bin/simpleorm', 'status',
            '--src', self::SRC,
            '--namespace', 'SimpleOrm\\Sample',
            '--db', $this->database->connectionString(),
        ];

        [$exit, $out, $err] = self::php($common);
        self::assertSame(0, $exit, $err);
        self::assertStringNotContainsString('migrate: ', $out);   // off unless asked

        [$exit, $out, $err] = self::php([...$common, '--log']);
        self::assertSame(0, $exit, $err);
        // A fresh database: status asks whether the version table exists and, finding none, reads no history (ADR-0047).
        $out = str_replace("\r\n", "\n", $out);
        self::assertStringContainsString("migrate: select name, type, \"notnull\", pk, ", $out);
        self::assertStringContainsString('from pragma_table_info(@relation) [@relation]', $out);
        self::assertStringNotContainsString('from schema_version', $out);
    }
}

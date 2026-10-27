<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use FilesystemIterator;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SchemaShadow;
use SimpleOrm\Migrations\SchemaSync;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\StatementLog;
use SimpleOrm\Tests\Migrations\Fixtures\Sync\SyncNewWidget;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Session\Fixtures\LoggedRegistry;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;

/**
 * The statement log (ADR-0045, spec/session.md "Statement log") outside the session's own reads and writes: the migration
 * runner writes every statement it runs as `migrate` — the version table, the recorded history, the steps, the recorded row —
 * and none of the dialect's housekeeping (the run lock, `COMMIT`); SchemaGuard writes its describe calls and introspection as
 * `validate <source>`; the shadow writes `shadow` and the force-sync `sync`.
 */
final class StatementLogToolingTest extends TestCase
{
    private TempDatabase $database;

    /** @var list<string> */
    private array $lines = [];

    /** @var list<string> */
    private array $tempDirs = [];

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
        foreach ($this->tempDirs as $dir) {
            self::removeDirectory($dir);
        }
    }

    private function log(bool $values = false): StatementLog
    {
        return new StatementLog(function (string $line): void {
            $this->lines[] = $line;
        }, $values);
    }

    /** @return list<string> */
    private function names(): array
    {
        return array_map(static fn (string $line): string => explode(': ', $line, 2)[0], $this->lines);
    }

    #[Test]
    public function the_migration_runner_writes_every_statement_it_runs_as_migrate_and_no_housekeeping(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $version = new SqlVersion(1, new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT'], ['drop table widgets']));
        $runner = new MigrationRunner($connection, $dialect, new EntityMapLoader(), MigrationSet::of($version), log: $this->log(true));

        $runner->migrate();

        // The version table, the recorded history, the step, the recorded row: four statements, all `migrate`.
        self::assertSame(['migrate', 'migrate', 'migrate', 'migrate'], $this->names());
        self::assertStringContainsString('schema_version', $this->lines[0]);
        self::assertStringContainsString('select version, object, description, checksum from schema_version', $this->lines[1]);
        self::assertStringStartsWith('migrate: create table widgets', $this->lines[2]);
        self::assertStringContainsString('insert into schema_version', $this->lines[3]);
        self::assertStringContainsString("[@version = 1, @object = 'widgets', @description = 'create', @checksum = '", $this->lines[3]);
        foreach ($this->lines as $line) {
            self::assertStringNotContainsString('BEGIN IMMEDIATE', $line);
            self::assertStringNotContainsString('COMMIT', $line);
        }

        $this->lines = [];
        $runner->migrateDown(0);   // the rollback: the derived or hand-written down, then the history row
        self::assertSame(['migrate'], array_values(array_unique($this->names())));
        self::assertTrue(self::anyLineContains($this->lines, 'drop table widgets'));
        self::assertTrue(self::anyLineContains($this->lines, 'delete from schema_version where version = 1'));
    }

    #[Test]
    public function a_runner_without_a_log_writes_nothing_and_a_status_read_is_its_table_check_then_its_history_read(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $version = new SqlVersion(1, new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT'], ['drop table widgets']));

        (new MigrationRunner($connection, $dialect, new EntityMapLoader(), MigrationSet::of($version)))->migrate();
        self::assertSame([], $this->lines);

        (new MigrationRunner($connection, $dialect, new EntityMapLoader(), MigrationSet::of($version), log: $this->log()))->status();
        self::assertSame(['migrate', 'migrate'], $this->names());   // the table check (ADR-0047), then the history
        self::assertStringContainsString('pragma_table_info', $this->lines[0]);
        self::assertStringContainsString('from schema_version', $this->lines[1]);
    }

    #[Test]
    public function schema_guard_writes_its_describe_calls_and_introspection_as_validate_with_the_source(): void
    {
        $lines = [];
        $db = SampleDatabase::open($this->database, new DbOptions(
            new SqliteDialect(),
            log: static function (string $line) use (&$lines): void {
                $lines[] = $line;
            },
        ));
        $lines = [];

        self::assertSame([], SchemaGuard::report($db, [LoggedRegistry::class, User::class]));
        $db->close();

        $names = array_map(static fn (string $line): string => explode(': ', $line, 2)[0], $lines);
        self::assertContains('validate LoggedRegistry.userCount', $names);   // the describe call of a query
        self::assertContains('validate LoggedRegistry.renameUser', $names);   // and of a command (prepared, never run)
        self::assertContains('validate User', $names);   // the column introspection of the entity's relation
        foreach ($names as $name) {
            self::assertStringStartsWith('validate ', $name);
        }

        $describe = array_values(array_filter($lines, static fn (string $line): bool => str_starts_with($line, 'validate LoggedRegistry.renameUser')));
        self::assertCount(1, $describe);
        self::assertStringEndsWith('[@DisplayName, @Id]', $describe[0]);   // a placeholder per parameter, bound NULL; values are off
    }

    #[Test]
    public function the_shadow_writes_its_replay_and_introspection_as_shadow(): void
    {
        $out = sys_get_temp_dir() . '/simpleorm_log_shadow_' . bin2hex(random_bytes(6));
        $this->tempDirs[] = $out;
        $set = MigrationSet::fromDirectory(__DIR__ . '/../Dialect/Fixtures/ShadowApp', 'SimpleOrm\\Tests\\Dialect\\Fixtures\\ShadowApp');

        SchemaShadow::for(new SqliteDialect(), '', $this->log())->rebuildSnapshots($set, new EntityMapLoader(), $out);

        self::assertNotSame([], $this->lines);
        self::assertSame(['shadow'], array_values(array_unique($this->names())));   // the runner's own statements included
        self::assertTrue(self::anyLineContains($this->lines, 'create table'));
        self::assertTrue(self::anyLineContains($this->lines, 'insert into schema_version'));
        self::assertTrue(self::anyLineContains($this->lines, '[@relation]'));   // the introspection queries
    }

    #[Test]
    public function the_force_sync_writes_its_introspection_and_its_plan_as_sync(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $log = $this->log();

        $plan = SchemaSync::plan($connection, $dialect, new EntityMapLoader(), [SyncNewWidget::class], $log);
        SchemaSync::apply($connection, $plan->additive, $log);

        self::assertGreaterThanOrEqual(2, count($this->lines));   // the columns read, then the create table
        self::assertSame(['sync'], array_values(array_unique($this->names())));
        self::assertTrue(self::anyLineContains($this->lines, 'create table'));
    }

    /** @param list<string> $lines */
    private static function anyLineContains(array $lines, string $needle): bool
    {
        foreach ($lines as $line) {
            if (str_contains($line, $needle)) {
                return true;
            }
        }

        return false;
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

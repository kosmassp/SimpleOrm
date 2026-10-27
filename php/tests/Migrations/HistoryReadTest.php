<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PDO;
use PDOException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Session\StatementLog;
use SimpleOrm\Tests\Support\TempDatabase;
use Throwable;

/**
 * How the runner reads the recorded history (ADR-0047, spec/migrations.md "Recording and integrity"): a failure is never
 * mistaken for an empty history. `migrate`, `migrate down`, and `baseline` create the table first in their run
 * transaction, so a failing read fails the run with the driver's error, unwrapped, and nothing applies; `status` reads the
 * history only when the dialect's columns query reports the table, so a fresh database is all pending without a history
 * read, and a present table that cannot be read fails it too. The failing read here is a `schema_version` table of the
 * wrong shape — the read's `description` column does not exist — on a real temp-file SQLite database.
 */
final class HistoryReadTest extends TestCase
{
    private const string HISTORY_READ = 'migrate: select version, object, description, checksum from schema_version';

    private TempDatabase $database;

    private PDO $connection;

    /** @var list<string> */
    private array $lines = [];

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->connection = (new SqliteDialect())->createConnection($this->database->connectionString());
    }

    protected function tearDown(): void
    {
        unset($this->connection);
        $this->database->delete();
    }

    private function runner(): MigrationRunner
    {
        $versions = [
            new SqlVersion(1, new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY) STRICT'], ['drop table widgets'])),
            new SqlVersion(2, new SqlVersionStep('gadgets', 'create', ['create table gadgets (id INTEGER PRIMARY KEY) STRICT'], ['drop table gadgets'])),
        ];

        return new MigrationRunner(
            $this->connection,
            new SqliteDialect(),
            new EntityMapLoader(),
            MigrationSet::of(...$versions),
            log: new StatementLog(function (string $line): void {
                $this->lines[] = $line;
            }),
        );
    }

    private function tableExists(string $name): bool
    {
        $statement = $this->connection->prepare("select count(*) from sqlite_master where type = 'table' and name = :name");
        $statement->execute(['name' => $name]);

        return (int) $statement->fetchColumn() === 1;
    }

    /** @return iterable<string, array{string}> */
    public static function commands(): iterable
    {
        yield 'migrate' => ['migrate'];
        yield 'migrate down' => ['down'];
        yield 'baseline' => ['baseline'];
        yield 'status' => ['status'];
    }

    #[Test]
    #[DataProvider('commands')]
    public function with_the_table_present_a_failing_history_read_fails_the_command_with_the_drivers_error(string $command): void
    {
        $this->connection->exec('create table schema_version (version INTEGER, object TEXT) STRICT');
        $runner = $this->runner();

        $failure = null;
        try {
            match ($command) {
                'migrate' => $runner->migrate(),
                'down' => $runner->migrateDown(0),
                'baseline' => $runner->baseline(2),
                'status' => $runner->status(),
            };
        } catch (Throwable $caught) {
            $failure = $caught;
        }

        self::assertInstanceOf(PDOException::class, $failure);   // the driver's own error, unwrapped (not MIG-021)
        self::assertStringContainsString('description', $failure->getMessage());
        self::assertSame(self::HISTORY_READ, end($this->lines));   // nothing was sent after the read
        self::assertFalse($this->tableExists('widgets'));
        self::assertFalse($this->connection->inTransaction());
    }

    #[Test]
    public function on_a_fresh_database_status_reports_every_entry_pending_without_reading_a_history(): void
    {
        $entries = $this->runner()->status();

        self::assertSame([MigrationState::Pending, MigrationState::Pending], array_map(static fn ($entry) => $entry->state, $entries));
        self::assertCount(1, $this->lines);   // the table check only
        self::assertStringContainsString('pragma_table_info(@relation) [@relation]', $this->lines[0]);
        self::assertFalse($this->tableExists('schema_version'));   // status creates nothing
    }

    #[Test]
    public function on_a_fresh_database_migrate_still_creates_the_table_and_applies_everything(): void
    {
        self::assertSame(2, $this->runner()->migrate());

        self::assertTrue($this->tableExists('schema_version'));
        self::assertSame([MigrationState::Applied, MigrationState::Applied], array_map(static fn ($entry) => $entry->state, $this->runner()->status()));
    }
}

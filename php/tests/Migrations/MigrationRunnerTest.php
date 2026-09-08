<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationEntry;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationSql;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\MigrationStep;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Migrations\Fixtures\Runner\RunnerGadget;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Milestone 5 (PHP port): the versioned migration runner, per scenario and per
 * code. Mirrors `dotnet/tests/SimpleOrm.Tests/MigrationRunnerTests.cs`.
 * Structural composition violations (`MIG-001`-`MIG-004`) are `MigrationSet`'s
 * own area, covered by `MigrationSetTest`; this file exercises what only the
 * runner can: applying, reverting, checksums against recorded history, atomic
 * failure, and baseline.
 */
final class MigrationRunnerTest extends TestCase
{
    private TempDatabase $database;

    private SqliteDialect $dialect;

    private PDO $connection;

    private EntityMapLoader $maps;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->dialect = new SqliteDialect();
        $this->connection = $this->dialect->createConnection($this->database->connectionString());
        $this->maps = new EntityMapLoader();
    }

    protected function tearDown(): void
    {
        unset($this->connection);
        $this->database->delete();
    }

    private function runner(SqlVersion ...$versions): MigrationRunner
    {
        return new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of(...$versions));
    }

    private static function widgets1(string $sql = 'create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT'): SqlVersion
    {
        return new SqlVersion(1, new SqlVersionStep('widgets', 'create', [$sql], ['drop table widgets']));
    }

    #[Test]
    public function migrate_applies_pending_versions_and_is_idempotent(): void
    {
        $runner = $this->runner(self::widgets1());

        self::assertTrue($runner->hasPending());
        self::assertSame(1, $runner->migrate());
        self::assertSame(0, $runner->migrate());
        self::assertFalse($runner->hasPending());

        $status = $runner->status();
        self::assertCount(1, $status);
        self::assertSame(MigrationState::Applied, $status[0]->state);
    }

    #[Test]
    public function actions_reorder_to_rename_add_remove_with_hooks_in_place(): void
    {
        $gadgetV1 = new SqlVersion(1, new SqlVersionStep(
            'runner_gadgets',
            'create',
            [
                'create table runner_gadgets (id INTEGER PRIMARY KEY, label TEXT, legacy TEXT) STRICT',
                "insert into runner_gadgets (label, legacy) values ('hello', 'old')",
            ],
            ['drop table runner_gadgets'],
        ));

        $step = new class extends TableMigration {
            public function version(): int
            {
                return 2;
            }

            public function description(): string
            {
                return 'Restructure';
            }

            public function entityClass(): string
            {
                return RunnerGadget::class;
            }

            // Deliberately declared out of order: the renderer must run rename -> add -> remove.
            public function action(TableActions $actions): void
            {
                $actions->addColumn('label', 'TEXT')->post("update runner_gadgets set label = 'L-' || title");
                $actions->removeColumn('legacy')->pre('update runner_gadgets set title = title || \'-\' || legacy');
                $actions->renameColumn('label', 'title');
            }

            public function down(TableActions $actions): void
            {
                $actions->removeColumn('label');
            }

            public function preDown(MigrationSql $sql): void
            {
                $sql->sql('update runner_gadgets set title = label'); // works only while label still exists
            }

            public function postDown(MigrationSql $sql): void
            {
                $sql->sql("update runner_gadgets set title = title || '!'"); // runs after the DDL
            }
        };
        $gadgetV2 = new class($step) extends MigrationVersion {
            public function __construct(private readonly MigrationStep $step)
            {
            }

            public function version(): int
            {
                return 2;
            }

            public function compose(VersionBuilder $version): void
            {
                $version->apply($this->step);
            }
        };

        $set = MigrationSet::of($gadgetV1, $gadgetV2);
        $runner = new MigrationRunner($this->connection, $this->dialect, $this->maps, $set);
        $runner->migrate();

        $read = $this->connection->prepare('select title, label from runner_gadgets');
        $read->execute();
        $row = $read->fetch(PDO::FETCH_ASSOC);
        self::assertSame('hello-old', $row['title']); // pre-remove ran after the add group, before remove
        self::assertSame('L-hello', $row['label']); // post-add backfilled from the renamed column

        // Down: PreDown (needs label) -> DDL (drops label) -> PostDown.
        $runner->migrateDown(1);
        $titleQuery = $this->connection->query('select title from runner_gadgets');
        self::assertSame('L-hello!', $titleQuery->fetchColumn());
    }

    #[Test]
    public function down_reverts_in_reverse_and_requires_down_statements(): void
    {
        $reversible = $this->runner(self::widgets1());
        $reversible->migrate();
        self::assertSame(1, $reversible->migrateDown(0));
        $status = $reversible->status();
        self::assertFalse(self::anyState($status, MigrationState::Applied));

        $irreversible = $this->runner(new SqlVersion(
            1,
            new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY) STRICT']),
        ));
        $irreversible->migrate();

        try {
            $irreversible->migrateDown(0);
            self::fail('expected MIG-020');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-020', $exception->errorCode);
        }
    }

    #[Test]
    public function checksum_drift_and_unknown_history_fail_before_executing(): void
    {
        $this->runner(self::widgets1())->migrate();

        $drifted = $this->runner(self::widgets1('create table widgets (id INTEGER PRIMARY KEY, extra TEXT) STRICT'));
        try {
            $drifted->migrate();
            self::fail('expected MIG-010');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-010', $exception->errorCode);
        }

        $emptied = $this->runner();
        try {
            $emptied->migrate();
            self::fail('expected MIG-011');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-011', $exception->errorCode);
        }
    }

    #[Test]
    public function failed_run_rolls_back_every_version(): void
    {
        $runner = $this->runner(
            self::widgets1(),
            new SqlVersion(2, new SqlVersionStep('widgets', 'boom', ['this is not sql'])),
        );

        try {
            $runner->migrate();
            self::fail('expected the bad statement to throw');
        } catch (SimpleOrmException) {
            // expected: MIG-021, but what matters here is that nothing survived.
        }

        // BEGIN IMMEDIATE run: nothing survives — not even V1.
        $clean = $this->runner(self::widgets1());
        $status = $clean->status();
        self::assertCount(1, $status);
        self::assertSame(MigrationState::Pending, $status[0]->state);
    }

    #[Test]
    public function baseline_records_without_running(): void
    {
        $runner = $this->runner(self::widgets1());
        $runner->baseline(1);

        self::assertFalse($runner->hasPending());
        self::assertSame(0, $runner->migrate());

        // The table was never actually created — baseline only records.
        $count = $this->connection
            ->query("select count(name) from sqlite_master where type = 'table' and name = 'widgets'")
            ->fetchColumn();
        self::assertSame(0, (int) $count);
    }

    /** @param list<MigrationEntry> $entries */
    private static function anyState(array $entries, MigrationState $state): bool
    {
        foreach ($entries as $entry) {
            if ($entry->state === $state) {
                return true;
            }
        }

        return false;
    }
}

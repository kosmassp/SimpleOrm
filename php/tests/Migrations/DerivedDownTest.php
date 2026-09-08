<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\ColumnRename;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * ADR-0018 (owner): "Down could be deducted from the previous schema" — nobody
 * writes rollback DDL. Mirrors `dotnet/tests/SimpleOrm.Tests/DerivedDownTests.cs`
 * at the `MigrationRunner` level (the pure-`DownDeriver` unit tests live in
 * `DownDeriverTest`): the runner derives the rollback at migrate-down time from
 * the versioned snapshots, inverting the step's typed renames data-preservingly
 * and diffing the rest. `down()` remains the manual override; missing snapshots
 * still refuse (`MIG-020`).
 */
final class DerivedDownTest extends TestCase
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

    /** @return list<SqlVersion> V1 creates widgets(id, name) and seeds 'admin'; V2 adds note; V3 renames name -> full_name. */
    private function history(): array
    {
        return [
            new SqlVersion(1, new SqlVersionStep(
                'widgets',
                'create',
                [
                    'create table widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT',
                    "insert into widgets (name) values ('admin')",
                ],
            )),
            new SqlVersion(2, new SqlVersionStep('widgets', 'add_note', ['alter table widgets add column note TEXT'])),
            new SqlVersion(3, new SqlVersionStep(
                'widgets',
                'rename_name',
                ['alter table widgets rename column name to full_name'],
                renames: [new ColumnRename('name', 'full_name')],
            )),
        ];
    }

    private function historySnapshots(): SnapshotSet
    {
        $idColumn = new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true);
        $documents = [
            SchemaSnapshot::export(
                new TableSchema('widgets', [$idColumn, new TableSchemaColumn('name', 'TEXT', false)], []),
                1,
                new DateTimeImmutable(),
            ),
            SchemaSnapshot::export(
                new TableSchema('widgets', [
                    $idColumn,
                    new TableSchemaColumn('name', 'TEXT', false),
                    new TableSchemaColumn('note', 'TEXT', true),
                ], []),
                2,
                new DateTimeImmutable(),
            ),
            SchemaSnapshot::export(
                new TableSchema('widgets', [
                    $idColumn,
                    new TableSchemaColumn('full_name', 'TEXT', false),
                    new TableSchemaColumn('note', 'TEXT', true),
                ], []),
                3,
                new DateTimeImmutable(),
            ),
        ];

        return new SnapshotSet($documents);
    }

    private function runner(SnapshotSet $snapshots): MigrationRunner
    {
        return new MigrationRunner(
            $this->connection,
            $this->dialect,
            $this->maps,
            MigrationSet::of(...$this->history()),
            $snapshots,
        );
    }

    #[Test]
    public function the_whole_history_rolls_back_and_reapplies_without_any_down_code(): void
    {
        $runner = $this->runner($this->historySnapshots());

        self::assertSame(3, $runner->migrate());

        // No step overrides down(); every rollback below is derived — the
        // rename reverses, the added column drops, the create reverts to a
        // drop table.
        self::assertSame(3, $runner->migrateDown(0));
        self::assertSame(0, $this->countTables());

        // And the same plan applies cleanly again: down really reached V0000.
        self::assertSame(3, $runner->migrate());
        self::assertSame('admin', $this->scalar('select full_name from widgets'));
    }

    #[Test]
    public function partial_rollback_reverses_the_rename_and_keeps_data(): void
    {
        $runner = $this->runner($this->historySnapshots());
        $runner->migrate();

        // Down past V3 (name -> full_name): the derived rollback inverts the
        // rename, so the V1 'admin' seed survives under the old column name.
        $runner->migrateDown(2);

        self::assertSame('admin', $this->scalar('select name from widgets'));
    }

    #[Test]
    public function underivable_constraint_is_noticed_not_guessed(): void
    {
        // History: V1 creates derived_widgets(id, label NOT NULL); V2 drops label.
        $versions = [
            new SqlVersion(1, new SqlVersionStep(
                'derived_widgets',
                'create',
                ['create table derived_widgets (id INTEGER PRIMARY KEY, label TEXT NOT NULL) STRICT'],
            )),
            new SqlVersion(2, new SqlVersionStep('derived_widgets', 'drop_label', ['alter table derived_widgets drop column label'])),
        ];

        $idColumn = new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true);
        $snapshots = new SnapshotSet([
            SchemaSnapshot::export(
                new TableSchema('derived_widgets', [$idColumn, new TableSchemaColumn('label', 'TEXT', false)], []),
                1,
                new DateTimeImmutable(),
            ),
            SchemaSnapshot::export(new TableSchema('derived_widgets', [$idColumn], []), 2, new DateTimeImmutable()),
        ]);

        $withSnapshots = new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of(...$versions), $snapshots);
        $withSnapshots->migrate();

        // The structure returns nullable; the NOT NULL constraint (and the data) can't derive.
        $notices = [];
        self::assertSame(1, $withSnapshots->migrateDown(1, false, function (string $notice) use (&$notices): void {
            $notices[] = $notice;
        }));
        self::assertTrue(self::anyContains($notices, 'label') && self::anyContains($notices, 'not derivable'));

        // Without snapshots the same plan still refuses honestly (MIG-020).
        $withSnapshots->migrate();
        $blind = new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of(...$versions));
        try {
            $blind->migrateDown(1);
            self::fail('expected MIG-020');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-020', $exception->errorCode);
        }
    }

    private function countTables(): int
    {
        return (int) $this->scalar(
            "select count(*) from sqlite_master where type in ('table', 'view') "
                . "and name not like 'sqlite_%' and name <> 'schema_version'",
        );
    }

    private function scalar(string $sql): mixed
    {
        return $this->connection->query($sql)->fetchColumn();
    }

    /** @param list<string> $items */
    private static function anyContains(array $items, string $needle): bool
    {
        foreach ($items as $item) {
            if (str_contains($item, $needle)) {
                return true;
            }
        }

        return false;
    }
}

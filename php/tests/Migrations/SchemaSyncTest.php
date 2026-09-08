<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\SchemaSync;
use SimpleOrm\Tests\Migrations\Fixtures\Sync\SyncAddWidget;
use SimpleOrm\Tests\Migrations\Fixtures\Sync\SyncBadWidget;
use SimpleOrm\Tests\Migrations\Fixtures\Sync\SyncIndexedWidget;
use SimpleOrm\Tests\Migrations\Fixtures\Sync\SyncNewWidget;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * ADR-0017: force sync (`migrate --force`). Mirrors
 * `dotnet/tests/SimpleOrm.Tests/SchemaSyncTests.cs`: additive fixes are planned
 * and applied; deletions are planned separately (gated by an allow-delete flag
 * at the CLI layer); type and nullability changes are never auto-applied
 * (`DDL-004`); indexes match structurally, never by name (ADR-0017 add.2).
 */
final class SchemaSyncTest extends TestCase
{
    private TempDatabase $database;

    private SqliteDialect $dialect;

    private \PDO $connection;

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

    #[Test]
    public function missing_table_is_created_additively(): void
    {
        $plan = SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncNewWidget::class]);

        self::assertTrue(self::anyMatches($plan->additive, static fn (string $sql): bool => str_starts_with($sql, 'create table if not exists sync_new_widgets')));
        self::assertSame([], $plan->deletions);
        self::assertSame([], $plan->unsupported);

        SchemaSync::apply($this->connection, $plan->additive);
        self::assertTrue(SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncNewWidget::class])->isEmpty());
    }

    #[Test]
    public function missing_nullable_column_is_additive_and_extra_column_is_a_deletion(): void
    {
        SchemaSync::apply($this->connection, [
            'create table sync_add_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, junk INTEGER) STRICT',
        ]);

        $plan = SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncAddWidget::class]);
        self::assertSame(['alter table sync_add_widgets add column note TEXT'], $plan->additive);
        self::assertSame(['alter table sync_add_widgets drop column junk'], $plan->deletions);
        self::assertSame([], $plan->unsupported);

        SchemaSync::apply($this->connection, [...$plan->additive, ...$plan->deletions]);
        self::assertTrue(SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncAddWidget::class])->isEmpty());
    }

    #[Test]
    public function index_matching_is_structural_not_by_name(): void
    {
        // The model declares ix_sync_indexed_widgets_name on (name); the DBA
        // added the same index under another name in an urgency: implemented.
        SchemaSync::apply($this->connection, [
            'create table sync_indexed_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT) STRICT',
            'create index idx_dba_hotfix on sync_indexed_widgets (name)',
        ]);
        self::assertTrue(SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncIndexedWidget::class])->isEmpty());

        // A structurally different index is not: the model's gets created, the
        // stranger is a (gated) deletion.
        SchemaSync::apply($this->connection, [
            'drop index idx_dba_hotfix',
            'create index idx_dba_hotfix on sync_indexed_widgets (note)',
        ]);
        $plan = SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncIndexedWidget::class]);
        self::assertCount(1, $plan->additive);
        self::assertStringContainsString('ix_sync_indexed_widgets_name', $plan->additive[0]);
        self::assertSame(['drop index idx_dba_hotfix'], $plan->deletions);
        self::assertSame([], $plan->unsupported);
    }

    #[Test]
    public function type_and_nullability_changes_are_never_auto_applied(): void
    {
        SchemaSync::apply($this->connection, ['create table sync_bad_widgets (id INTEGER PRIMARY KEY, note INTEGER) STRICT']);

        $plan = SchemaSync::plan($this->connection, $this->dialect, $this->maps, [SyncBadWidget::class]);
        self::assertSame([], $plan->additive);
        self::assertSame([], $plan->deletions);
        self::assertCount(1, $plan->unsupported);
        self::assertStringContainsString('sync_bad_widgets.note', $plan->unsupported[0]);
        self::assertStringContainsString('INTEGER', $plan->unsupported[0]);
    }

    /** @param list<string> $items @param callable(string): bool $predicate */
    private static function anyMatches(array $items, callable $predicate): bool
    {
        foreach ($items as $item) {
            if ($predicate($item)) {
                return true;
            }
        }

        return false;
    }
}

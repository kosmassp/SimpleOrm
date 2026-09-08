<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Migrations\ColumnRename;
use SimpleOrm\Migrations\DownDeriver;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;

/**
 * Mirrors `dotnet/tests/SimpleOrm.Tests/DerivedDownTests.cs` and the relevant
 * `conformance/migrations-cases/derived_down_table.json` /
 * `view_derived_down.json` / `derived_down_refuses_type_change.json`
 * scenarios, exercising `DownDeriver` directly against a `SnapshotSet` — the
 * runner phase (`migrate down`, `MIG-012` live-view checks) is a later area.
 */
final class DownDeriverTest extends TestCase
{
    private SqliteDialect $dialect;

    protected function setUp(): void
    {
        $this->dialect = new SqliteDialect();
    }

    #[Test]
    public function no_snapshot_at_the_version_derives_nothing(): void
    {
        $notices = [];
        $result = DownDeriver::derive('widgets', 1, new SnapshotSet(), [], $notices, $this->dialect);

        self::assertNull($result);
    }

    #[Test]
    public function a_create_reverts_to_a_drop_table(): void
    {
        $snapshots = $this->tableSnapshots([
            1 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false)], []),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('widgets', 1, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertCount(1, $statements);
        self::assertSame('drop table widgets', $statements[0]->sql);
    }

    #[Test]
    public function an_added_column_reverts_to_a_drop_and_a_removed_column_is_restored(): void
    {
        $snapshots = $this->tableSnapshots([
            1 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false)], []),
            2 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false), $this->column('note', 'TEXT', true)], []),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('widgets', 2, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertSame(['alter table widgets drop column note'], array_map(static fn ($s) => $s->sql, $statements));
        self::assertSame([], $notices);
    }

    #[Test]
    public function rename_inverts_data_preservingly(): void
    {
        $snapshots = $this->tableSnapshots([
            2 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false)], []),
            3 => new TableSchema('widgets', [$this->idColumn(), $this->column('full_name', 'TEXT', false)], []),
        ]);

        $notices = [];
        $renames = [new ColumnRename('name', 'full_name')];
        $statements = DownDeriver::derive('widgets', 3, $snapshots, $renames, $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertSame(
            ['alter table widgets rename column full_name to name'],
            array_map(static fn ($s) => $s->sql, $statements),
        );
    }

    #[Test]
    public function a_not_null_column_restored_by_drop_lands_nullable_with_a_notice(): void
    {
        $snapshots = $this->tableSnapshots([
            1 => new TableSchema('widgets', [$this->idColumn(), $this->column('label', 'TEXT', false)], []),
            2 => new TableSchema('widgets', [$this->idColumn()], []),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('widgets', 2, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertSame(['alter table widgets add column label TEXT'], array_map(static fn ($s) => $s->sql, $statements));
        self::assertCount(1, $notices);
        self::assertStringContainsString('label', $notices[0]);
        self::assertStringContainsString('not derivable', $notices[0]);
    }

    #[Test]
    public function a_type_change_refuses_with_mig_020(): void
    {
        $snapshots = $this->tableSnapshots([
            1 => new TableSchema('widgets', [$this->idColumn(), $this->column('price', 'TEXT', false)], []),
            2 => new TableSchema('widgets', [$this->idColumn(), $this->column('price', 'INTEGER', false)], []),
        ]);

        $notices = [];
        try {
            DownDeriver::derive('widgets', 2, $snapshots, [], $notices, $this->dialect);
            self::fail('expected MIG-020');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-020', $exception->errorCode);
        }
    }

    #[Test]
    public function indexes_revert_structurally(): void
    {
        $withIndex = new TableSchemaIndex('ix_widgets_name', [new TableSchemaIndexPart('name')]);
        $snapshots = $this->tableSnapshots([
            1 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false)], [$withIndex]),
            2 => new TableSchema('widgets', [$this->idColumn(), $this->column('name', 'TEXT', false)], []),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('widgets', 2, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertStringContainsString('ix_widgets_name', implode(' ', array_map(static fn ($s) => $s->sql, $statements)));
    }

    #[Test]
    public function a_view_create_reverts_to_a_guarded_drop(): void
    {
        $snapshots = new SnapshotSet([
            SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 1 as answer', 1, new DateTimeImmutable()),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('totals', 1, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertCount(2, $statements);
        self::assertSame('create view totals as select 1 as answer', $statements[0]->sql);
        self::assertSame('totals', $statements[0]->guardView);
        self::assertSame('drop view if exists totals', $statements[1]->sql);
    }

    #[Test]
    public function a_view_change_reverts_to_a_guarded_drop_and_the_previous_definition(): void
    {
        $snapshots = new SnapshotSet([
            SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 1 as answer', 1, new DateTimeImmutable()),
            SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 2 as answer', 2, new DateTimeImmutable()),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('totals', 2, $snapshots, [], $notices, $this->dialect);

        self::assertNotNull($statements);
        self::assertSame('create view totals as select 2 as answer', $statements[0]->sql);
        self::assertSame('totals', $statements[0]->guardView);
        self::assertSame('drop view if exists totals', $statements[1]->sql);
        self::assertSame('create view totals as select 1 as answer', $statements[2]->sql);
    }

    private function idColumn(): TableSchemaColumn
    {
        return new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true);
    }

    private function column(string $name, string $type, bool $nullable): TableSchemaColumn
    {
        return new TableSchemaColumn($name, $type, $nullable);
    }

    /** @param array<int, TableSchema> $byVersion */
    private function tableSnapshots(array $byVersion): SnapshotSet
    {
        $documents = [];
        foreach ($byVersion as $version => $schema) {
            $documents[] = SchemaSnapshot::export($schema, $version, new DateTimeImmutable());
        }

        return new SnapshotSet($documents);
    }
}

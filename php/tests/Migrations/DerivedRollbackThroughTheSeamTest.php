<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Migrations\ColumnRename;
use SimpleOrm\Migrations\DownDeriver;
use SimpleOrm\Migrations\MigrationStatement;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;

/**
 * The derived rollback renders every statement through the dialect seam, in the order ADR-0043 fixes: renames, the
 * type/nullability refusal, the indexes this version added dropped FIRST, restored columns added, added columns
 * dropped, the previous indexes recreated (SQLite and SQL Server refuse to drop a column an index still covers).
 */
final class DerivedRollbackThroughTheSeamTest extends TestCase
{
    #[Test]
    public function the_added_indexes_drop_before_the_columns_and_the_previous_indexes_return_last(): void
    {
        $dialect = Dialects::create(Dialects::SQLITE);
        $snapshots = new SnapshotSet([
            self::table('widgets', 1, [['id', 'INTEGER', false, true], ['name', 'TEXT', false], ['legacy', 'TEXT', true]], [['ix_widgets_name', ['name']]]),
            self::table('widgets', 2, [['id', 'INTEGER', false, true], ['name', 'TEXT', false], ['fresh', 'TEXT', true]], [['ix_widgets_fresh', ['fresh']]]),
        ]);

        self::assertSame(
            [
                'drop index ix_widgets_fresh',
                'alter table widgets add column legacy TEXT',
                'alter table widgets drop column fresh',
                'create index if not exists ix_widgets_name on widgets (name)',
            ],
            self::derive($snapshots, $dialect),
        );
    }

    #[Test]
    public function a_rename_inverts_first_then_the_order_holds(): void
    {
        $dialect = Dialects::create(Dialects::SQLITE);
        $snapshots = new SnapshotSet([
            self::table('widgets', 1, [['id', 'INTEGER', false, true], ['name', 'TEXT', false]], []),
            self::table('widgets', 2, [['id', 'INTEGER', false, true], ['full_name', 'TEXT', false], ['note', 'TEXT', true]], [['ix_widgets_note', ['note']]]),
        ]);

        self::assertSame(
            [
                'alter table widgets rename column full_name to name',
                'drop index ix_widgets_note',
                'alter table widgets drop column note',
            ],
            self::derive($snapshots, $dialect, [new ColumnRename('name', 'full_name')]),
        );
    }

    #[Test]
    public function postgresql_renders_quoted_statements_and_the_unique_index_in_its_form(): void
    {
        $dialect = Dialects::create(Dialects::POSTGRES);
        $snapshots = new SnapshotSet([
            self::table('widgets', 1, [['id', 'bigint', false, true], ['legacy', 'text', true]], [['ix_widgets_legacy', ['legacy'], true]], 'postgres'),
            self::table('widgets', 2, [['id', 'bigint', false, true], ['fresh', 'text', true]], [['ix_widgets_fresh', ['fresh']]], 'postgres'),
        ]);

        self::assertSame(
            [
                'drop index "ix_widgets_fresh"',
                'alter table "widgets" add column "legacy" text',
                'alter table "widgets" drop column "fresh"',
                'create unique index if not exists "ix_widgets_legacy" on "widgets" ("legacy")',
            ],
            self::derive($snapshots, $dialect),
        );
    }

    #[Test]
    public function sql_server_names_the_table_in_the_index_drop_and_drops_a_default_constraint_first(): void
    {
        $dialect = Dialects::create(Dialects::SQLSERVER);
        $snapshots = new SnapshotSet([
            self::table('widgets', 1, [['id', 'bigint', false, true], ['name', 'nvarchar(max)', false]], [], 'sqlserver'),
            self::table('widgets', 2, [['id', 'bigint', false, true], ['name', 'nvarchar(max)', false], ['note', 'nvarchar(450)', true]], [['ix_widgets_note', ['note']]], 'sqlserver'),
        ]);

        $statements = self::derive($snapshots, $dialect);

        self::assertCount(2, $statements);
        self::assertSame('drop index [ix_widgets_note] on [widgets]', $statements[0]);
        self::assertStringStartsWith('declare @df sysname = ', $statements[1]);
        self::assertStringEndsWith('alter table [widgets] drop column [note]', $statements[1]);
    }

    #[Test]
    public function a_view_restores_from_the_neutral_snapshot_through_the_dialect(): void
    {
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');
        $snapshots = static fn (string $dialect): SnapshotSet => new SnapshotSet([
            SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 1 as answer', 1, $at, $dialect),
            SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 2 as answer', 2, $at, $dialect),
        ]);

        $expected = [
            Dialects::SQLITE => ['drop view if exists totals', "create view if not exists totals as\nselect 1 as answer"],
            Dialects::SQLSERVER => ['drop view if exists [totals]', "create or alter view [totals] as\nselect 1 as answer"],
            Dialects::POSTGRES => ['drop view if exists "totals"', "create or replace view \"totals\" as\nselect 1 as answer"],
        ];
        foreach ($expected as $name => [$drop, $restore]) {
            $notices = [];
            $statements = DownDeriver::derive('totals', 2, $snapshots($name), [], $notices, Dialects::create($name));

            self::assertNotNull($statements, $name);
            self::assertSame('create view totals as select 2 as answer', $statements[0]->sql, "{$name}: the guard expects this version's neutral definition");
            self::assertSame('totals', $statements[0]->guardView);
            self::assertSame($drop, $statements[1]->sql, $name);
            self::assertSame($restore, $statements[2]->sql, $name);
        }
    }

    #[Test]
    public function a_materialized_view_drops_and_restores_as_one(): void
    {
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');
        $snapshots = new SnapshotSet([
            SchemaSnapshot::exportDdl('totals', 'materialized_view', 'create materialized view totals as select 1 as answer', 1, $at, 'postgres'),
            SchemaSnapshot::exportDdl('totals', 'materialized_view', 'create materialized view totals as select 2 as answer', 2, $at, 'postgres'),
        ]);

        $notices = [];
        $statements = DownDeriver::derive('totals', 2, $snapshots, [], $notices, Dialects::create(Dialects::POSTGRES));

        self::assertNotNull($statements);
        self::assertSame('drop materialized view if exists "totals"', $statements[1]->sql);
        self::assertSame("create materialized view if not exists \"totals\" as\nselect 1 as answer", $statements[2]->sql);
    }

    // --- helpers -----------------------------------------------------------------------------

    /**
     * @param list<ColumnRename> $renames
     * @return list<string> the derived statements' SQL, version 2 against version 1
     */
    private static function derive(SnapshotSet $snapshots, Dialect $dialect, array $renames = []): array
    {
        $notices = [];
        $statements = DownDeriver::derive('widgets', 2, $snapshots, $renames, $notices, $dialect);
        self::assertNotNull($statements);

        return array_map(static fn (MigrationStatement $statement): string => $statement->sql, $statements);
    }

    /**
     * @param list<array{0: string, 1: string, 2: bool, 3?: bool}> $columns name, storage type, not null, key (generated)
     * @param list<array{0: string, 1: list<string>, 2?: bool}> $indexes name, columns, unique
     */
    private static function table(string $name, int $version, array $columns, array $indexes, string $dialect = 'sqlite'): string
    {
        $schema = new TableSchema(
            $name,
            array_map(
                static fn (array $c): TableSchemaColumn => new TableSchemaColumn($c[0], $c[1], !$c[2], $c[3] ?? false, $c[3] ?? false),
                $columns,
            ),
            array_map(
                static fn (array $i): TableSchemaIndex => new TableSchemaIndex(
                    $i[0],
                    array_map(static fn (string $column): TableSchemaIndexPart => new TableSchemaIndexPart($column), $i[1]),
                    $i[2] ?? false,
                ),
                $indexes,
            ),
        );

        return SchemaSnapshot::export($schema, $version, new DateTimeImmutable('2026-10-22T12:00:00Z'), $dialect);
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\MigrationGenerator;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TableSchemaIndex;
use SimpleOrm\Migrations\TableSchemaIndexPart;
use SimpleOrm\Tests\Migrations\Support\PropertyMapFactory;
use SimpleOrm\Tests\Migrations\Support\Widget;

/**
 * Mirrors `dotnet/tests/SimpleOrm.Tests/GeneratorTests.cs` (ADR-0017): metadata
 * is the final truth, the latest snapshot is the past, the diff is the
 * migration. Renames are declared, never inferred; destructive and
 * inexpressible changes are reported (gating is the future CLI's job,
 * `DDL-003`/`DDL-004`).
 */
final class MigrationGeneratorTest extends TestCase
{
    private SqliteDialect $dialect;

    protected function setUp(): void
    {
        $this->dialect = new SqliteDialect();
    }

    private function map(): EntityMap
    {
        return new EntityMap(
            entityType: Widget::class,
            kind: RelationKind::Table,
            relationName: 'gen_widgets',
            schema: null,
            definingSql: null,
            statementParameters: [],
            properties: [
                PropertyMapFactory::make(Widget::class, 'id', 'id', ColumnType::Int64, key: true, generated: true),
                PropertyMapFactory::make(Widget::class, 'name', 'name', ColumnType::String),
                PropertyMapFactory::make(Widget::class, 'note', 'note', ColumnType::String, nullable: true),
            ],
            keyStrategy: KeyStrategy::DatabaseGenerated,
            indexes: [$this->currentIndex()],
            relationships: [],
        );
    }

    private function currentIndex(): EntityIndex
    {
        return new EntityIndex('ix_gen_widgets_name', [new IndexColumn('name', 'name', false)], false);
    }

    private function id(): TableSchemaColumn
    {
        return new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true);
    }

    private function nameColumn(): TableSchemaColumn
    {
        return new TableSchemaColumn('name', 'TEXT', false);
    }

    private function noteColumn(): TableSchemaColumn
    {
        return new TableSchemaColumn('note', 'TEXT', true);
    }

    /**
     * @param list<TableSchemaColumn> $columns
     * @param list<TableSchemaIndex>|null $indexes
     */
    private function snapshot(array $columns, ?array $indexes = null): TableSchema
    {
        return new TableSchema('gen_widgets', $columns, $indexes ?? [$this->currentSnapshotIndex()]);
    }

    private function currentSnapshotIndex(): TableSchemaIndex
    {
        return new TableSchemaIndex('ix_gen_widgets_name', [new TableSchemaIndexPart('name')]);
    }

    #[Test]
    public function missing_snapshot_means_new_table(): void
    {
        $diff = MigrationGenerator::diffMap($this->map(), $this->dialect, null, []);

        self::assertTrue($diff->isNew);
        self::assertTrue($diff->hasChanges());
    }

    #[Test]
    public function identical_snapshot_means_no_changes(): void
    {
        $diff = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot([$this->id(), $this->nameColumn(), $this->noteColumn()]),
            [],
        );

        self::assertFalse($diff->hasChanges());
        self::assertSame([], $diff->unsupported);
    }

    #[Test]
    public function nullable_addition_is_generated_and_non_nullable_is_not(): void
    {
        $addNote = MigrationGenerator::diffMap($this->map(), $this->dialect, $this->snapshot([$this->id(), $this->nameColumn()]), []);
        self::assertCount(1, $addNote->added);
        self::assertSame('note', $addNote->added[0]->name);

        // Adding NOT NULL "name" needs a default/backfill: DDL-004 territory, write it by hand.
        $addName = MigrationGenerator::diffMap($this->map(), $this->dialect, $this->snapshot([$this->id(), $this->noteColumn()]), []);
        self::assertNotEmpty(array_filter($addName->unsupported, static fn ($m) => str_contains($m, 'name')));
        self::assertSame([], $addName->added);
    }

    #[Test]
    public function removed_column_and_type_change_are_detected(): void
    {
        $removed = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot([$this->id(), $this->nameColumn(), $this->noteColumn(), new TableSchemaColumn('legacy', 'TEXT', true)]),
            [],
        );
        self::assertCount(1, $removed->removed);
        self::assertSame('legacy', $removed->removed[0]->name);

        $retyped = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot([$this->id(), $this->nameColumn(), new TableSchemaColumn('note', 'INTEGER', true)]),
            [],
        );
        self::assertNotEmpty(array_filter(
            $retyped->unsupported,
            static fn ($m) => str_contains($m, 'note') && str_contains($m, 'INTEGER'),
        ));
    }

    #[Test]
    public function declared_rename_is_a_rename_not_add_plus_remove(): void
    {
        $snapshot = $this->snapshot([$this->id(), $this->nameColumn(), new TableSchemaColumn('remark', 'TEXT', true)]);
        $diff = MigrationGenerator::diffMap($this->map(), $this->dialect, $snapshot, ['remark' => 'note']);

        self::assertCount(1, $diff->renamed);
        self::assertSame('remark', $diff->renamed[0]->from);
        self::assertSame('note', $diff->renamed[0]->to);
        self::assertSame([], $diff->added);
        self::assertSame([], $diff->removed);

        // Without the declaration the same shapes read as add + remove (never inferred).
        $undeclared = MigrationGenerator::diffMap($this->map(), $this->dialect, $snapshot, []);
        self::assertCount(1, $undeclared->added);
        self::assertSame('note', $undeclared->added[0]->name);
        self::assertCount(1, $undeclared->removed);
        self::assertSame('remark', $undeclared->removed[0]->name);
    }

    #[Test]
    public function index_additions_and_removals_are_diffed_structurally(): void
    {
        $noIndex = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot([$this->id(), $this->nameColumn(), $this->noteColumn()], []),
            [],
        );
        self::assertCount(1, $noIndex->addedIndexSql);
        self::assertStringContainsString('ix_gen_widgets_name', $noIndex->addedIndexSql[0]);

        $staleIndex = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot(
                [$this->id(), $this->nameColumn(), $this->noteColumn()],
                [$this->currentSnapshotIndex(), new TableSchemaIndex('ix_old', [new TableSchemaIndexPart('note')])],
            ),
            [],
        );
        self::assertSame(['ix_old'], $staleIndex->removedIndexNames);
    }

    #[Test]
    public function index_that_exists_under_another_name_counts_as_implemented(): void
    {
        // ADR-0017 add.2: indexes get added directly to the database in
        // urgencies — identity is the indexed columns, not the name.
        $renamedOnly = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot(
                [$this->id(), $this->nameColumn(), $this->noteColumn()],
                [new TableSchemaIndex('idx_dba_hotfix', [new TableSchemaIndexPart('name')])],
            ),
            [],
        );
        self::assertFalse($renamedOnly->hasChanges());

        // Uniqueness is structure: a unique index is not the plain one the model wants.
        $uniqueMismatch = MigrationGenerator::diffMap(
            $this->map(),
            $this->dialect,
            $this->snapshot(
                [$this->id(), $this->nameColumn(), $this->noteColumn()],
                [new TableSchemaIndex('idx_dba_hotfix', [new TableSchemaIndexPart('name')], unique: true)],
            ),
            [],
        );
        self::assertCount(1, $uniqueMismatch->addedIndexSql);
        self::assertSame(['idx_dba_hotfix'], $uniqueMismatch->removedIndexNames);
    }

    #[Test]
    public function emitted_new_table_step_is_literal_and_has_no_down(): void
    {
        $diff = MigrationGenerator::diffMap($this->map(), $this->dialect, null, []);
        $code = MigrationGenerator::emitTableStep(
            'My\\Migrations',
            Widget::class,
            $this->map(),
            $this->dialect,
            3,
            'CreateWidgets',
            $diff,
        );

        self::assertStringContainsString("namespace My\\Migrations\\Table\\Widget;", $code);
        self::assertStringContainsString('class V0003_CreateWidgets extends \\SimpleOrm\\Migrations\\TableMigration', $code);
        self::assertStringContainsString('create table if not exists gen_widgets', $code);
        self::assertStringContainsString('ix_gen_widgets_name', $code);
        self::assertStringNotContainsString('function down(', $code);
        self::assertValidPhp($code);
    }

    #[Test]
    public function emitted_change_step_has_literal_actions_and_no_down(): void
    {
        $snapshot = $this->snapshot([
            $this->id(),
            $this->nameColumn(),
            new TableSchemaColumn('remark', 'TEXT', true),
            new TableSchemaColumn('legacy', 'TEXT', true),
        ]);
        $diff = MigrationGenerator::diffMap($this->map(), $this->dialect, $snapshot, ['remark' => 'note']);
        $code = MigrationGenerator::emitTableStep('My\\Migrations', Widget::class, $this->map(), $this->dialect, 4, 'Reshape', $diff);

        self::assertStringContainsString("\$actions->renameColumn('remark', 'note');", $code);
        self::assertStringContainsString("\$actions->removeColumn('legacy');", $code);
        self::assertStringNotContainsString('function down(', $code);
        self::assertValidPhp($code);
    }

    #[Test]
    public function emitted_view_create_is_literal_and_has_no_down(): void
    {
        $code = MigrationGenerator::emitViewStep(
            'My\\Migrations',
            Widget::class,
            'View',
            'widget_totals',
            5,
            'CreateTotals',
            'create view widget_totals as select 1 as one',
            null,
        );

        self::assertStringContainsString("namespace My\\Migrations\\View\\Widget;", $code);
        self::assertStringContainsString("\$actions->sql('create view widget_totals as select 1 as one');", $code);
        self::assertStringNotContainsString('function down(', $code);
        self::assertStringNotContainsString('expectDefinition', $code);
        self::assertValidPhp($code);
    }

    #[Test]
    public function emitted_view_change_guards_the_previous_definition_and_has_no_down(): void
    {
        $code = MigrationGenerator::emitViewStep(
            'My\\Migrations',
            Widget::class,
            'View',
            'widget_totals',
            6,
            'AddTwo',
            'create view widget_totals as select 1 as one, 2 as two',
            'create view widget_totals as select 1 as one',
        );

        self::assertStringContainsString("\$actions->expectDefinition('create view widget_totals as select 1 as one');", $code);
        self::assertStringContainsString("\$actions->sql('drop view if exists widget_totals');", $code);
        self::assertStringContainsString("\$actions->sql('create view widget_totals as select 1 as one, 2 as two');", $code);
        self::assertStringNotContainsString('function down(', $code);
        self::assertValidPhp($code);
    }

    #[Test]
    public function emitted_root_composes_steps_in_order(): void
    {
        $code = MigrationGenerator::emitRoot(
            'My\\Migrations',
            4,
            ['My\\Migrations\\Table\\Widget\\V0004_Reshape', 'My\\Migrations\\Table\\Other\\V0004_Reshape'],
            'sqlite',
        );

        self::assertStringContainsString('class V0004 extends \\SimpleOrm\\Migrations\\MigrationVersion', $code);
        self::assertStringContainsString('->apply(\\My\\Migrations\\Table\\Widget\\V0004_Reshape::class)', $code);
        self::assertStringContainsString('->apply(\\My\\Migrations\\Table\\Other\\V0004_Reshape::class);', $code);

        // The root remembers its dialect (ADR-0017 add.3): a future amend refuses a switch.
        self::assertTrue(MigrationGenerator::isGenerated($code));
        self::assertSame('sqlite', MigrationGenerator::generatedDialectLabel($code));
        self::assertNull(MigrationGenerator::generatedDialectLabel('/** Generated by simpleorm diff (ADR-0017); legacy */'));
        self::assertFalse(MigrationGenerator::isGenerated('final class V0004 extends MigrationVersion {}'));
        self::assertValidPhp($code);
    }

    /** Every emitted-source assertion above also has to be code a PHP parser accepts. */
    private static function assertValidPhp(string $code): void
    {
        $path = tempnam(sys_get_temp_dir(), 'simpleorm_gen_') . '.php';
        file_put_contents($path, $code);
        exec('php -l ' . escapeshellarg($path) . ' 2>&1', $output, $exitCode);
        unlink($path);

        self::assertSame(0, $exitCode, "generated code is not valid PHP:\n" . implode("\n", $output) . "\n\n" . $code);
    }
}

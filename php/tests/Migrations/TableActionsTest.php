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
use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Tests\Migrations\Support\PropertyMapFactory;
use SimpleOrm\Tests\Migrations\Support\Widget;

/** Mirrors the C# `TableActions` behavior described in Migration.cs: fixed execution order, hooks, dialect-rendered SQL. */
final class TableActionsTest extends TestCase
{
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
            indexes: [new EntityIndex('ix_gen_widgets_name', [new IndexColumn('name', 'name', false)], false)],
            relationships: [],
        );
    }

    private function actions(): TableActions
    {
        return new TableActions($this->map(), new SqliteDialect());
    }

    #[Test]
    public function create_table_renders_the_table_and_its_indexes(): void
    {
        $statements = $this->actions()->createTable()->render();

        self::assertCount(2, $statements);
        self::assertStringContainsString('create table if not exists gen_widgets', $statements[0]->sql);
        self::assertStringContainsString('ix_gen_widgets_name', $statements[1]->sql);
    }

    #[Test]
    public function execution_order_is_rename_then_add_then_remove_then_sql_regardless_of_declaration_order(): void
    {
        $actions = $this->actions();
        // Declared out of order on purpose.
        $actions->sql('vacuum');
        $actions->removeColumn('note');
        $actions->addColumn('extra', 'TEXT');
        $actions->renameColumn('name', 'full_name');

        $origins = array_map(static fn ($s) => $s->origin, $actions->build());

        self::assertSame([
            'rename gen_widgets.name',
            'add gen_widgets.extra',
            'remove gen_widgets.note',
            'sql gen_widgets',
        ], $origins);
    }

    #[Test]
    public function declaration_order_is_kept_within_a_group(): void
    {
        $actions = $this->actions();
        $actions->addColumn('second', 'TEXT');
        $actions->addColumn('first', 'TEXT');

        $sql = array_map(static fn ($s) => $s->sql, $actions->build());

        self::assertSame(
            ['alter table gen_widgets add column second TEXT', 'alter table gen_widgets add column first TEXT'],
            $sql,
        );
    }

    #[Test]
    public function pre_and_post_hooks_wrap_the_action_statement(): void
    {
        $actions = $this->actions();
        $actions->addColumn('extra', 'TEXT')
            ->pre('update gen_widgets set note = extra')
            ->post('update gen_widgets set extra = 0 where extra is null');

        $statements = $actions->build();
        self::assertCount(3, $statements);
        self::assertSame('update gen_widgets set note = extra', $statements[0]->sql);
        self::assertStringContainsString('add column extra', $statements[1]->sql);
        self::assertSame('update gen_widgets set extra = 0 where extra is null', $statements[2]->sql);
        self::assertStringEndsWith(' pre', $statements[0]->origin);
        self::assertStringEndsWith(' post', $statements[2]->origin);
    }

    #[Test]
    public function rename_column_is_tracked_structurally_for_the_derived_rollback(): void
    {
        $actions = $this->actions();
        $actions->renameColumn('name', 'full_name');

        $renames = $actions->columnRenames();
        self::assertCount(1, $renames);
        self::assertSame('name', $renames[0]->from);
        self::assertSame('full_name', $renames[0]->to);
    }

    #[Test]
    public function add_column_renders_nullability_and_default(): void
    {
        $sql = $this->actions()->addColumn('score', 'INTEGER', false, '0')->render()[0]->sql;

        self::assertSame('alter table gen_widgets add column score INTEGER not null default 0', $sql);
    }

    #[Test]
    public function drop_table_and_drop_index_render_through_the_dialect(): void
    {
        self::assertSame('drop table gen_widgets', $this->actions()->dropTable()->render()[0]->sql);
        self::assertSame('drop index ix_old', $this->actions()->dropIndex('ix_old')->render()[0]->sql);
    }

    #[Test]
    public function rename_table_renders_through_the_dialect(): void
    {
        $sql = $this->actions()->renameTable('old_widgets')->render()[0]->sql;

        self::assertSame('alter table old_widgets rename to gen_widgets', $sql);
    }
}

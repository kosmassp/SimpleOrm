<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Tests\Migrations\Support\PropertyMapFactory;
use SimpleOrm\Tests\Migrations\Support\UserShape;
use SimpleOrm\Tests\Migrations\Support\Widget;

/**
 * Mirrors `dotnet/tests/SimpleOrm.Tests/SchemaSnapshotTests.cs`. `EntityMap`s
 * are hand-built (§7.1's loader is a parallel, not-yet-landed area) — see
 * `Support\PropertyMapFactory`.
 */
final class SchemaSnapshotTest extends TestCase
{
    private function widgetMap(): EntityMap
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
            indexes: [
                new EntityIndex('ix_gen_widgets_name', [new IndexColumn('name', 'name', false)], false),
            ],
            relationships: [],
        );
    }

    /** Mirrors `conformance/snapshot-cases/users_v7.json`'s fixture shape. */
    private function userMap(): EntityMap
    {
        return new EntityMap(
            entityType: UserShape::class,
            kind: RelationKind::Table,
            relationName: 'users',
            schema: null,
            definingSql: null,
            statementParameters: [],
            properties: [
                PropertyMapFactory::make(UserShape::class, 'id', 'id', ColumnType::Int64, key: true, generated: true),
                PropertyMapFactory::make(UserShape::class, 'name', 'name', ColumnType::String),
                PropertyMapFactory::make(UserShape::class, 'email', 'email', ColumnType::String),
                PropertyMapFactory::make(UserShape::class, 'displayName', 'display_name', ColumnType::String, nullable: true),
                PropertyMapFactory::make(UserShape::class, 'createdAtUtc', 'created_at', ColumnType::DateTime),
                PropertyMapFactory::make(UserShape::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, nullable: true),
            ],
            keyStrategy: KeyStrategy::DatabaseGenerated,
            indexes: [
                new EntityIndex('ix_users_email', [new IndexColumn('email', 'email', false)], true),
                new EntityIndex('ix_users_display_name', [new IndexColumn('displayName', 'display_name', false)], false),
            ],
            relationships: [],
        );
    }

    #[Test]
    public function snapshot_carries_version_time_columns_and_indexes(): void
    {
        $schema = SchemaSnapshot::fromMap($this->userMap(), new SqliteDialect());
        $json = SchemaSnapshot::export($schema, 2, new DateTimeImmutable('2026-08-29T12:00:00Z'));

        self::assertStringContainsString('"object": "users"', $json);
        self::assertStringContainsString('"asOfVersion": 2', $json);
        self::assertStringContainsString('"generatedAt": "2026-08-29T12:00:00.0000000Z"', $json);
        self::assertStringContainsString('"column": "display_name"', $json);
        self::assertStringContainsString('"type": "TEXT"', $json);
        self::assertStringContainsString('"name": "ix_users_email"', $json);
    }

    #[Test]
    public function export_matches_the_pinned_users_v7_document_structurally(): void
    {
        $schema = SchemaSnapshot::fromMap($this->userMap(), new SqliteDialect());
        $json = SchemaSnapshot::export($schema, 7, new DateTimeImmutable('2026-08-29T12:00:00Z'));

        $expectedPath = dirname(__DIR__, 3) . '/conformance/snapshot-cases/users_v7.json';
        $expected = json_decode(file_get_contents($expectedPath), true, flags: JSON_THROW_ON_ERROR)['expect'];
        $actual = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        self::assertSame($expected, $actual);
    }

    #[Test]
    public function snapshot_round_trips_through_parse(): void
    {
        $map = $this->userMap();
        $dialect = new SqliteDialect();
        $schema = SchemaSnapshot::fromMap($map, $dialect);
        $json = SchemaSnapshot::export($schema, 7, new DateTimeImmutable());

        $parsed = SchemaSnapshot::parse($json);
        self::assertSame(7, $parsed->asOfVersion);
        self::assertSame('users', $parsed->schema->name);
        self::assertCount(count($map->properties), $parsed->schema->columns);

        $id = self::findColumn($parsed->schema->columns, 'id');
        self::assertTrue($id->key);
        self::assertTrue($id->generated);
        self::assertSame('INTEGER', $id->storageType);

        $names = array_map(static fn ($c) => $c->name, $parsed->schema->columns);
        $sorted = $names;
        sort($sorted, SORT_STRING);
        self::assertSame($sorted, $names);
    }

    #[Test]
    public function ddl_snapshot_round_trips_and_normalizes(): void
    {
        $json = SchemaSnapshot::exportDdl(
            'totals',
            'view',
            "create view if not exists totals as\n  select 1 as one",
            6,
            new DateTimeImmutable(),
        );

        $parsed = SchemaSnapshot::parseDdl($json);
        self::assertSame('totals', $parsed->object);
        self::assertSame('view', $parsed->kind);
        self::assertSame(6, $parsed->asOfVersion);
        self::assertSame('create view totals as select 1 as one', $parsed->ddl);
    }

    #[Test]
    public function ddl_normalization_canonicalizes_the_prefix_the_database_rewrites(): void
    {
        $rendered = SchemaSnapshot::normalizeDdl("create view if not exists totals as\n    select 1 as one");
        $introspected = SchemaSnapshot::normalizeDdl('CREATE VIEW totals as select 1 as one');
        self::assertSame($rendered, $introspected);

        self::assertNotSame(
            SchemaSnapshot::normalizeDdl('create view totals as select 1 as one'),
            SchemaSnapshot::normalizeDdl('create view totals as select 2 as one'),
        );
    }

    /** @param list<\SimpleOrm\Migrations\TableSchemaColumn> $columns */
    private static function findColumn(array $columns, string $name): \SimpleOrm\Migrations\TableSchemaColumn
    {
        foreach ($columns as $column) {
            if ($column->name === $name) {
                return $column;
            }
        }

        self::fail("column {$name} not found");
    }
}

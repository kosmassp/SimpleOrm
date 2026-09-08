<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use DateTimeImmutable;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Json\CanonicalWriter;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Types\Iso8601;

/**
 * The per-table schema snapshot (ADR-0013 add.3, format v2 per ADR-0017): a
 * generated artifact (`Migrations/Table/<Object>/V000N.schema.json`) recording
 * the table's shape as of a version, stamped with generation time. Storage
 * types make snapshots directly DDL-usable — derived downs, trusted baseline
 * rebuilds — and comparable with database introspection. Tables only — views,
 * materialized views, and procedures self-reflect by DDL (`exportDdl()`).
 *
 * Every document is written through `Json\CanonicalWriter` (never
 * `json_encode`) so the byte layout matches the C# `System.Text.Json` reference
 * exactly — the conformance suite compares these documents structurally, but
 * a byte-identical writer is what makes that possible across languages.
 */
final class SchemaSnapshot
{
    private function __construct()
    {
    }

    /** The current model rendered as a schema (the metadata-side producer). */
    public static function fromMap(EntityMap $map, Dialect $dialect): TableSchema
    {
        $columns = array_map(
            static fn (PropertyMap $property): TableSchemaColumn => new TableSchemaColumn(
                $property->columnName,
                $dialect->storageType($property),
                $property->nullable,
                $property->key,
                $property->generated,
            ),
            $map->properties,
        );

        $indexes = array_map(
            static fn (EntityIndex $index): TableSchemaIndex => new TableSchemaIndex(
                $index->name,
                array_map(
                    static fn (IndexColumn $column): TableSchemaIndexPart => new TableSchemaIndexPart(
                        $column->columnName,
                        $column->descending,
                    ),
                    $index->columns,
                ),
                $index->unique,
            ),
            $map->indexes,
        );

        /** @var string $relationName table-kind maps always carry a relation name */
        $relationName = $map->relationName;

        return new TableSchema($relationName, $columns, $indexes);
    }

    public static function export(TableSchema $schema, int $asOfVersion, DateTimeImmutable $generatedAt): string
    {
        $columns = $schema->columns;
        usort($columns, static fn (TableSchemaColumn $a, TableSchemaColumn $b): int => strcmp($a->name, $b->name));
        $indexes = $schema->indexes;
        usort($indexes, static fn (TableSchemaIndex $a, TableSchemaIndex $b): int => strcmp($a->name, $b->name));

        return CanonicalWriter::write([
            'object' => $schema->name,
            'asOfVersion' => $asOfVersion,
            'generatedAt' => self::formatGeneratedAt($generatedAt),
            'columns' => array_map(self::columnDocument(...), $columns),
            'indexes' => array_map(self::indexDocument(...), $indexes),
        ]);
    }

    /**
     * The DDL-shaped snapshot for view-, materialized-view-, and
     * procedure-backed objects: those self-reflect from their defining SQL, so
     * their history is compared by DDL, not by columns. The DDL is
     * whitespace-normalized — layout changes in the source SQL are not schema
     * changes — and stays executable.
     */
    public static function exportDdl(string $objectName, string $kind, string $ddl, int $asOfVersion, DateTimeImmutable $generatedAt): string
    {
        return CanonicalWriter::write([
            'object' => $objectName,
            'kind' => $kind,
            'asOfVersion' => $asOfVersion,
            'generatedAt' => self::formatGeneratedAt($generatedAt),
            'ddl' => self::normalizeDdl($ddl),
        ]);
    }

    public static function parseDdl(string $json): ParsedDdlSnapshot
    {
        /** @var array{object: string, kind: string, ddl: string, asOfVersion: int} $data */
        $data = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        return new ParsedDdlSnapshot($data['object'], $data['kind'], $data['ddl'], (int) $data['asOfVersion']);
    }

    public static function parse(string $json): ParsedTableSnapshot
    {
        /** @var array{object: string, asOfVersion: int, columns: list<array<string, mixed>>, indexes: list<array<string, mixed>>} $data */
        $data = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        $columns = array_map(
            static fn (array $c): TableSchemaColumn => new TableSchemaColumn(
                (string) $c['column'],
                (string) $c['type'],
                (bool) $c['nullable'],
                (bool) ($c['key'] ?? false),
                (bool) ($c['generated'] ?? false),
            ),
            $data['columns'],
        );

        $indexes = array_map(
            static fn (array $i): TableSchemaIndex => new TableSchemaIndex(
                (string) $i['name'],
                array_map(
                    static fn (array $p): TableSchemaIndexPart => new TableSchemaIndexPart(
                        (string) $p['column'],
                        ($p['direction'] ?? 'asc') === 'desc',
                    ),
                    $i['columns'],
                ),
                (bool) ($i['unique'] ?? false),
            ),
            $data['indexes'],
        );

        return new ParsedTableSnapshot(new TableSchema($data['object'], $columns, $indexes), (int) $data['asOfVersion']);
    }

    /**
     * Canonical, comparable, still-executable DDL: whitespace collapses
     * (layout is not schema), and a view create's prefix is canonicalized to
     * lowercase `create [materialized] view <name> as` — databases rewrite
     * that prefix when storing it (SQLite drops `IF NOT EXISTS` and recases
     * `CREATE VIEW`), so the rendered and the introspected form must meet in
     * the middle. The body is compared as written.
     */
    public static function normalizeDdl(string $sql): string
    {
        $words = preg_split('/\s+/', trim($sql), -1, PREG_SPLIT_NO_EMPTY);
        $collapsed = implode(' ', $words === false ? [] : $words);

        $pattern = '/^create\s+(?<mat>materialized\s+)?view\s+(if\s+not\s+exists\s+)?(?<name>\S+)\s+as\s+(?<body>.+)$/i';
        if (preg_match($pattern, $collapsed, $matches) === 1) {
            $materialized = !empty($matches['mat']) ? 'materialized ' : '';

            return "create {$materialized}view {$matches['name']} as {$matches['body']}";
        }

        return $collapsed;
    }

    /** @return array<string, mixed> */
    private static function columnDocument(TableSchemaColumn $column): array
    {
        $document = ['column' => $column->name, 'type' => $column->storageType, 'nullable' => $column->nullable];
        if ($column->key) {
            $document['key'] = true;
        }

        if ($column->generated) {
            $document['generated'] = true;
        }

        return $document;
    }

    /** @return array<string, mixed> */
    private static function indexDocument(TableSchemaIndex $index): array
    {
        $document = [
            'name' => $index->name,
            'columns' => array_map(
                static fn (TableSchemaIndexPart $part): array => [
                    'column' => $part->columnName,
                    'direction' => $part->descending ? 'desc' : 'asc',
                ],
                $index->columns,
            ),
        ];
        if ($index->unique) {
            $document['unique'] = true;
        }

        return $document;
    }

    private static function formatGeneratedAt(DateTimeImmutable $generatedAt): string
    {
        return Iso8601::format($generatedAt);
    }
}

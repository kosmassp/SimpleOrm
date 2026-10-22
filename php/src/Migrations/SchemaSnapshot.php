<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use DateTimeImmutable;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Json\CanonicalWriter;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityTrigger;
use SimpleOrm\Metadata\IndexColumn;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Types\Iso8601;

/**
 * The per-table schema snapshot (ADR-0013 add.3, format v2 per ADR-0017, v3 per ADR-0043): a
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
 *
 * **Format v3 (ADR-0043):** a snapshot written for any dialect but SQLite carries `"dialect": "<name>"` right after
 * `"object"`, whatever its kind; an unstamped document is SQLite's, so every v2 file and every SQLite tree stays
 * byte-identical. View snapshots are dialect-neutral in content ({@see self::normalizeDdl()}); only the stamp differs.
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
                $dialect->storageType($map, $property),
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

    /**
     * @param string $dialect the dialect name the snapshot is written for (format v3, ADR-0043): every dialect but
     *        `sqlite` is stamped as `"dialect"` right after `"object"`; an unstamped document is SQLite's
     */
    public static function export(
        TableSchema $schema,
        int $asOfVersion,
        DateTimeImmutable $generatedAt,
        string $dialect = Dialects::SQLITE,
    ): string {
        $columns = $schema->columns;
        usort($columns, static fn (TableSchemaColumn $a, TableSchemaColumn $b): int => strcmp($a->name, $b->name));
        $indexes = $schema->indexes;
        usort($indexes, static fn (TableSchemaIndex $a, TableSchemaIndex $b): int => strcmp($a->name, $b->name));

        return CanonicalWriter::write([
            ...self::header($schema->name, $dialect),
            'asOfVersion' => $asOfVersion,
            'generatedAt' => self::formatGeneratedAt($generatedAt),
            'columns' => array_map(self::columnDocument(...), $columns),
            'indexes' => array_map(self::indexDocument(...), $indexes),
        ]);
    }

    /**
     * The first keys of every snapshot document: `"object"`, then — for any dialect but SQLite — `"dialect"` (format v3,
     * ADR-0043).
     *
     * @return array<string, string>
     */
    private static function header(string $object, string $dialect): array
    {
        $header = ['object' => $object];
        if ($dialect !== Dialects::SQLITE) {
            $header['dialect'] = $dialect;
        }

        return $header;
    }

    /**
     * `MIG-014` (ADR-0043): a snapshot stamped for another dialect than the one in use is refused by every reader — the
     * derived rollback, the diff baseline, the shadow's trusted baseline. A tree and its snapshots belong to one dialect.
     */
    public static function assertDialect(string $stamp, Dialect $dialect, string $object, int $asOfVersion): void
    {
        if (strcasecmp($stamp, $dialect->name()) === 0) {
            return;
        }

        throw new SimpleOrmException(
            'MIG-014',
            sprintf('V%04d %s', $asOfVersion, $object),
            "the snapshot is stamped for dialect '{$stamp}' but this run uses '{$dialect->name()}'; a tree and its snapshots belong to one dialect — "
                . "rerun snapshot/shadow with --dialect {$dialect->name()}, or point at the right tree",
        );
    }

    /**
     * The DDL-shaped snapshot for view-, materialized-view-, and
     * procedure-backed objects: those self-reflect from their defining SQL, so
     * their history is compared by DDL, not by columns. The DDL is
     * whitespace-normalized — layout changes in the source SQL are not schema
     * changes — and stays executable.
     */
    public static function exportDdl(
        string $objectName,
        string $kind,
        string $ddl,
        int $asOfVersion,
        DateTimeImmutable $generatedAt,
        string $dialect = Dialects::SQLITE,
    ): string {
        return CanonicalWriter::write([
            ...self::header($objectName, $dialect),
            'kind' => $kind,
            'asOfVersion' => $asOfVersion,
            'generatedAt' => self::formatGeneratedAt($generatedAt),
            'ddl' => self::normalizeDdl($ddl),
        ]);
    }

    /** A linked trigger as the snapshot and diff see it: its declaration on the entity's relation (ADR-0042). */
    public static function fromTrigger(EntityMap $map, EntityTrigger $trigger): TriggerSchema
    {
        /** @var string $relationName trigger-bearing maps (table, view) always carry a relation name */
        $relationName = $map->relationName;

        return new TriggerSchema($relationName, $map->kind === RelationKind::View, $trigger->definition);
    }

    /**
     * The trigger snapshot (ADR-0042, spec/migrations.md "Snapshots"): the neutral declaration — relation and its kind,
     * timing, events in the fixed order, `updateOf` only when declared, the body whitespace-collapsed with its tokens
     * unexpanded — so one snapshot serves every dialect. From metadata it is the linked declaration; from history
     * (`shadow`) the stored trigger parsed back; the two agree byte for byte apart from `generatedAt`.
     */
    public static function exportTrigger(
        TriggerSchema $schema,
        int $asOfVersion,
        DateTimeImmutable $generatedAt,
        string $dialect = Dialects::SQLITE,
    ): string {
        $definition = $schema->trigger;
        $document = [
            ...self::header($definition->name, $dialect),
            'kind' => 'trigger',
            'relation' => $schema->relation,
            'relationKind' => $schema->relationKind(),
            'asOfVersion' => $asOfVersion,
            'generatedAt' => self::formatGeneratedAt($generatedAt),
            'timing' => $definition->timing->value,
            'events' => array_map(static fn (TriggerEvent $event): string => $event->value, $definition->events),
        ];
        if ($definition->updateOf !== []) {
            $document['updateOf'] = $definition->updateOf;
        }

        $document['sql'] = $definition->normalizedSql();

        return CanonicalWriter::write($document);
    }

    public static function parseTrigger(string $json): ParsedTriggerSnapshot
    {
        /** @var array{object: string, dialect?: string, relation: string, relationKind?: string, asOfVersion: int|string, timing: string, events: list<string>, updateOf?: list<string>, sql: string} $data */
        $data = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        return new ParsedTriggerSnapshot(
            new TriggerSchema(
                $data['relation'],
                ($data['relationKind'] ?? 'table') === 'view',
                new TriggerDefinition(
                    $data['object'],
                    TriggerTiming::from($data['timing']),
                    TriggerEvent::fromTokens($data['events']),
                    $data['sql'],
                    $data['updateOf'] ?? [],
                ),
            ),
            (int) $data['asOfVersion'],
            $data['dialect'] ?? Dialects::SQLITE,
        );
    }

    /**
     * A stored or rendered trigger statement made comparable (the `MIG-012` trigger guard, ADR-0042): whitespace
     * collapsed and the leading `create [or replace] trigger|function` keywords lowercased — databases recase them
     * when storing the text, the rest is compared as written.
     */
    public static function normalizeTriggerDdl(string $sql): string
    {
        $words = preg_split('/\s+/', trim($sql), -1, PREG_SPLIT_NO_EMPTY);
        $collapsed = implode(' ', $words === false ? [] : $words);

        return (string) preg_replace_callback(
            '/^create(\s+or\s+replace)?\s+(trigger|function)\b/i',
            static fn (array $matches): string => strtolower($matches[0]),
            $collapsed,
        );
    }

    public static function parseDdl(string $json): ParsedDdlSnapshot
    {
        /** @var array{object: string, dialect?: string, kind: string, ddl: string, asOfVersion: int} $data */
        $data = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        return new ParsedDdlSnapshot($data['object'], $data['kind'], $data['ddl'], (int) $data['asOfVersion'], $data['dialect'] ?? Dialects::SQLITE);
    }

    public static function parse(string $json): ParsedTableSnapshot
    {
        /** @var array{object: string, dialect?: string, asOfVersion: int, columns: list<array<string, mixed>>, indexes: list<array<string, mixed>>} $data */
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

        return new ParsedTableSnapshot(new TableSchema($data['object'], $columns, $indexes), (int) $data['asOfVersion'], $data['dialect'] ?? Dialects::SQLITE);
    }

    /**
     * Canonical, comparable, still-executable DDL: whitespace collapses
     * (layout is not schema), and a view create's prefix is canonicalized to
     * lowercase `create [materialized] view <name> as` (ADR-0043) — databases
     * rewrite that prefix when storing it (SQLite drops `IF NOT EXISTS` and
     * recases `CREATE VIEW`) and each dialect spells it its own way
     * (`or alter`, `or replace`, `if not exists`, the name's quoting), so the
     * rendered, the introspected, and the recorded form must meet in the
     * middle: a view snapshot's content is dialect-neutral. The body is
     * compared as written.
     */
    public static function normalizeDdl(string $sql): string
    {
        $words = preg_split('/\s+/', trim($sql), -1, PREG_SPLIT_NO_EMPTY);
        $collapsed = implode(' ', $words === false ? [] : $words);

        $pattern = '/^create\s+(?:or\s+(?:alter|replace)\s+)?(?<mat>materialized\s+)?view\s+(?:if\s+not\s+exists\s+)?'
            . '(?<name>\S+)\s+as\s+(?<body>.+)$/i';
        if (preg_match($pattern, $collapsed, $matches) === 1) {
            $materialized = !empty($matches['mat']) ? 'materialized ' : '';
            $name = str_replace(['"', '`', '[', ']'], '', $matches['name']);

            return "create {$materialized}view {$name} as {$matches['body']}";
        }

        return $collapsed;
    }

    /**
     * The parts of a normalized view DDL (ADR-0043) — the neutral snapshot form `create [materialized] view <name> as
     * <body>` — so a dialect can render it again with {@see Dialect::createViewSqlFromBody()}. Null when the text is not
     * a view create (a procedure's DDL).
     *
     * @return array{name: string, body: string, materialized: bool}|null
     */
    public static function splitViewDdl(string $normalizedDdl): ?array
    {
        if (preg_match('/^create (?<mat>materialized )?view (?<name>\S+) as (?<body>.+)$/is', $normalizedDdl, $matches) !== 1) {
            return null;
        }

        return ['name' => $matches['name'], 'body' => $matches['body'], 'materialized' => $matches['mat'] !== ''];
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

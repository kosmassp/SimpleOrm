<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use SimpleOrm\Json\CanonicalWriter;

/**
 * Exports an {@see EntityMap} as the conformance JSON defined in
 * spec/metadata-model.md, via {@see CanonicalWriter} (never `json_encode` —
 * CODING-STANDARD §8). The export is deliberately column-centric and
 * language-neutral: column names, neutral type tokens, and SQL-side parameter
 * names — never PHP property names, which legitimately differ per
 * implementation. Every port must produce byte-identical output for the same
 * entity; ported from the C# reference's `EntityMapJson`.
 */
final class EntityMapJson
{
    private function __construct()
    {
    }

    public static function export(EntityMap $map): string
    {
        $document = [
            'entity' => $map->entityName(),
            'source' => self::source($map),
            'key' => [
                'strategy' => $map->keyStrategy->value,
                'columns' => array_map(static fn (PropertyMap $p): string => $p->columnName, $map->keyProperties),
            ],
        ];

        if ($map->versionProperty !== null) {
            $document['version'] = $map->versionProperty->columnName;
        }

        $document['columns'] = self::columns($map);

        if ($map->indexes !== []) {
            $document['indexes'] = self::indexes($map);
        }

        if ($map->relationships !== []) {
            $document['relationships'] = self::relationships($map);
        }

        return CanonicalWriter::write($document);
    }

    /** @return array<string, mixed> */
    private static function source(EntityMap $map): array
    {
        $source = ['kind' => $map->kind->value];

        if ($map->kind === RelationKind::Statement) {
            $source['sql'] = self::normalizeSql((string) $map->definingSql);
            $source['parameters'] = self::parameters($map->statementParameters);

            return $source;
        }

        $source['name'] = $map->relationName;
        if ($map->schema !== null) {
            $source['schema'] = $map->schema;
        }

        if ($map->definingSql !== null) {
            $source['sql'] = self::normalizeSql($map->definingSql);
        }

        if ($map->kind === RelationKind::Procedure) {
            $source['parameters'] = self::parameters($map->statementParameters);
        }

        return $source;
    }

    /**
     * @param list<StatementParameter> $parameters
     * @return list<array{name: string, type: string}>
     */
    private static function parameters(array $parameters): array
    {
        return array_map(
            static fn (StatementParameter $p): array => ['name' => $p->name, 'type' => $p->type->value],
            $parameters,
        );
    }

    /** @return list<array<string, mixed>> */
    private static function columns(EntityMap $map): array
    {
        $columns = [];
        foreach ($map->properties as $property) {
            $entry = [
                'column' => $property->columnName,
                'type' => self::typeToken($property),
                'nullable' => $property->nullable,
            ];
            if ($property->key) {
                $entry['key'] = true;
            }

            if ($property->generated) {
                $entry['generated'] = true;
            }

            $columns[] = $entry;
        }

        return $columns;
    }

    /**
     * Neutral type tokens (spec/metadata-model.md). Unlike the C# reference,
     * which derives the token from the CLR type at export time, PHP's loaders
     * resolve `ColumnType` once at load time (CODING-STANDARD §10), so export
     * only reads it — except `Custom`, which carries the PHP type name instead
     * of the spec's `clr:<full name>` (CODING-STANDARD §10).
     */
    private static function typeToken(PropertyMap $property): string
    {
        if ($property->type !== ColumnType::Custom) {
            return $property->type->value;
        }

        return 'php:' . ($property->phpType ?? 'mixed');
    }

    /** @return list<array<string, mixed>> */
    private static function indexes(EntityMap $map): array
    {
        $indexes = [];
        foreach ($map->indexes as $index) {
            $entry = [
                'name' => $index->name,
                'columns' => array_map(
                    static fn (IndexColumn $c): array => ['column' => $c->columnName, 'direction' => $c->descending ? 'desc' : 'asc'],
                    $index->columns,
                ),
            ];
            if ($index->unique) {
                $entry['unique'] = true;
            }

            $indexes[] = $entry;
        }

        return $indexes;
    }

    /** @return list<array<string, mixed>> */
    private static function relationships(EntityMap $map): array
    {
        return array_map(static function (RelationshipMap $relationship) use ($map): array {
            /** @var array<string, mixed> */
            return match ($relationship->kind) {
                RelationshipKind::ManyToOne => [
                    'kind' => 'many_to_one',
                    'foreignKeyColumns' => array_map(
                        static function (string $n) use ($map): string {
                            $property = $map->property($n);
                            assert($property !== null, "many_to_one FK property '{$n}' must be mapped");

                            return $property->columnName;
                        },
                        $relationship->foreignKeyProperties,
                    ),
                    'references' => self::shortName($relationship->targetType),
                ],
                RelationshipKind::OneToOne, RelationshipKind::OneToMany => [
                    'kind' => $relationship->kind === RelationshipKind::OneToOne ? 'one_to_one' : 'one_to_many',
                    'references' => self::shortName($relationship->targetType),
                    'targetForeignKeyProperties' => array_map(self::pascalCase(...), $relationship->foreignKeyProperties),
                ],
                RelationshipKind::ManyToMany => [
                    'kind' => 'many_to_many',
                    'references' => self::shortName($relationship->targetType),
                    'through' => self::shortName((string) $relationship->linkType),
                    'linkForeignKeysToOwner' => array_map(self::pascalCase(...), $relationship->linkForeignKeysToOwner),
                    'linkForeignKeysToTarget' => array_map(self::pascalCase(...), $relationship->linkForeignKeysToTarget),
                ],
            };
        }, $map->relationships);
    }

    /**
     * PascalCases a PHP property name for the cross-language `targetForeignKeyProperties` /
     * `linkForeignKeys*` export fields (CODING-STANDARD §10: an interim rule — the spec
     * should switch these fields to columns, ADR-0026 — that keeps the export byte-identical
     * to the C# reference's own property names).
     */
    private static function pascalCase(string $propertyName): string
    {
        return ucfirst($propertyName);
    }

    /** Collapses whitespace so the exported SQL is layout-independent across implementations. */
    private static function normalizeSql(string $sql): string
    {
        $parts = preg_split('/\s+/u', trim($sql));

        return implode(' ', array_filter($parts !== false ? $parts : [], static fn (string $s): bool => $s !== ''));
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}

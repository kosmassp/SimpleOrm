<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use SimpleOrm\Errors\MappingError;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Naming\NamingConvention;

/**
 * Shared final stage of every loader path (§7.2: attribute, convention, manual
 * builder): resolves column names through the naming convention, runs the
 * source-independent validations (duplicate columns, key shape, version shape,
 * relationship FK resolution, index column resolution), and produces the
 * {@see EntityMap}. Violations accumulate in `$errors`; the caller throws one
 * `MappingException` with all of them (never first-error-only).
 */
final class MapAssembler
{
    private function __construct()
    {
    }

    /**
     * @param class-string $entityType
     * @param list<StatementParameter> $statementParameters
     * @param list<MappedPropertySpec> $specs
     * @param list<IndexSpec> $indexSpecs
     * @param list<RelationshipSpec> $relationshipSpecs
     * @param list<MappingError> $errors
     */
    public static function assemble(
        string $entityType,
        RelationKind $kind,
        ?string $relationName,
        ?string $schema,
        ?string $statementSql,
        array $statementParameters,
        array $specs,
        array $indexSpecs,
        array $relationshipSpecs,
        NamingConvention $convention,
        array &$errors,
    ): ?EntityMap {
        $shortName = self::shortName($entityType);
        $properties = [];
        /** @var array<string, PropertyMap> $byProperty */
        $byProperty = [];
        /** @var array<string, string> $seenColumns column => property that claimed it */
        $seenColumns = [];

        /** @var array<int, OwnedMap> $ownedMaps keyed by spl_object_id of the OwnedSpec */
        $ownedMaps = [];

        foreach ($specs as $spec) {
            // An owned member (ADR-0030) flattens into the owner: its column
            // carries the navigation's prefix, its name is the dotted path, and
            // a nullable navigation makes every member column nullable.
            $owner = null;
            if ($spec->owner !== null) {
                $owner = $ownedMaps[spl_object_id($spec->owner)] ??= new OwnedMap(
                    $spec->owner->property,
                    $spec->owner->ownedType,
                    $spec->owner->explicitPrefix ?? $convention->toDatabase($spec->owner->property->getName()) . '_',
                    $spec->owner->nullable,
                );
            }

            $column = ($owner?->prefix ?? '') . ($spec->explicitColumn ?? $convention->toDatabase($spec->property->getName()));
            $propertyName = $owner === null
                ? $spec->property->getName()
                : $owner->propertyName() . '.' . $spec->property->getName();
            if (isset($seenColumns[$column])) {
                $errors[] = new MappingError(
                    'MAP-018',
                    "{$shortName}.{$propertyName}",
                    "maps to column '{$column}' already used by '{$seenColumns[$column]}'",
                );
            } else {
                $seenColumns[$column] = $propertyName;
            }

            $property = new PropertyMap(
                $spec->property,
                $column,
                $spec->type,
                $spec->phpType,
                ($owner?->nullable ?? false) || $spec->nullable,
                key: $spec->isKey,
                generated: $spec->isGenerated,
                version: $spec->isVersion,
                foreignKeyReferences: $spec->foreignKeyReferences,
                owner: $owner,
            );
            $owner?->addMember($property);
            $properties[] = $property;
            $byProperty[$propertyName] = $property;
        }

        self::validateVersion($shortName, $properties, $errors);
        $keyStrategy = self::resolveKeyStrategy($shortName, $kind, $properties, $errors);
        $indexes = self::resolveIndexes($shortName, $relationName, $indexSpecs, $byProperty, $errors);
        $relationships = self::resolveRelationships($shortName, $relationshipSpecs, $byProperty, $errors);

        if ($errors !== []) {
            return null;
        }

        return new EntityMap(
            $entityType,
            $kind,
            $relationName,
            $schema,
            $statementSql,
            $statementParameters,
            $properties,
            $keyStrategy,
            $indexes,
            $relationships,
        );
    }

    /**
     * @param list<PropertyMap> $properties
     * @param list<MappingError> $errors
     */
    private static function validateVersion(string $shortName, array $properties, array &$errors): void
    {
        $version = null;
        foreach ($properties as $property) {
            if (!$property->version) {
                continue;
            }

            if ($version !== null) {
                $errors[] = new MappingError(
                    'MAP-019',
                    "{$shortName}.{$property->propertyName()}",
                    "a second #[Version] property; '{$version->propertyName()}' already is the version column",
                );
                continue;
            }

            $version = $property;
            if ($property->phpType !== 'int') {
                $errors[] = new MappingError(
                    'MAP-019',
                    "{$shortName}.{$property->propertyName()}",
                    '#[Version] requires int, found ' . ($property->phpType ?? 'untyped'),
                );
            }

            if ($property->key) {
                $errors[] = new MappingError('MAP-019', "{$shortName}.{$property->propertyName()}", '#[Version] cannot be part of the key');
            }
        }
    }

    /**
     * @param list<PropertyMap> $properties
     * @param list<MappingError> $errors
     */
    private static function resolveKeyStrategy(string $shortName, RelationKind $kind, array $properties, array &$errors): KeyStrategy
    {
        $keys = array_values(array_filter($properties, static fn (PropertyMap $p): bool => $p->key));
        if ($keys === []) {
            if ($kind === RelationKind::Table) {
                $errors[] = new MappingError('MAP-019', $shortName, 'a table-backed entity must declare a key');
            }

            return KeyStrategy::None;
        }

        $generatedKeys = array_values(array_filter($keys, static fn (PropertyMap $p): bool => $p->generated));
        if ($generatedKeys !== []) {
            if (count($keys) > 1) {
                $errors[] = new MappingError(
                    'MAP-019',
                    "{$shortName}.{$generatedKeys[0]->propertyName()}",
                    '#[Generated] is not valid on a composite key',
                );
            } elseif (!$keys[0]->type->isInteger()) {
                $errors[] = new MappingError(
                    'MAP-019',
                    "{$shortName}.{$keys[0]->propertyName()}",
                    "a database-generated key must be an integer type; '{$keys[0]->type->value}' is not "
                        . '(client-generated GUIDs drop #[Generated])',
                );
            }

            return KeyStrategy::DatabaseGenerated;
        }

        if (count($keys) === 1 && $keys[0]->type === ColumnType::Guid) {
            return KeyStrategy::ClientGuid;
        }

        return KeyStrategy::Natural;
    }

    /**
     * @param list<IndexSpec> $indexSpecs
     * @param array<string, PropertyMap> $byProperty
     * @param list<MappingError> $errors
     * @return list<EntityIndex>
     */
    private static function resolveIndexes(
        string $shortName,
        ?string $relationName,
        array $indexSpecs,
        array $byProperty,
        array &$errors,
    ): array {
        $indexes = [];
        foreach ($indexSpecs as $spec) {
            $columns = [];
            $valid = true;
            foreach ($spec->columns as [$propertyName, $descending]) {
                if (!isset($byProperty[$propertyName])) {
                    $errors[] = new MappingError('MAP-015', "{$shortName} [Index]", "'{$propertyName}' is not a mapped property");
                    $valid = false;
                    continue;
                }

                $columns[] = new IndexColumn($propertyName, $byProperty[$propertyName]->columnName, $descending);
            }

            if (!$valid) {
                continue;
            }

            $name = $spec->name ?? self::defaultIndexName(
                $relationName ?? '',
                array_map(static fn (IndexColumn $c): string => $c->columnName, $columns),
            );
            $indexes[] = new EntityIndex($name, $columns, $spec->unique);
        }

        return $indexes;
    }

    /** @param list<string> $columnNames */
    private static function defaultIndexName(string $tableName, array $columnNames): string
    {
        return 'ix_' . $tableName . '_' . implode('_', $columnNames);
    }

    /**
     * @param list<RelationshipSpec> $relationshipSpecs
     * @param array<string, PropertyMap> $byProperty
     * @param list<MappingError> $errors
     * @return list<RelationshipMap>
     */
    private static function resolveRelationships(string $shortName, array $relationshipSpecs, array $byProperty, array &$errors): array
    {
        $relationships = [];
        $ownerKeyCount = count(array_filter($byProperty, static fn (PropertyMap $p): bool => $p->key));

        foreach ($relationshipSpecs as $spec) {
            $target = "{$shortName}.{$spec->propertyName}";

            switch ($spec->kind) {
                case RelationshipKind::ManyToOne:
                    $unmapped = array_values(array_filter(
                        $spec->foreignKeyProperties,
                        static fn (string $n): bool => !isset($byProperty[$n]),
                    ));
                    if ($unmapped !== []) {
                        $errors[] = new MappingError(
                            'MAP-016',
                            $target,
                            '#[ManyToOne] names foreign-key propert' . (count($unmapped) === 1 ? 'y' : 'ies') . " '"
                                . implode("', '", $unmapped) . "' that are not mapped properties",
                        );
                        continue 2;
                    }

                    $targetArity = self::keyArity($spec->targetType);
                    if ($targetArity !== null && $targetArity !== count($spec->foreignKeyProperties)) {
                        $errors[] = new MappingError(
                            'MAP-016',
                            $target,
                            '#[ManyToOne] declares ' . count($spec->foreignKeyProperties) . ' foreign-key propert'
                                . (count($spec->foreignKeyProperties) === 1 ? 'y' : 'ies') . " but '"
                                . self::shortName($spec->targetType) . "' has a {$targetArity}-part key",
                        );
                        continue 2;
                    }

                    break;

                case RelationshipKind::OneToMany:
                case RelationshipKind::OneToOne:
                    if ($ownerKeyCount > 0 && count($spec->foreignKeyProperties) !== $ownerKeyCount) {
                        $errors[] = new MappingError(
                            'MAP-021',
                            $target,
                            'declares ' . count($spec->foreignKeyProperties) . ' target foreign-key propert'
                                . (count($spec->foreignKeyProperties) === 1 ? 'y' : 'ies')
                                . " but this entity has a {$ownerKeyCount}-part key",
                        );
                        continue 2;
                    }

                    break;

                default:
                    if ($ownerKeyCount > 0 && count($spec->linkForeignKeysToOwner) !== $ownerKeyCount) {
                        $errors[] = new MappingError(
                            'MAP-022',
                            $target,
                            "link '" . self::shortName((string) $spec->linkType) . "' declares "
                                . count($spec->linkForeignKeysToOwner) . ' #[ForeignKey] propert'
                                . (count($spec->linkForeignKeysToOwner) === 1 ? 'y' : 'ies')
                                . " referencing this type, whose key has {$ownerKeyCount} part(s)",
                        );
                        continue 2;
                    }

                    $elementArity = self::keyArity($spec->targetType);
                    if ($elementArity !== null && count($spec->linkForeignKeysToTarget) !== $elementArity) {
                        $errors[] = new MappingError(
                            'MAP-022',
                            $target,
                            "link '" . self::shortName((string) $spec->linkType) . "' declares "
                                . count($spec->linkForeignKeysToTarget) . ' #[ForeignKey] propert'
                                . (count($spec->linkForeignKeysToTarget) === 1 ? 'y' : 'ies') . " referencing '"
                                . self::shortName($spec->targetType) . "', whose key has {$elementArity} part(s)",
                        );
                        continue 2;
                    }

                    break;
            }

            $relationships[] = new RelationshipMap(
                $spec->propertyName,
                $spec->kind,
                $spec->targetType,
                $spec->foreignKeyProperties,
                $spec->linkType,
                $spec->linkForeignKeysToOwner,
                $spec->linkForeignKeysToTarget,
            );
        }

        return $relationships;
    }

    /**
     * The related type's key arity by its `#[Key]` declarations — null when it
     * declares none (convention-mapped or unknown: the check is skipped rather
     * than guessed).
     *
     * @param class-string $entityType
     */
    private static function keyArity(string $entityType): ?int
    {
        $class = new ReflectionClass($entityType);
        $count = 0;
        foreach (PropertyDiscovery::propertiesInDeclarationOrder($class) as $property) {
            if ($property->getAttributes(Key::class) !== []) {
                $count++;
            }
        }

        return $count > 0 ? $count : null;
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}

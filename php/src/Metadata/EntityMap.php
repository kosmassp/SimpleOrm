<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use SimpleOrm\Errors\SimpleOrmException;

/**
 * The single source of truth about a mapped type (§7.1, spec/metadata-model.md).
 * Produced only by the loaders; every other subsystem — mapping, generated CRUD,
 * validation, migrations, the criteria core — reads this and never the
 * attributes. Immutable; `$keyProperties` and `$versionProperty` are derived
 * once at construction.
 */
final readonly class EntityMap
{
    /** @var list<PropertyMap> key properties in declaration order (composite keys are ordered) */
    public array $keyProperties;

    public ?PropertyMap $versionProperty;

    /**
     * @param class-string $entityType
     * @param string|null $relationName table/view/procedure name; null for statement-backed entities
     * @param string|null $definingSql a statement's query or a view's defining SELECT; null for tables and procedures
     * @param list<StatementParameter> $statementParameters
     * @param list<PropertyMap> $properties
     * @param list<EntityIndex> $indexes
     * @param list<RelationshipMap> $relationships
     */
    public function __construct(
        public string $entityType,
        public RelationKind $kind,
        public ?string $relationName,
        public ?string $schema,
        public ?string $definingSql,
        public array $statementParameters,
        public array $properties,
        public KeyStrategy $keyStrategy,
        public array $indexes,
        public array $relationships,
    ) {
        $this->keyProperties = array_values(array_filter($properties, static fn (PropertyMap $p): bool => $p->key));
        $version = array_values(array_filter($properties, static fn (PropertyMap $p): bool => $p->version));
        $this->versionProperty = $version[0] ?? null;
    }

    /** The unqualified class name: the export's `entity` and the target of error messages. */
    public function entityName(): string
    {
        $slash = strrpos($this->entityType, '\\');

        return $slash === false ? $this->entityType : substr($this->entityType, $slash + 1);
    }

    /** The mapped property with the given name, or null (exact match; callers resolve case rules). */
    public function property(string $propertyName): ?PropertyMap
    {
        foreach ($this->properties as $property) {
            if ($property->propertyName() === $propertyName) {
                return $property;
            }
        }

        return null;
    }

    /**
     * Entity identity (§7.4): the key values of an instance, in key order.
     *
     * @return list<mixed>
     */
    public function getKeyValues(object $entity): array
    {
        if ($this->keyProperties === []) {
            throw new SimpleOrmException(
                'CRUD-002',
                $this->entityName(),
                'the entity defines no key; identity is undefined',
            );
        }

        return array_map(static fn (PropertyMap $key): mixed => $key->getValue($entity), $this->keyProperties);
    }

    /** True when two instances have equal key values, position by position (§7.4). */
    public function keysEqual(object $left, object $right): bool
    {
        $leftKeys = $this->getKeyValues($left);
        $rightKeys = $this->getKeyValues($right);
        foreach ($leftKeys as $i => $value) {
            if ($value != $rightKeys[$i] || gettype($value) !== gettype($rightKeys[$i])) {
                return false;
            }
        }

        return true;
    }
}

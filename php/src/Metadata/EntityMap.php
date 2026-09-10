<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;
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

    /** @var list<OwnedMap> the owned value types flattened into this entity (ADR-0030), in declaration order */
    public array $ownedTypes;

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

        $owned = [];
        foreach ($properties as $property) {
            if ($property->owner !== null && !in_array($property->owner, $owned, true)) {
                $owned[] = $property->owner;
            }
        }

        $this->ownedTypes = $owned;
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

    /**
     * True when two instances have equal key values, compared **by value**
     * (§7.4) through {@see KeyTuple} — the one identity implementation
     * relationship loading also uses (CODING-STANDARD §8), so a `Decimal` or
     * temporal key compares the same way here as it does while loading.
     */
    public function keysEqual(object $left, object $right): bool
    {
        return (new KeyTuple($this->getKeyValues($left)))->equals(new KeyTuple($this->getKeyValues($right)));
    }

    /**
     * `Type.Navigation`, the target form loading errors use (`REL-001`
     * through `REL-003`).
     */
    public function navigationTarget(RelationshipMap $relationship): string
    {
        return "{$this->entityName()}.{$relationship->propertyName}";
    }

    /**
     * The navigation's own reflection property — `public private(set)`
     * (`MAP-011`): reflection is the library's only route to write it, exactly
     * like {@see PropertyMap::setValue()}. The one implementation both the
     * explicit/batch loading engine and the join-mode eager loader use
     * (CODING-STANDARD §8) to attach a loaded navigation.
     */
    public function navigationProperty(RelationshipMap $relationship): ReflectionProperty
    {
        return new ReflectionProperty($this->entityType, $relationship->propertyName);
    }

    /**
     * Sorts entities of this type by key, compared value-wise through
     * {@see KeyTuple} (spec/loading.md: "ordered by target key … compared
     * value-wise"). The one ordering implementation every relationship-loading
     * path uses for a collection navigation (CODING-STANDARD §8) — a
     * dialect's own `ORDER BY` is not, by itself, enough: SQLite sorts a
     * TEXT-affinity column (every `Decimal` key, §7.9) lexicographically, so
     * "10" would render before "2".
     *
     * @param list<object> $entities
     * @return list<object>
     */
    public function sortByKey(array $entities): array
    {
        usort($entities, fn (object $a, object $b): int => (new KeyTuple($this->getKeyValues($a)))
            ->compare(new KeyTuple($this->getKeyValues($b))));

        return $entities;
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;

/**
 * An owned value type flattened into its owner's table (ADR-0030): the
 * navigation property, the column prefix its members carry, and the members.
 * An owned type is not an entity — no table, key, version, generated column,
 * relationship, or nested owned type — so every subsystem sees only the
 * flattened {@see PropertyMap}s; this is how the mapper regroups them.
 * Mirrors the C# reference's `OwnedMap`.
 */
final class OwnedMap
{
    /** @var list<PropertyMap> */
    private array $members = [];

    /** @param class-string $ownedType */
    public function __construct(
        public readonly ReflectionProperty $property,
        public readonly string $ownedType,
        public readonly string $prefix,
        public readonly bool $nullable,
    ) {
    }

    public function propertyName(): string
    {
        return $this->property->getName();
    }

    /**
     * The flattened members, in declaration order; the same instances appear in the owner's property list.
     *
     * @return list<PropertyMap>
     */
    public function members(): array
    {
        return $this->members;
    }

    /** @internal assembler use only */
    public function addMember(PropertyMap $member): void
    {
        $this->members[] = $member;
    }
}

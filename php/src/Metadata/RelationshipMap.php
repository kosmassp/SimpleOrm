<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * A declared navigation (ADR-0005, extended by ADR-0019): many-to-one through a
 * foreign key on this class, one-to-many/one-to-one through a foreign key on the
 * target, or many-to-many through an explicit link entity. Metadata only in the
 * PHP port (loading is Level 2); it exists so the EntityMap export is complete.
 */
final readonly class RelationshipMap
{
    /**
     * @param class-string $targetType the related entity (a collection navigation's element type)
     * @param list<string> $foreignKeyProperties many-to-one: the FK properties on this class, in the
     *        target's key order; one-to-many/one-to-one: the FK properties on the target, in this
     *        entity's key order; empty for many-to-many. One entry per key part (ADR-0019 add.1).
     * @param class-string|null $linkType many-to-many only: the link entity
     * @param list<string> $linkForeignKeysToOwner many-to-many only: link properties referencing this class, in declaration order
     * @param list<string> $linkForeignKeysToTarget many-to-many only: link properties referencing the element type
     */
    public function __construct(
        public string $propertyName,
        public RelationshipKind $kind,
        public string $targetType,
        public array $foreignKeyProperties,
        public ?string $linkType = null,
        public array $linkForeignKeysToOwner = [],
        public array $linkForeignKeysToTarget = [],
    ) {
    }
}

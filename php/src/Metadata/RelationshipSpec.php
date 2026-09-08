<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * Loader-internal working shape of one declared navigation before its foreign
 * keys are validated against the related key's arity (mirrors the C#
 * reference's `RelationshipSpec`); {@see MapAssembler} turns a valid one into a
 * {@see RelationshipMap}.
 */
final readonly class RelationshipSpec
{
    /**
     * @param class-string $targetType
     * @param list<string> $foreignKeyProperties
     * @param class-string|null $linkType
     * @param list<string> $linkForeignKeysToOwner
     * @param list<string> $linkForeignKeysToTarget
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

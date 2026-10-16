<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * Loader-internal working shape of one declared navigation before its foreign
 * keys are validated against the related key's arity (ADR-0019; mirrors the C#
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
     * @param list<CascadeOperation> $cascade
     */
    public function __construct(
        public string $propertyName,
        public RelationshipKind $kind,
        public string $targetType,
        public array $foreignKeyProperties,
        public ?string $linkType = null,
        public array $linkForeignKeysToOwner = [],
        public array $linkForeignKeysToTarget = [],
        public array $cascade = [],
    ) {
    }

    /**
     * The same navigation with its declared cascades (`#[Cascade]`, Level 3 M2).
     *
     * @param list<CascadeOperation> $cascade
     */
    public function withCascade(array $cascade): self
    {
        return new self(
            $this->propertyName,
            $this->kind,
            $this->targetType,
            $this->foreignKeyProperties,
            $this->linkType,
            $this->linkForeignKeysToOwner,
            $this->linkForeignKeysToTarget,
            $cascade,
        );
    }
}

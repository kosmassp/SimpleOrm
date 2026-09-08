<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;

/**
 * Loader-internal: an `#[Owned]` navigation whose members flatten into the
 * owner (ADR-0030); the prefix resolves at assembly. Mirrors the C#
 * reference's `OwnedSpec`.
 */
final readonly class OwnedSpec
{
    /** @param class-string $ownedType */
    public function __construct(
        public ReflectionProperty $property,
        public string $ownedType,
        public bool $nullable,
        public ?string $explicitPrefix,
    ) {
    }
}

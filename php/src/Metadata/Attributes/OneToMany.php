<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * A one-to-many collection navigation (ADR-0019): `$of` is the element type (PHP
 * arrays carry none — CODING-STANDARD §10), and the FK properties live on it,
 * one per part of this entity's key, in key order. The property is an `array`
 * defaulting to `[]`, `public private(set)`.
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class OneToMany
{
    /** @var list<string> */
    public array $targetForeignKeyProperties;

    /** @param class-string $of */
    public function __construct(
        public string $of,
        string ...$targetForeignKeyProperties,
    ) {
        $this->targetForeignKeyProperties = array_values($targetForeignKeyProperties);
    }
}

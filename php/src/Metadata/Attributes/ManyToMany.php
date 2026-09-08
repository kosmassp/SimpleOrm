<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * A many-to-many collection navigation (ADR-0019): `$of` is the element type and
 * `$through` the link entity — declared, never inferred; the link's
 * `#[ForeignKey]`s identify which of its properties reference each side
 * (`MAP-022` when a side is missing or its count mismatches that side's key).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class ManyToMany
{
    /**
     * @param class-string $of
     * @param class-string $through
     */
    public function __construct(
        public string $of,
        public string $through,
    ) {
    }
}

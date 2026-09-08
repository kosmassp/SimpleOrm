<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** Declares that this column references another entity's key (ADR-0005); a many-to-many link's `#[ForeignKey]`s identify its sides. */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class ForeignKey
{
    /** @param class-string $references */
    public function __construct(public string $references)
    {
    }
}

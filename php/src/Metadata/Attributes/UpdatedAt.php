<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * Declares the audit column the session stamps with its clock on every write that changes a row
 * (Level 3 M3, spec/hooks.md): a `DateTimeImmutable` property, nullable or not, carrying `#[Column]`;
 * never a key, version, or generated column; at most one per entity (`MAP-026`). The library is its
 * only writer, and it never makes a tracked instance dirty on its own.
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class UpdatedAt
{
}

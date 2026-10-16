<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * Declares the audit column the session fills with its clock on insert when the value is unset
 * (Level 3 M3, spec/hooks.md): a `DateTimeImmutable` property, nullable or not, carrying `#[Column]`;
 * never a key, version, or generated column; at most one per entity (`MAP-026`).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class CreatedAt
{
}

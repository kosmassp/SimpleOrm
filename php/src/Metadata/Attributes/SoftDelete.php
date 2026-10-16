<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * Declares the flag a delete sets instead of removing the row (Level 3 M3, spec/hooks.md): a
 * non-nullable `bool` property carrying `#[Column]`; never a key, version, or generated column; at most
 * one per entity (`MAP-026`). Reads never filter on it — there is no hidden WHERE (§2).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class SoftDelete
{
}

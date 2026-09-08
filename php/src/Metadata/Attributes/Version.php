<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** The optimistic-concurrency version column (§7.16): integer, at most one, tables only (`MAP-013`). */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Version
{
}

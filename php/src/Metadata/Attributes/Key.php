<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** Marks a key property (§7.4); several in declaration order form a composite key. */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Key
{
}

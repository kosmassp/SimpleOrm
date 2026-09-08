<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** The database produces this column's value (§7.14): never written; a generated key is read back on insert. Tables only (`MAP-013`). */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Generated
{
}

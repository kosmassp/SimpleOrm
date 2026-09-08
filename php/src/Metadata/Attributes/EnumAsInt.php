<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** Stores an enum property by ordinal instead of by name (§7.9 opt-in; token `enum_int`). */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class EnumAsInt
{
}

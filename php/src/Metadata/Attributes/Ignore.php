<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/** A public settable property that is deliberately not a column (ADR-0004; otherwise `MAP-010`). */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Ignore
{
}

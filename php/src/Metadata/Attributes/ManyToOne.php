<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * A many-to-one navigation (ADR-0005): the FK properties on this class, one per
 * part of the target's key, in the target's key order (ADR-0019 add.1). The
 * target is the property's declared type. Never a column, never written, not
 * publicly settable (`MAP-011`: declare it `public private(set)`).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class ManyToOne
{
    /** @var list<string> */
    public array $foreignKeyProperties;

    public function __construct(string ...$foreignKeyProperties)
    {
        $this->foreignKeyProperties = array_values($foreignKeyProperties);
    }
}

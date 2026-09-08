<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * A one-to-one navigation (ADR-0019): the FK lives on the target, named by its
 * properties, one per part of this entity's key, in key order (ADR-0019 add.1).
 * The target is the property's declared type. True 1:1 integrity is the
 * database's (a unique index on the target FK).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class OneToOne
{
    /** @var list<string> */
    public array $targetForeignKeyProperties;

    public function __construct(string ...$targetForeignKeyProperties)
    {
        $this->targetForeignKeyProperties = array_values($targetForeignKeyProperties);
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Query\SortOrder;

/**
 * A declared index (ADR-0007, add.3 token stream): property names, each optionally
 * followed by a `SortOrder` — `#[Index(['status', 'createdAtUtc', SortOrder::Desc])]`.
 * A leading or doubled direction, an unknown or unmapped property, or an empty
 * stream is `MAP-015`. Name defaults to `ix_<table>_<column>[_<column>…]`.
 * Tables and materialized views only (`MAP-014`).
 */
#[Attribute(Attribute::TARGET_CLASS | Attribute::IS_REPEATABLE)]
final readonly class Index
{
    /** @param list<string|SortOrder> $columns */
    public function __construct(
        public array $columns,
        public ?string $name = null,
        public bool $unique = false,
    ) {
    }
}

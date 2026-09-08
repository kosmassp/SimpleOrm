<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Metadata\ColumnType;

/**
 * Maps a property to a column (ADR-0004: mapping is opt-in — a property is mapped
 * iff it carries this). A bare `#[Column]` derives the column name through the
 * naming convention; `#[Column('name')]` binds it explicitly. `type` overrides
 * the neutral token the PHP type would give (`int32`, `date`, `guid`, … —
 * CODING-STANDARD §10).
 */
#[Attribute(Attribute::TARGET_PROPERTY)]
final readonly class Column
{
    public function __construct(
        public ?string $name = null,
        public ?ColumnType $type = null,
    ) {
    }
}

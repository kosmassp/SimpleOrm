<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;

/**
 * A materialized-view-backed, read-only entity (ADR-0008 add.): separate from
 * `#[View]` because it may carry `#[Index]`. Dormant on SQLite (`DDL-002` on
 * create) until a dialect with materialized views.
 */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class MaterializedView
{
    public function __construct(
        public string $name,
        public string $sql,
        public ?string $schema = null,
    ) {
    }
}

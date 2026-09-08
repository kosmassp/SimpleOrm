<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** A declared index (ADR-0007): tables and materialized views only; consumed by generated DDL and the diff generator. */
final readonly class EntityIndex
{
    /** @param list<IndexColumn> $columns */
    public function __construct(
        public string $name,
        public array $columns,
        public bool $unique,
    ) {
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

/** One ORDER BY term of a criteria query: a property name and a direction (spec/query-ast.md). */
final readonly class Ordering
{
    public function __construct(
        public string $property,
        public SortOrder $order = SortOrder::Asc,
    ) {
    }
}

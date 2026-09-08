<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** One column of a declared index, in index order (ADR-0007). */
final readonly class IndexColumn
{
    public function __construct(
        public string $propertyName,
        public string $columnName,
        public bool $descending,
    ) {
    }
}

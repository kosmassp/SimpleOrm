<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One column of a `TableSchemaIndex`, in index order (ADR-0017). */
final readonly class TableSchemaIndexPart
{
    public function __construct(
        public string $columnName,
        public bool $descending = false,
    ) {
    }
}

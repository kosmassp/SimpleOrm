<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** A declared parameter of a `#[Statement]`/`#[Procedure]` entity (ADR-0008 add.2): SQL-side name and neutral type. */
final readonly class StatementParameter
{
    public function __construct(
        public string $name,
        public ColumnType $type,
    ) {
    }
}

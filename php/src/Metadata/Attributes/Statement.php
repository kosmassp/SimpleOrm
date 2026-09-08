<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Metadata\ColumnType;

/**
 * A statement-backed entity (ADR-0008 add.2, ADR-0010): the type IS the query.
 * Declared parameters map SQL-side name → neutral type; every `@placeholder` in
 * the SQL must be declared (`PRM-010`) and every declaration used (`PRM-011`).
 */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class Statement
{
    /** @param array<string, ColumnType> $parameters */
    public function __construct(
        public string $sql,
        public array $parameters = [],
    ) {
    }
}

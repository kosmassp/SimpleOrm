<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;

/** `property is [not] null` (ADR-0020); strict null semantics — `Eq`/`Ne(null)` render this, never `= NULL`. @internal */
final class NullCheck extends Criteria
{
    public function __construct(
        public readonly string $property,
        public readonly bool $negated,
    ) {
    }
}

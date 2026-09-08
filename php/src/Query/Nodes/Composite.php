<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;

/** `(a and b …)` / `(a or b …)`; empty renders its identity truth-value (ADR-0020 add.1). @internal */
final class Composite extends Criteria
{
    /**
     * @param 'and'|'or' $operator
     * @param list<Criteria> $children
     */
    public function __construct(
        public readonly string $operator,
        public readonly array $children,
    ) {
    }
}

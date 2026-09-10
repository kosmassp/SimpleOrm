<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\SelectAst;

/**
 * `(p1, p2 …) in (select …)` (ADR-0022 add.1, SubSelect eager loading): the
 * listed root properties — a row value when more than one — against a
 * subquery whose projection has the same arity (`QRY-006` otherwise). @internal
 */
final class SubqueryMembership extends Criteria
{
    /** @param list<string> $properties */
    public function __construct(
        public readonly array $properties,
        public readonly SelectAst $subquery,
    ) {
    }
}

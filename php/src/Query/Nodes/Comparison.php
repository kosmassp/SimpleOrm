<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;

/** `property <op> value` (ADR-0020); the operator token is one of `= <> > >= < <= like` (pinned by conformance/ast). @internal */
final class Comparison extends Criteria
{
    public function __construct(
        public readonly string $property,
        public readonly string $operator,
        public readonly mixed $value,
    ) {
    }
}

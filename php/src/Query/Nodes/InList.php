<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;

/** `property in (values)` (ADR-0020); empty matches nothing, a null element is `QRY-007`. @internal */
final class InList extends Criteria
{
    /** @param list<mixed> $values */
    public function __construct(
        public readonly string $property,
        public readonly array $values,
    ) {
    }
}

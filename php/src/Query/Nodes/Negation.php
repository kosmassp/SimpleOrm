<?php

declare(strict_types=1);

namespace SimpleOrm\Query\Nodes;

use SimpleOrm\Query\Criteria;

/** `not <inner>`. @internal */
final class Negation extends Criteria
{
    public function __construct(public readonly Criteria $inner)
    {
    }
}

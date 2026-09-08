<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

/** Result fixture for {@see BadRegistry::expressionNeedsNullable()} / {@see BadRegistry::expressionWithComment()} (`VAL-010`). */
final readonly class TotalRow
{
    public function __construct(public int $total)
    {
    }
}

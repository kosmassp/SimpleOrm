<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

use DateTimeImmutable;

/** Result fixture for {@see BadRegistry::nullableIntoNonNullable()}: a non-nullable member fed from a nullable column (`VAL-010`). */
final readonly class StampRow
{
    public function __construct(public DateTimeImmutable $stamp)
    {
    }
}

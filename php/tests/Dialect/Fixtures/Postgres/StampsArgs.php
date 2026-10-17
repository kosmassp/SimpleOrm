<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use DateTimeImmutable;

/** A temporal collection: the converter's ISO-8601 strings form a `timestamptz[]` the server infers from the comparison. */
final readonly class StampsArgs
{
    /** @param list<DateTimeImmutable> $stamps */
    public function __construct(public array $stamps)
    {
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;

/** The args object for `DailySales` (§7.12): public properties bind to `@since`; the declared type must match (`PRM-012`). */
final readonly class DailySalesArgs
{
    public function __construct(public DateTimeImmutable $since)
    {
    }
}

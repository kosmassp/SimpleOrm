<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Statements;

use DateTimeImmutable;

/** Args for executing `DailySales` (§7.12): public properties bind to `@since`; the declared type must match (`PRM-012`). */
final readonly class DailySalesArgs
{
    public function __construct(public DateTimeImmutable $since)
    {
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

use DateTimeImmutable;

/** Args for `Commands::cancelStaleTransactions()`: both instants must be UTC (§7.9, `VAL-020` otherwise). */
final readonly class CancelStaleArgs
{
    public function __construct(
        public DateTimeImmutable $before,
        public DateTimeImmutable $nowUtc,
    ) {
    }
}

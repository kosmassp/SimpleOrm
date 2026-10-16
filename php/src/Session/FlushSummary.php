<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * What one {@see Db::flush()} wrote (Level 3 M2, unit-of-work.md "The calls"): how many rows were
 * inserted, updated (dirty tracked instances that wrote a column-list update), and deleted.
 * A readonly value class standing in for the reference's summary record (CODING-STANDARD §10).
 */
final readonly class FlushSummary
{
    public function __construct(
        public int $inserted,
        public int $updated,
        public int $deleted,
    ) {
    }
}

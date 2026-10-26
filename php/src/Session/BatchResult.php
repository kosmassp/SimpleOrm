<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * What one {@see DbBatch::send()} did (ADR-0044, spec/session.md "The explicit batch"): `$affected` — one count per
 * queued operation, in queue order (an insert 1; a generated update or delete its judged count; a registry command
 * the driver's count; an operation that issued no statement — a clean tracked update — 0) — and `$messages`, the
 * number of messages the plan formed. A readonly value class standing in for the reference's result record
 * (CODING-STANDARD §10).
 */
final readonly class BatchResult
{
    /** @param list<int> $affected */
    public function __construct(
        public array $affected,
        public int $messages,
    ) {
    }
}

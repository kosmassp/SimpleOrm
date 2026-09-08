<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

/** Args for `Queries::transactionsWithDetails()`: `userId` binds to `@userId` (§7.12). */
final readonly class TransactionsByUserArgs
{
    public function __construct(public int $userId)
    {
    }
}

<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

/** Args for the `TransactionWithDetails` JSON-nesting query (§7.12). */
final readonly class TransactionsByUserArgs
{
    public function __construct(public int $userId)
    {
    }
}

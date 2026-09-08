<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

use SimpleOrm\Session\Command;

/** The command registry (§6): the one legitimate hand-SQL write at Level 1, a partial update (§7.15). */
final class Commands
{
    private function __construct()
    {
    }

    /** Cancels every pending transaction created before a cut-off; touches only the columns it changes. */
    public static function cancelStaleTransactions(): Command
    {
        return Command::inline(CancelStaleArgs::class, <<<'SQL'
            update transactions
            set status = 'Cancelled',
                updated_at = @nowUtc,
                version = version + 1
            where status = 'Pending'
              and created_at < @before
            SQL);
    }
}

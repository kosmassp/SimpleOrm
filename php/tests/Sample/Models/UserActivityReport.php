<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Procedure;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/** Procedure entity `user_activity_report` (ADR-0008 add.): declaration-only on SQLite — no procedures; the export is still pinned. */
#[Procedure('user_activity_report', <<<'SQL'
    select u.id                as user_id,
           u.name              as user_name,
           count(t.id)         as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at)   as last_transaction_at_utc
    from users u
    left join transactions t on t.user_id = u.id and t.created_at >= @since
    group by u.id, u.name
    SQL, parameters: ['since' => ColumnType::DateTime])]
final class UserActivityReport
{
    #[Column]
    public int $userId;

    #[Column]
    public string $userName;

    #[Column(type: ColumnType::Int32)]
    public int $transactionCount;

    #[Column]
    public Decimal $totalAmount;

    #[Column]
    public ?DateTimeImmutable $lastTransactionAtUtc = null;
}

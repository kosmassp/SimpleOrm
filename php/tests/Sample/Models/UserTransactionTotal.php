<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/** View `user_transaction_totals` (ADR-0008 add.3): read-only, keyed by user; the defining SELECT lives in the attribute. */
#[View('user_transaction_totals', <<<'SQL'
    select u.id              as user_id,
           u.name            as user_name,
           count(t.id)       as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at) as last_transaction_at
    from users u
    left join transactions t on t.user_id = u.id
    group by u.id, u.name
    SQL)]
final class UserTransactionTotal
{
    #[Key]
    #[Column]
    public int $userId;

    #[Column]
    public string $userName;

    #[Column(type: ColumnType::Int32)]
    public int $transactionCount;

    #[Column]
    public Decimal $totalAmount;

    #[Column('last_transaction_at')]
    public ?DateTimeImmutable $lastTransactionAtUtc = null;
}

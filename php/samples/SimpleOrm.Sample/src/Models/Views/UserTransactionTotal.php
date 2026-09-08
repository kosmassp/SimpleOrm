<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Views;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/**
 * View `user_transaction_totals`: one row per user with transaction totals.
 * Self-contained (ADR-0008 add.3): the defining SELECT lives in the attribute
 * and `Db::createView()` / the view migration generate the CREATE VIEW.
 * Read-only; declares `#[Key]` so read-by-key works, but never `#[Generated]`:
 * nothing is written to a view. Not a `BaseModel`: projections carry no audit
 * columns.
 */
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

    /** Added by migration V0006; null for users with no transactions. */
    #[Column('last_transaction_at')]
    public ?DateTimeImmutable $lastTransactionAtUtc = null;
}

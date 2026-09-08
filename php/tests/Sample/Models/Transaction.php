<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Version;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Types\Decimal;

/** Table `transactions`: carries the version column — the optimistic-concurrency fixture (§7.16). */
#[Table('transactions')]
#[Index(['userId'])]
#[Index(['status', 'createdAtUtc', SortOrder::Desc], name: 'ix_transactions_status_created')]
final class Transaction extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(User::class)]
    public int $userId;

    #[ManyToOne('userId')]
    public private(set) ?User $user = null;

    #[Column]
    public TransactionStatus $status = TransactionStatus::Pending;

    #[Column]
    public Decimal $amount;

    #[Version]
    #[Column]
    public int $version = 0;

    /** Added by migration V0003. */
    #[Column]
    public ?string $note = null;

    #[OneToMany(TransactionDetail::class, 'transactionId')]
    public private(set) array $details = [];
}

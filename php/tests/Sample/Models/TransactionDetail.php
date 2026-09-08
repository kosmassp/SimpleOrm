<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/** Table `transaction_details`: the many side of `Transaction.details`. `quantity` is an `int32` column — PHP's `int` needs the token override. */
#[Table('transaction_details')]
#[Index(['transactionId'])]
final class TransactionDetail extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(Transaction::class)]
    public int $transactionId;

    #[ManyToOne('transactionId')]
    public private(set) ?Transaction $transaction = null;

    #[Column]
    public string $description;

    #[Column(type: ColumnType::Int32)]
    public int $quantity;

    #[Column]
    public Decimal $unitPrice;
}

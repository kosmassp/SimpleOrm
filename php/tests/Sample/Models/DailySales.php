<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use DateTimeImmutable;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/** Statement entity (ADR-0008 add.2/ADR-0010): the type IS the query; `-- notnull:` lifts the expression columns' nullability (§7.19). */
#[Statement(<<<'SQL'
    -- notnull: sales_date, transaction_count, total_amount
    select date(created_at) as sales_date,
           count(id)        as transaction_count,
           sum(amount)      as total_amount
    from transactions
    where created_at >= @since
    group by date(created_at)
    order by sales_date desc
    SQL, parameters: ['since' => ColumnType::DateTime])]
final class DailySales
{
    #[Column(type: ColumnType::Date)]
    public DateTimeImmutable $salesDate;

    #[Column(type: ColumnType::Int32)]
    public int $transactionCount;

    #[Column]
    public Decimal $totalAmount;
}

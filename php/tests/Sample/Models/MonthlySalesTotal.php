<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/** Materialized view `monthly_sales_totals` (ADR-0008 add.): declaration-only on SQLite (`DDL-002` on create); carries an `#[Index]`, which is what distinguishes it from a plain view. */
#[MaterializedView('monthly_sales_totals', <<<'SQL'
    select strftime('%Y-%m', created_at) as sales_month,
           count(id)                     as transaction_count,
           sum(amount)                   as total_amount
    from transactions
    group by sales_month
    SQL)]
#[Index(['salesMonth'], unique: true)]
final class MonthlySalesTotal
{
    /** Calendar month as `YYYY-MM`. */
    #[Key]
    #[Column]
    public string $salesMonth;

    #[Column(type: ColumnType::Int32)]
    public int $transactionCount;

    #[Column]
    public Decimal $totalAmount;
}

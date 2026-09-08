<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\MaterializedViews;

use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Types\Decimal;

/**
 * Materialized view `monthly_sales_totals`: one row per calendar month
 * (ADR-0008 addendum). Read-only; carries `#[Index]`, the capability that
 * distinguishes a materialized view from a plain view. Dormant on SQLite (no
 * materialized views): declaration-only metadata, capability-gated out of
 * SchemaGuard until a dialect with them arrives. Not a `BaseModel`.
 */
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

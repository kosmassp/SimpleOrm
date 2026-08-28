namespace SimpleOrm.Sample.Models;

/// <summary>
/// Statement-backed entity (ADR-0008 addendum 2): self-contained result shape —
/// inline SQL plus the declared parameter contract. Read-only and keyless;
/// SchemaGuard validates by preparing the statement. Not a <c>BaseModel</c>:
/// projections carry no audit columns.
/// </summary>
[Statement("""
    select date(created_at) as sales_date,
           count(id)        as transaction_count,
           sum(amount)      as total_amount
    from transactions
    where created_at >= @since
    group by date(created_at)
    order by sales_date desc
    """,
    "since", typeof(DateTime))]
public sealed class DailySales
{
    [Column]
    public DateOnly SalesDate { get; set; }

    [Column]
    public int TransactionCount { get; set; }

    [Column]
    public decimal TotalAmount { get; set; }
}

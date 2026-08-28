namespace SimpleOrm.Sample.Models;

/// <summary>
/// Materialized view <c>monthly_sales_totals</c>: one row per calendar month
/// (ADR-0008 addendum). Read-only; carries <c>[Index]</c> — the capability that
/// distinguishes a materialized view from a plain view. Dormant on SQLite (no
/// materialized views): declaration-only metadata, capability-gated out of
/// SchemaGuard until a dialect with them arrives (Level 4 Postgres). Not a
/// <c>BaseModel</c>: projections carry no audit columns.
/// </summary>
[MaterializedView("monthly_sales_totals")]
[Index(nameof(SalesMonth), Unique = true)]
public sealed class MonthlySalesTotal
{
    /// <summary>Calendar month as <c>YYYY-MM</c>.</summary>
    [Key]
    [Column]
    public required string SalesMonth { get; set; }

    [Column]
    public int TransactionCount { get; set; }

    [Column]
    public decimal TotalAmount { get; set; }
}

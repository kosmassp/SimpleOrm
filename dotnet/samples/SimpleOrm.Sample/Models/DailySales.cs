namespace SimpleOrm.Sample.Models;

/// <summary>
/// Statement-backed entity (ADR-0008): the result shape of
/// <c>Sql/Reports/DailySales.sql</c>. Read-only and keyless; SchemaGuard validates
/// the class by preparing that statement. Not a <c>BaseModel</c>: projections carry
/// no audit columns.
/// </summary>
[Statement("Reports/DailySales.sql")]
public sealed class DailySales
{
    [Column]
    public DateOnly SalesDate { get; set; }

    [Column]
    public int TransactionCount { get; set; }

    [Column]
    public decimal TotalAmount { get; set; }
}

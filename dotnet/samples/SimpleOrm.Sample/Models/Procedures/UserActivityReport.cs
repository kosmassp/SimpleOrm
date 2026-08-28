namespace SimpleOrm.Sample.Models;

/// <summary>
/// Procedure-backed entity (ADR-0008 addendum): the result shape of
/// <c>user_activity_report</c>. Call parameters bind from an args record at call
/// time; how the invocation is rendered is the dialect's job. Read-only and keyless.
/// Dormant on SQLite (no stored procedures): declaration-only metadata,
/// capability-gated out of SchemaGuard until a dialect with them arrives (Level 4).
/// Not a <c>BaseModel</c>: projections carry no audit columns.
/// </summary>
[Procedure("user_activity_report")]
public sealed class UserActivityReport
{
    [Column]
    public long UserId { get; set; }

    [Column]
    public required string UserName { get; set; }

    [Column]
    public int TransactionCount { get; set; }

    [Column]
    public decimal TotalAmount { get; set; }

    [Column]
    public DateTime? LastTransactionAtUtc { get; set; }
}

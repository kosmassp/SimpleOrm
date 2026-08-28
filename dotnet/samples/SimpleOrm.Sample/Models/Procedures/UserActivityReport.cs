namespace SimpleOrm.Sample.Models;

/// <summary>
/// Procedure-backed entity (ADR-0008 addenda): self-contained — name, body SQL, and
/// parameter contract in the attribute. Read-only and keyless. Dormant on SQLite
/// (no stored procedures, <c>SupportsProcedures</c> false): creation and invocation
/// arrive with a Level 4 dialect. Not a <c>BaseModel</c>: projections carry no audit
/// columns.
/// </summary>
[Procedure("user_activity_report", """
    select u.id                as user_id,
           u.name              as user_name,
           count(t.id)         as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount,
           max(t.created_at)   as last_transaction_at_utc
    from users u
    left join transactions t on t.user_id = u.id and t.created_at >= @since
    group by u.id, u.name
    """,
    "since", typeof(DateTime))]
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

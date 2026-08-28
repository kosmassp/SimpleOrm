namespace SimpleOrm.Sample.Models;

/// <summary>
/// View <c>user_transaction_totals</c>: one row per user with transaction totals.
/// Self-contained (ADR-0008 addendum 3): the defining SELECT lives in the attribute
/// and <c>db.CreateViewAsync</c> generates the CREATE VIEW. Read-only; declares
/// <c>[Key]</c> so read-by-key works at milestone 7, but never <c>[Generated]</c> —
/// nothing is written to a view. Not a <c>BaseModel</c>: projections carry no audit
/// columns.
/// </summary>
[View("user_transaction_totals", """
    select u.id              as user_id,
           u.name            as user_name,
           count(t.id)       as transaction_count,
           coalesce(sum(t.amount), 0) as total_amount
    from users u
    left join transactions t on t.user_id = u.id
    group by u.id, u.name
    """)]
public sealed class UserTransactionTotal
{
    [Key]
    [Column]
    public long UserId { get; set; }

    [Column]
    public required string UserName { get; set; }

    [Column]
    public int TransactionCount { get; set; }

    [Column]
    public decimal TotalAmount { get; set; }
}

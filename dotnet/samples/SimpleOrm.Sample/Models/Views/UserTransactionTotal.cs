namespace SimpleOrm.Sample.Models;

/// <summary>
/// View <c>user_transaction_totals</c>: one row per user with transaction totals
/// (the view's CREATE VIEW lives in migrations, milestone 5). Read-only; declares
/// <c>[Key]</c> so read-by-key works at milestone 7, but never <c>[Generated]</c> —
/// nothing is written to a view. Not a <c>BaseModel</c>: projections carry no audit
/// columns.
/// </summary>
[View("user_transaction_totals")]
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

using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample;

/// <summary>
/// Hand-SQL commands — only what generation cannot express (ADR-0011). Inserts come
/// from `db.InsertAsync(entity)`; full-row updates and deletes arrive with
/// milestone 7 CRUD. What remains is the legitimate escape hatch: partial updates
/// are hand SQL at Level 1 (§7.15).
/// </summary>
public static class Commands
{
    public static readonly Command<SetTransactionStatusArgs> SetTransactionStatus = Query.Inline(
        """
        update transactions
        set status = @Status, updated_at = @UpdatedAt
        where id = @Id
        """);
}

public sealed record SetTransactionStatusArgs(long Id, TransactionStatus Status, DateTime UpdatedAt);

using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample.Dao;

/// <summary>
/// Transaction DAO: criteria reads (ADR-0012), the statement entity executed by
/// type, and the one legitimate hand-SQL write — a partial update (§7.15).
/// </summary>
public sealed class TransactionDao(Db db) : BaseDao<Transaction>(db)
{
    public Task<IReadOnlyList<Transaction>> GetByUserAsync(long userId, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(Transaction.UserId), userId)).OrderBy(nameof(Transaction.Id)).ToListAsync(ct);

    public Task<IReadOnlyList<Transaction>> GetByStatusAsync(TransactionStatus status, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(Transaction.Status), status)).OrderBy(nameof(Transaction.Id)).ToListAsync(ct);

    public Task<IReadOnlyList<DailySales>> GetDailySalesAsync(DateTime sinceUtc, CancellationToken ct)
        => Db.QueryAsync<DailySales>(new DailySalesArgs(sinceUtc), ct);

    public Task<int> SetStatusAsync(long id, TransactionStatus status, DateTime nowUtc, CancellationToken ct)
        => Db.ExecuteAsync(Commands.SetTransactionStatus, new SetTransactionStatusArgs(id, status, nowUtc), ct);
}

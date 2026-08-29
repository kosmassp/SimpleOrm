using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample.Repositories;

/// <summary>
/// Transaction repository: criteria reads, the statement entity executed by type,
/// and the one legitimate hand-SQL write — a partial update (§7.15).
/// </summary>
public sealed class TransactionRepository(Db db) : Repository<Transaction>(db)
{
    public Task<IReadOnlyList<Transaction>> GetByUserAsync(long userId, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(Transaction.UserId), userId)).OrderBy(nameof(Transaction.Id)).ToListAsync(ct);

    public Task<IReadOnlyList<Transaction>> GetByStatusAsync(TransactionStatus status, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(Transaction.Status), status)).OrderBy(nameof(Transaction.Id)).ToListAsync(ct);

    public Task<IReadOnlyList<DailySales>> GetDailySalesAsync(DateTime sinceUtc, CancellationToken ct)
        => Db.QueryAsync<DailySales>(new DailySalesArgs(sinceUtc), ct);

    /// <summary>Read-modify-write through the generated update — participates in optimistic concurrency (§7.16).</summary>
    public async Task SetStatusAsync(long id, TransactionStatus status, DateTime nowUtc, CancellationToken ct)
    {
        var transaction = await GetAsync(id, ct);
        transaction.Status = status;
        transaction.UpdatedAtUtc = nowUtc;
        await UpdateAsync(transaction, ct);
    }
}

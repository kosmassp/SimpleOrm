using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample.Dao;

/// <summary>
/// Per-entity DAO: generic operations from the base; specific reads are one-line
/// criteria (ADR-0012) — no per-table SQL anywhere.
/// </summary>
public sealed class UserDao(Db db) : BaseDao<User>(db)
{
    public Task<User> GetByEmailAsync(string email, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(User.Email), email)).SingleAsync(ct);

    public Task<IReadOnlyList<User>> GetByIdsAsync(IReadOnlyList<long> ids, CancellationToken ct)
        => Query().Where(Criteria.In(nameof(User.Id), ids)).OrderBy(nameof(User.Id)).ToListAsync(ct);
}

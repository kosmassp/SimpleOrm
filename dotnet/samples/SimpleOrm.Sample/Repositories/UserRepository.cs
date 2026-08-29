using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample.Repositories;

/// <summary>
/// Per-entity repository: the generic surface comes from the library's
/// <see cref="Repository{TEntity}"/> (ADR-0016); only entity-specific reads live
/// here — one-line criteria, no per-table SQL.
/// </summary>
public sealed class UserRepository(Db db) : Repository<User>(db)
{
    public Task<User> GetByEmailAsync(string email, CancellationToken ct)
        => Query().Where(Criteria.Eq(nameof(User.Email), email)).SingleAsync(ct);

    public Task<IReadOnlyList<User>> GetByIdsAsync(IReadOnlyList<long> ids, CancellationToken ct)
        => Query().Where(Criteria.In(nameof(User.Id), ids)).OrderBy(nameof(User.Id)).ToListAsync(ct);
}

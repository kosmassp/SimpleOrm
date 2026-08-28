using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample.Dao;

/// <summary>
/// Per-entity DAO: generic operations from the base, plus this entity's specific
/// reads. The registry queries wrapped here are the Level 1 escape hatch
/// (ADR-0010); at Level 2 they become criteria calls on the base and this class
/// shrinks or disappears.
/// </summary>
public sealed class UserDao(Db db) : BaseDao<User>(db)
{
    public Task<User> GetByEmailAsync(string email, CancellationToken ct)
        => Db.QuerySingleAsync(Queries.UserByEmail, new UserByEmailArgs(email), ct);

    public Task<User?> FindByIdAsync(long id, CancellationToken ct)
        => Db.QuerySingleOrDefaultAsync(Queries.UserById, new UserByIdArgs(id), ct);

    public Task<IReadOnlyList<User>> GetByIdsAsync(IReadOnlyList<long> ids, CancellationToken ct)
        => Db.QueryAsync(Queries.UsersByIds, new UsersByIdsArgs(ids), ct);
}

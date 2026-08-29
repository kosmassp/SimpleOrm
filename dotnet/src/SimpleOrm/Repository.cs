namespace SimpleOrm;

/// <summary>
/// The per-entity accessor (ADR-0016): generic CRUD, key reads, and criteria over
/// one injected session — the base every app-side data layer was rewriting by hand.
/// Instance-based, session-first (§7.17): no statics, no ambient state; one
/// repository instance per unit of work, subclass to add entity-specific methods.
/// (Deliberately not named DbContext — that is EF's word for the session, which is
/// <see cref="Db"/> here.)
/// </summary>
public class Repository<TEntity>(Db db)
    where TEntity : class
{
    /// <summary>The session this repository operates on.</summary>
    protected Db Db { get; } = db;

    public Task InsertAsync(TEntity entity, CancellationToken ct) => Db.InsertAsync(entity, ct);

    public Task UpdateAsync(TEntity entity, CancellationToken ct) => Db.UpdateAsync(entity, ct);

    /// <summary>A key (or tuple) deletes by key; passing the entity gives the version-checked delete (§7.16).</summary>
    public Task DeleteAsync(object keyOrEntity, CancellationToken ct) => Db.DeleteAsync<TEntity>(keyOrEntity, ct);

    public Task<TEntity> GetAsync(object key, CancellationToken ct) => Db.GetAsync<TEntity>(key, ct);

    public Task<TEntity?> GetOrDefaultAsync(object key, CancellationToken ct) => Db.GetOrDefaultAsync<TEntity>(key, ct);

    public Task<IReadOnlyList<TEntity>> GetAllAsync(CancellationToken ct) => Db.QueryAllAsync<TEntity>(ct);

    /// <summary>Criteria find (ADR-0012); compose with And/Or for more.</summary>
    public Task<IReadOnlyList<TEntity>> FindAsync(Criteria criteria, CancellationToken ct)
        => Db.Query<TEntity>().Where(criteria).ToListAsync(ct);

    /// <summary>The full criteria chain for ordering and paging.</summary>
    public CriteriaQuery<TEntity> Query() => Db.Query<TEntity>();
}

namespace SimpleOrm.Sample.Dao;

/// <summary>
/// The base of the app-side DAO layer (decision log 2026-08-28: ergonomics above
/// <see cref="Db"/> are application architecture, not library). Instance-based with
/// the session injected — no statics, no ambient state — so the pattern ports to
/// any language and can sit behind an interface for tests.
///
/// Generic operations come from metadata-generated code; nothing here is
/// per-table SQL. Growth path: milestone 7 adds GetAsync/UpdateAsync/DeleteAsync;
/// Level 2 adds criteria finds through the query AST
/// (e.g. <c>FindAsync(u =&gt; u.Email == x)</c>) — deliberately NOT hand-rolled at
/// Level 1 (§10.4: no string-based query builder).
/// </summary>
public abstract class BaseDao<TEntity>(Db db)
    where TEntity : class
{
    /// <summary>The session this DAO operates on; one DAO instance per unit of work.</summary>
    protected Db Db { get; } = db;

    public Task InsertAsync(TEntity entity, CancellationToken ct) => Db.InsertAsync(entity, ct);

    public Task<IReadOnlyList<TEntity>> GetAllAsync(CancellationToken ct) => Db.QueryAllAsync<TEntity>(ct);
}

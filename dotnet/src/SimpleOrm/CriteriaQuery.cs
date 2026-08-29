using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// How <see cref="CriteriaQuery{TEntity}.Include"/> fetches (ADR-0022 add.1,
/// owner: "it will depends on the need"). All three produce identical loaded
/// graphs; they differ in round trips and data shape:
/// <list type="bullet">
/// <item><see cref="MultiQuery"/> (default): root query + one batched query per
/// navigation — no duplicated data, paging always correct.</item>
/// <item><see cref="SubSelect"/>: like MultiQuery, but each navigation's owner
/// filter is <c>IN (select … from the root query)</c> instead of a client-side
/// key list — no owner-side chunking and correct paging (a paged root gains
/// key-tiebroken ordering so both evaluations pick the same rows; the
/// many-to-many link→target hop still key-lists client-side).</item>
/// <item><see cref="Join"/>: one SELECT with LEFT JOINs — fewest round trips;
/// a collection include refuses paging (<c>REL-005</c>, never in-memory — to-one
/// includes page fine) and at most one collection navigation joins
/// (<c>REL-006</c>, never a Cartesian product); keyless roots/targets refuse
/// (<c>REL-003</c>).</item>
/// </list>
/// </summary>
public enum FetchMode
{
    MultiQuery,
    SubSelect,
    Join,
}

/// <summary>
/// The session-first criteria chain (ADR-0012): <c>db.Query&lt;User&gt;()
/// .Where(…).OrderBy(…).Limit(…)</c>. <see cref="Where"/> arguments are implicitly
/// ANDed. The rendered SELECT lists explicit columns (never <c>*</c>), resolves
/// property names through the metadata (<c>QRY-006</c> when unknown), and binds
/// every value as a parameter.
/// </summary>
public sealed class CriteriaQuery<TEntity>
    where TEntity : class
{
    private readonly Db _db;
    private readonly List<Criteria> _where = [];
    private readonly List<Ordering> _orderings = [];
    private readonly List<string> _includes = [];
    private FetchMode _fetch = FetchMode.MultiQuery;
    private long? _limit;
    private long? _offset;

    internal CriteriaQuery(Db db) => _db = db;

    /// <summary>Adds criteria; multiple arguments and multiple calls are ANDed.</summary>
    public CriteriaQuery<TEntity> Where(params Criteria[] criteria)
    {
        _where.AddRange(criteria);
        return this;
    }

    public CriteriaQuery<TEntity> OrderBy(string property, SortOrder order = SortOrder.Asc)
    {
        _orderings.Add(new Ordering(property, order));
        return this;
    }

    public CriteriaQuery<TEntity> Limit(long limit)
    {
        _limit = limit;
        return this;
    }

    public CriteriaQuery<TEntity> Offset(long offset)
    {
        _offset = offset;
        return this;
    }

    /// <summary>
    /// Eager loading (ADR-0022): the named navigations load automatically with
    /// the query — the root query plus **one batch load per navigation** (M3
    /// machinery: visible round trips, correct paging, shared instances). An
    /// unknown name is <c>REL-001</c>, even when the query matches no rows.
    /// Repeatable; duplicates load once.
    /// </summary>
    public CriteriaQuery<TEntity> Include(params string[] navigations)
    {
        foreach (var navigation in navigations)
        {
            if (!_includes.Contains(navigation))
            {
                _includes.Add(navigation);
            }
        }

        return this;
    }

    /// <summary>Chooses how the includes fetch (ADR-0022 add.1); no includes, no effect.</summary>
    public CriteriaQuery<TEntity> Fetch(FetchMode mode)
    {
        _fetch = mode;
        return this;
    }

    public async Task<IReadOnlyList<TEntity>> ToListAsync(CancellationToken ct)
    {
        var map = _db.Maps.Load<TEntity>();

        // Includes validate before any SQL runs (REL-001 in every mode).
        foreach (var navigation in _includes)
        {
            _db.ResolveNavigation<TEntity>(map, navigation);
        }

        if (_includes.Count > 0 && _fetch == FetchMode.Join)
        {
            return await _db.EagerJoinLoadAsync<TEntity>(ToAst(map), _includes, ct).ConfigureAwait(false);
        }

        var ast = ToAst(map);
        if (_fetch == FetchMode.SubSelect && _includes.Count > 0
            && (ast.Limit is not null || ast.Offset is not null))
        {
            // A paged subselect re-evaluates the root, so the page must be
            // deterministic: the key columns break ordering ties. The tiebroken
            // AST drives BOTH the root query and every subquery, so the two
            // evaluations pick the same rows.
            var orderings = new List<Ordering>(ast.Orderings);
            foreach (var key in map.KeyProperties)
            {
                if (!orderings.Any(o => string.Equals(o.Property, key.PropertyName, StringComparison.OrdinalIgnoreCase)))
                {
                    orderings.Add(new Ordering(key.PropertyName));
                }
            }

            ast = new SelectAst(map, ast.Where, orderings, ast.Limit, ast.Offset);
        }

        var rows = await _db.ExecuteAstAsync<TEntity>(ast, ct).ConfigureAwait(false);
        if (_includes.Count > 0)
        {
            var ownerSubquery = _fetch == FetchMode.SubSelect ? ast : null;
            foreach (var navigation in _includes)
            {
                await _db.LoadEachAsync(rows, navigation, ownerSubquery, ct).ConfigureAwait(false);
            }
        }

        return rows;
    }

    /// <summary>Exactly one row: zero throws <c>QRY-001</c>, more than one throws <c>QRY-002</c>.</summary>
    public async Task<TEntity> SingleAsync(CancellationToken ct)
    {
        var rows = await ToListAsync(ct).ConfigureAwait(false);
        return rows.Count switch
        {
            1 => rows[0],
            0 => throw new SimpleOrmException("QRY-001", QueryName, "expected exactly one row, found none"),
            _ => throw new SimpleOrmException("QRY-002", QueryName, $"expected exactly one row, found {rows.Count}"),
        };
    }

    public async Task<TEntity?> SingleOrDefaultAsync(CancellationToken ct)
    {
        var rows = await ToListAsync(ct).ConfigureAwait(false);
        return rows.Count switch
        {
            0 => null,
            1 => rows[0],
            _ => throw new SimpleOrmException("QRY-002", QueryName, $"expected at most one row, found {rows.Count}"),
        };
    }

    private static string QueryName => typeof(TEntity).Name + " criteria";

    /// <summary>The query as data; the dialect renders it (§10.4, ADR-0020).</summary>
    internal SelectAst ToAst(EntityMap map) => new(map, [.. _where], [.. _orderings], _limit, _offset);
}

/// <summary>
/// Binds criteria parameter values onto the command, naming them in render order
/// (@c0…). The compared property's conversion rules apply — an enum against an
/// <c>[EnumAsInt]</c> column binds as its number, not its name.
/// </summary>
internal sealed class CommandParameterBinder(DbCommand command, TypeConverter converter, string queryName)
{
    private int _next;

    public string Add(object? value, PropertyMap? property)
    {
        var name = "@c" + _next++;
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = converter.ToDatabase(value, queryName + " " + name, property?.EnumAsInt ?? false);
        command.Parameters.Add(parameter);
        return name;
    }
}

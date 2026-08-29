using System.Data.Common;

namespace SimpleOrm;

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

    public Task<IReadOnlyList<TEntity>> ToListAsync(CancellationToken ct)
        => _db.ExecuteCriteriaAsync<TEntity>(BuildCommand, ct);

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

    private string BuildCommand(DbCommand command, EntityMap map, TypeConverter converter, IDialect dialect)
    {
        var binder = new CommandParameterBinder(command, converter, QueryName);
        return dialect.SelectSql(ToAst(map), binder.Add);
    }
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

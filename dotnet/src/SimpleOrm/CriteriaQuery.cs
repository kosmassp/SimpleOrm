using System.Data.Common;
using System.Text;

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
    private readonly List<(string Property, SortOrder Order)> _orderings = [];
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
        _orderings.Add((property, order));
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

    private string BuildCommand(DbCommand command, EntityMap map, TypeConverter converter, IDialect dialect)
    {
        var renderer = new CriteriaRenderer(map, converter, command, QueryName);
        var sql = new StringBuilder("select ")
            .Append(string.Join(", ", map.Properties.Select(p => p.ColumnName)))
            .Append(" from ").Append(map.RelationName);

        if (_where.Count > 0)
        {
            sql.Append(" where ").Append(renderer.Render(
                _where.Count == 1 ? _where[0] : Criteria.And(_where.ToArray())));
        }

        if (_orderings.Count > 0)
        {
            sql.Append(" order by ").Append(string.Join(", ", _orderings.Select(o =>
                renderer.ColumnOf(o.Property) + (o.Order == SortOrder.Desc ? " desc" : string.Empty))));
        }

        if (_limit is not null || _offset is not null)
        {
            string? limitParameter = null;
            string? offsetParameter = null;
            if (_limit is not null)
            {
                limitParameter = renderer.AddValue(_limit.Value);
            }

            if (_offset is not null)
            {
                offsetParameter = renderer.AddValue(_offset.Value);
            }

            sql.Append(' ').Append(dialect.LimitOffsetClause(limitParameter, offsetParameter));
        }

        return sql.ToString();
    }
}

/// <summary>Renders a criteria tree to parameterized SQL against one entity's metadata.</summary>
internal sealed class CriteriaRenderer(EntityMap map, TypeConverter converter, DbCommand command, string queryName)
{
    private int _next;

    public string Render(Criteria criteria) => criteria switch
    {
        Criteria.Comparison c => ColumnOf(c.Property) + " " + c.Operator + " " + AddValue(c.Value),
        Criteria.InList { Values.Count: 0 } => "1 = 0",
        Criteria.InList c => ColumnOf(c.Property) + " in (" + string.Join(", ", c.Values.Select(AddValue)) + ")",
        Criteria.NullCheck c => ColumnOf(c.Property) + (c.Negated ? " is not null" : " is null"),
        Criteria.Composite c => "(" + string.Join(" " + c.Operator + " ", c.Children.Select(Render)) + ")",
        Criteria.Negation c => "not " + Render(c.Inner),
        _ => throw new ArgumentOutOfRangeException(nameof(criteria)),
    };

    public string ColumnOf(string property)
    {
        var mapped = map.Properties.FirstOrDefault(
            p => string.Equals(p.PropertyName, property, StringComparison.OrdinalIgnoreCase));
        return mapped?.ColumnName ?? throw new SimpleOrmException(
            "QRY-006", queryName, $"'{property}' is not a mapped property of {map.EntityType.Name}");
    }

    public string AddValue(object? value)
    {
        var name = "@c" + _next++;
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = converter.ToDatabase(value, queryName + " " + name);
        command.Parameters.Add(parameter);
        return name;
    }
}

namespace SimpleOrm;

/// <summary>One ORDER BY term of a criteria query: a property name and a direction.</summary>
public sealed class Ordering(string property, SortOrder order = SortOrder.Asc)
{
    public string Property { get; } = property;

    public SortOrder Order { get; } = order;
}

/// <summary>
/// The criteria query as data (§10.4, ADR-0012/0020): source metadata, an
/// implicitly ANDed predicate list, orderings, and paging. Every front-end —
/// today's string-based criteria chain, the Level 2 fluent front-end — produces
/// this and never SQL text; the dialect renders it
/// (<see cref="IDialect.SelectSql"/>). Property names, not column names: the
/// renderer resolves them through the metadata (<c>QRY-006</c>). GROUP BY is
/// deliberately absent — aggregations are <c>[Statement]</c> entities.
/// </summary>
public sealed class SelectAst
{
    public SelectAst(
        EntityMap map,
        IReadOnlyList<Criteria> where,
        IReadOnlyList<Ordering> orderings,
        long? limit = null,
        long? offset = null)
    {
        Map = map;
        Where = where;
        Orderings = orderings;
        Limit = limit;
        Offset = offset;
    }

    public EntityMap Map { get; }

    /// <summary>Predicates, implicitly ANDed. Empty means no WHERE clause.</summary>
    public IReadOnlyList<Criteria> Where { get; }

    public IReadOnlyList<Ordering> Orderings { get; }

    public long? Limit { get; }

    public long? Offset { get; }
}

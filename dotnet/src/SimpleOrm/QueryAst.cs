namespace SimpleOrm;

/// <summary>One ORDER BY term of a criteria query: a property name and a direction.</summary>
public sealed class Ordering(string property, SortOrder order = SortOrder.Asc)
{
    public string Property { get; } = property;

    public SortOrder Order { get; } = order;
}

/// <summary>
/// One joined relation of a select (ADR-0022 add.1 — join-mode eager loading):
/// LEFT JOIN <c>Target</c> aliased <c>Alias</c>, ON equality pairs between the
/// parent's properties and the target's. Only projected joins contribute columns
/// (a many-to-many's link joins without projecting).
/// </summary>
public sealed class SelectJoin(
    EntityMap target,
    string alias,
    string? parentAlias,
    IReadOnlyList<(string ParentProperty, string TargetProperty)> on,
    bool project)
{
    public EntityMap Target { get; } = target;

    public string Alias { get; } = alias;

    /// <summary>The alias this join hangs off; null joins to the root.</summary>
    public string? ParentAlias { get; } = parentAlias;

    /// <summary>Equality pairs: parent property = target property, resolved through the respective maps.</summary>
    public IReadOnlyList<(string ParentProperty, string TargetProperty)> On { get; } = on;

    /// <summary>Whether the join's columns are selected (aliased <c>alias_column</c>).</summary>
    public bool Project { get; } = project;
}

/// <summary>
/// The criteria query as data (§10.4, ADR-0012/0020): source metadata, an
/// implicitly ANDed predicate list, orderings, and paging. Every front-end —
/// today's string-based criteria chain, the Level 2 fluent front-end — produces
/// this and never SQL text; the dialect renders it
/// (<see cref="IDialect.SelectSql"/>). Property names, not column names: the
/// renderer resolves them through the metadata (<c>QRY-006</c>). GROUP BY is
/// deliberately absent — aggregations are <c>[Statement]</c> entities.
/// Joins and projections (ADR-0022 add.1) serve eager loading and subqueries;
/// no front-end exposes them directly yet.
/// </summary>
public sealed class SelectAst
{
    public SelectAst(
        EntityMap map,
        IReadOnlyList<Criteria> where,
        IReadOnlyList<Ordering> orderings,
        long? limit = null,
        long? offset = null,
        IReadOnlyList<PropertyMap>? projection = null,
        IReadOnlyList<SelectJoin>? joins = null)
    {
        Map = map;
        Where = where;
        Orderings = orderings;
        Limit = limit;
        Offset = offset;
        Projection = projection;
        Joins = joins ?? [];
    }

    public EntityMap Map { get; }

    /// <summary>Predicates, implicitly ANDed. Empty means no WHERE clause.</summary>
    public IReadOnlyList<Criteria> Where { get; }

    public IReadOnlyList<Ordering> Orderings { get; }

    public long? Limit { get; }

    public long? Offset { get; }

    /// <summary>The projected properties of the root, or null for every mapped column (a subquery selects only what it feeds).</summary>
    public IReadOnlyList<PropertyMap>? Projection { get; }

    /// <summary>LEFT JOINs for join-mode eager loading (ADR-0022 add.1); empty for plain queries.</summary>
    public IReadOnlyList<SelectJoin> Joins { get; }
}

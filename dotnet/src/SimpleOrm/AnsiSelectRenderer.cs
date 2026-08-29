using System.Text;

namespace SimpleOrm;

/// <summary>
/// Binds one criteria value; <paramref name="property"/> is the mapped property the
/// value compares against (its conversion rules — e.g. <c>[EnumAsInt]</c> — apply),
/// or null for paging values. Returns the placeholder to render.
/// </summary>
public delegate string BindCriteriaParameter(object? value, PropertyMap? property);

/// <summary>
/// The shared ANSI rendering of a <see cref="SelectAst"/> (ADR-0020): explicit
/// column list (never <c>*</c>), parameters bound in render order
/// (<c>@c0…</c>: WHERE first, then limit, then offset), dialect knobs consulted
/// where SQL differs (limit/offset today). A dialect's
/// <see cref="IDialect.SelectSql"/> normally delegates here and overrides what
/// its SQL disagrees with — the AST is the contract, this rendering is the
/// reference.
///
/// A join-free, projection-free select renders exactly as pinned by
/// <c>conformance/ast/</c>. Joins (ADR-0022 add.1) alias the root <c>t</c> and
/// each join <c>j0…</c>; projected columns alias as <c>&lt;alias&gt;_&lt;column&gt;</c>
/// so segment readers can partition the row.
///
/// Null semantics are explicit and strict (ADR-0020): <c>Eq(p, null)</c> renders
/// <c>is null</c> and <c>Ne(p, null)</c> renders <c>is not null</c> — never
/// <c>= NULL</c>, which silently matches nothing; any other comparison with null,
/// or a null inside an IN list, is meaningless three-valued SQL and throws
/// <c>QRY-007</c>. Degenerate composites render their identity truth-values —
/// an empty IN or OR is false (<c>1 = 0</c>), an empty AND is true
/// (<c>1 = 1</c>) — never invalid SQL. Negative limit/offset is refused
/// (<c>QRY-008</c>).
/// </summary>
public static class AnsiSelectRenderer
{
    public static string SelectSql(IDialect dialect, SelectAst select, BindCriteriaParameter bindParameter)
    {
        var map = select.Map;
        var queryName = map.EntityType.Name + " criteria";
        if (select.Limit is < 0 || select.Offset is < 0)
        {
            throw new SimpleOrmException(
                "QRY-008", queryName,
                $"negative {(select.Limit is < 0 ? "limit " + select.Limit : "offset " + select.Offset)} — dialects disagree on its meaning (SQLite: no limit at all); refuse the arithmetic bug instead");
        }

        var joined = select.Joins.Count > 0;
        var rootAlias = joined ? "t" : null;
        var rootColumns = (select.Projection ?? map.Properties)
            .Select(p => joined ? $"t.{p.ColumnName} as t_{p.ColumnName}" : p.ColumnName);
        var columns = rootColumns.Concat(select.Joins
            .Where(j => j.Project)
            .SelectMany(j => j.Target.Properties.Select(p => $"{j.Alias}.{p.ColumnName} as {j.Alias}_{p.ColumnName}")));

        var sql = new StringBuilder("select ")
            .Append(string.Join(", ", columns))
            .Append(" from ").Append(map.RelationName);
        if (joined)
        {
            sql.Append(" t");
            foreach (var join in select.Joins)
            {
                var parent = join.ParentAlias ?? "t";
                var parentMap = join.ParentAlias is null
                    ? map
                    : select.Joins.First(j => j.Alias == join.ParentAlias).Target;
                sql.Append(" left join ").Append(join.Target.RelationName).Append(' ').Append(join.Alias)
                    .Append(" on ").Append(string.Join(" and ", join.On.Select(pair =>
                        $"{join.Alias}.{Resolve(join.Target, pair.TargetProperty, queryName).ColumnName}"
                        + $" = {parent}.{Resolve(parentMap, pair.ParentProperty, queryName).ColumnName}")));
            }
        }

        if (select.Where.Count > 0)
        {
            var predicate = select.Where.Count == 1 ? select.Where[0] : Criteria.And([.. select.Where]);
            sql.Append(" where ").Append(Render(predicate, map, rootAlias, queryName, dialect, bindParameter));
        }

        if (select.Orderings.Count > 0)
        {
            sql.Append(" order by ").Append(string.Join(", ", select.Orderings.Select(o =>
                Column(map, rootAlias, o.Property, queryName)
                + (o.Order == SortOrder.Desc ? " desc" : string.Empty))));
        }

        if (select.Limit is not null || select.Offset is not null)
        {
            var limitParameter = select.Limit is { } limit ? bindParameter(limit, null) : null;
            var offsetParameter = select.Offset is { } offset ? bindParameter(offset, null) : null;
            sql.Append(' ').Append(dialect.LimitOffsetClause(limitParameter, offsetParameter));
        }

        return sql.ToString();
    }

    private static string Render(
        Criteria criteria, EntityMap map, string? rootAlias, string queryName,
        IDialect dialect, BindCriteriaParameter bind)
    {
        switch (criteria)
        {
            case Criteria.Comparison { Value: null } c:
                return c.Operator switch
                {
                    "=" => Column(map, rootAlias, c.Property, queryName) + " is null",
                    "<>" => Column(map, rootAlias, c.Property, queryName) + " is not null",
                    _ => throw new SimpleOrmException(
                        "QRY-007", queryName,
                        $"'{c.Property} {c.Operator} null' has no meaning in SQL; use IsNull/IsNotNull"),
                };
            case Criteria.Comparison c:
            {
                var property = Resolve(map, c.Property, queryName);
                return Column(map, rootAlias, c.Property, queryName) + " " + c.Operator + " " + bind(c.Value, property);
            }

            case Criteria.InList { Values.Count: 0 } c:
                Resolve(map, c.Property, queryName);   // an unknown property is QRY-006 even when empty
                return "1 = 0";
            case Criteria.InList c when c.Values.Any(v => v is null):
                throw new SimpleOrmException(
                    "QRY-007", queryName,
                    $"the IN list for '{c.Property}' contains null, which SQL IN can never match; "
                    + "combine Criteria.Or(Criteria.In(…), Criteria.IsNull(…))");
            case Criteria.InList c:
            {
                var property = Resolve(map, c.Property, queryName);
                return Column(map, rootAlias, c.Property, queryName)
                    + " in (" + string.Join(", ", c.Values.Select(v => bind(v, property))) + ")";
            }

            case Criteria.SubqueryMembership c:
            {
                // (p1, p2) in (select …) — the subquery renders through the same
                // dialect and shares the parameter sequence (ADR-0022 add.1).
                foreach (var property in c.Properties)
                {
                    Resolve(map, property, queryName);
                }

                var lhs = c.Properties.Count == 1
                    ? Column(map, rootAlias, c.Properties[0], queryName)
                    : "(" + string.Join(", ", c.Properties.Select(p => Column(map, rootAlias, p, queryName))) + ")";
                return lhs + " in (" + dialect.SelectSql(c.Subquery, bind) + ")";
            }

            case Criteria.NullCheck c:
                return Column(map, rootAlias, c.Property, queryName) + (c.Negated ? " is not null" : " is null");
            case Criteria.Composite { Children.Count: 0 } c:
                // The identity truth-values: an empty AND is true, an empty OR is
                // false — dynamic composition may legitimately produce either,
                // and invalid SQL ("()") names nothing (§2).
                return c.Operator == "and" ? "1 = 1" : "1 = 0";
            case Criteria.Composite c:
                return "(" + string.Join(
                    " " + c.Operator + " ",
                    c.Children.Select(child => Render(child, map, rootAlias, queryName, dialect, bind))) + ")";
            case Criteria.Negation c:
                return "not " + Render(c.Inner, map, rootAlias, queryName, dialect, bind);
            default:
                throw new ArgumentOutOfRangeException(nameof(criteria));
        }
    }

    private static string Column(EntityMap map, string? rootAlias, string property, string queryName)
    {
        var column = Resolve(map, property, queryName).ColumnName;
        return rootAlias is null ? column : rootAlias + "." + column;
    }

    /// <summary>
    /// Property-name resolution: exact match first, then case-insensitive — but a
    /// case-insensitive match that fits more than one property is ambiguous, and
    /// ambiguity is an error (<c>QRY-006</c>), never a silent first-wins. Public
    /// for dialects that override parts of the reference rendering.
    /// </summary>
    public static PropertyMap Resolve(EntityMap map, string property, string queryName)
    {
        var exact = map.Properties.FirstOrDefault(p => string.Equals(p.PropertyName, property, StringComparison.Ordinal));
        if (exact is not null)
        {
            return exact;
        }

        var matches = map.Properties
            .Where(p => string.Equals(p.PropertyName, property, StringComparison.OrdinalIgnoreCase))
            .ToArray();
        return matches.Length switch
        {
            1 => matches[0],
            0 => throw new SimpleOrmException(
                "QRY-006", queryName, $"'{property}' is not a mapped property of {map.EntityType.Name}"),
            _ => throw new SimpleOrmException(
                "QRY-006", queryName,
                $"'{property}' is ambiguous on {map.EntityType.Name}: matches {string.Join(", ", matches.Select(m => m.PropertyName))}"),
        };
    }
}

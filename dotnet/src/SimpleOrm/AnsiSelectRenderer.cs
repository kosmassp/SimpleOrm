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
/// Null semantics are explicit and strict (ADR-0020): <c>Eq(p, null)</c> renders
/// <c>is null</c> and <c>Ne(p, null)</c> renders <c>is not null</c> — never
/// <c>= NULL</c>, which silently matches nothing; any other comparison with null,
/// or a null inside an IN list, is meaningless three-valued SQL and throws
/// <c>QRY-007</c>. Degenerate composites render their identity truth-values —
/// an empty IN or OR is false (<c>1 = 0</c>), an empty AND is true
/// (<c>1 = 1</c>) — never invalid SQL. Negative limit/offset is refused
/// (<c>QRY-008</c>): databases disagree about what it means, and "silently
/// unlimited" is a bug factory.
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

        var sql = new StringBuilder("select ")
            .Append(string.Join(", ", map.Properties.Select(p => p.ColumnName)))
            .Append(" from ").Append(map.RelationName);

        if (select.Where.Count > 0)
        {
            var predicate = select.Where.Count == 1 ? select.Where[0] : Criteria.And([.. select.Where]);
            sql.Append(" where ").Append(Render(predicate, map, queryName, bindParameter));
        }

        if (select.Orderings.Count > 0)
        {
            sql.Append(" order by ").Append(string.Join(", ", select.Orderings.Select(o =>
                Resolve(map, o.Property, queryName).ColumnName
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

    private static string Render(Criteria criteria, EntityMap map, string queryName, BindCriteriaParameter bind)
    {
        switch (criteria)
        {
            case Criteria.Comparison { Value: null } c:
                return c.Operator switch
                {
                    "=" => Resolve(map, c.Property, queryName).ColumnName + " is null",
                    "<>" => Resolve(map, c.Property, queryName).ColumnName + " is not null",
                    _ => throw new SimpleOrmException(
                        "QRY-007", queryName,
                        $"'{c.Property} {c.Operator} null' has no meaning in SQL; use IsNull/IsNotNull"),
                };
            case Criteria.Comparison c:
            {
                var property = Resolve(map, c.Property, queryName);
                return property.ColumnName + " " + c.Operator + " " + bind(c.Value, property);
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
                return property.ColumnName + " in (" + string.Join(", ", c.Values.Select(v => bind(v, property))) + ")";
            }

            case Criteria.NullCheck c:
                return Resolve(map, c.Property, queryName).ColumnName + (c.Negated ? " is not null" : " is null");
            case Criteria.Composite { Children.Count: 0 } c:
                // The identity truth-values: an empty AND is true, an empty OR is
                // false — dynamic composition may legitimately produce either,
                // and invalid SQL ("()") names nothing (§2).
                return c.Operator == "and" ? "1 = 1" : "1 = 0";
            case Criteria.Composite c:
                return "(" + string.Join(
                    " " + c.Operator + " ", c.Children.Select(child => Render(child, map, queryName, bind))) + ")";
            case Criteria.Negation c:
                return "not " + Render(c.Inner, map, queryName, bind);
            default:
                throw new ArgumentOutOfRangeException(nameof(criteria));
        }
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

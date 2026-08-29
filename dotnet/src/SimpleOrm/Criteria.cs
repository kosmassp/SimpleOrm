namespace SimpleOrm;

/// <summary>
/// The query AST (§10.4, ADR-0012): criteria are explicit trees built from static
/// factories — the portable core every language expresses natively. Property names
/// (not column names) resolve through the entity's metadata at render time; values
/// always bind as parameters. Composition is explicit, so SQL's and/or precedence
/// ambiguity cannot occur. GROUP BY is deliberately absent: aggregations are
/// <c>[Statement]</c> entities.
/// </summary>
public abstract class Criteria
{
    private protected Criteria()
    {
    }

    /// <summary>Equality; null renders <c>is null</c> (ADR-0020) — never <c>= NULL</c>, which silently matches nothing.</summary>
    public static Criteria Eq(string property, object? value) => new Comparison(property, "=", value);

    /// <summary>Inequality; null renders <c>is not null</c> (ADR-0020).</summary>
    public static Criteria Ne(string property, object? value) => new Comparison(property, "<>", value);

    public static Criteria Gt(string property, object value) => new Comparison(property, ">", value);

    public static Criteria Ge(string property, object value) => new Comparison(property, ">=", value);

    public static Criteria Lt(string property, object value) => new Comparison(property, "<", value);

    public static Criteria Le(string property, object value) => new Comparison(property, "<=", value);

    /// <summary>SQL LIKE; the caller supplies the wildcards (<c>%</c>, <c>_</c>).</summary>
    public static Criteria Like(string property, string pattern) => new Comparison(property, "like", pattern);

    public static Criteria In<T>(string property, IEnumerable<T> values)
        => new InList(property, Required(values, property).Cast<object?>().ToArray());

    public static Criteria In(string property, params object?[] values)
        => new InList(property, Required(values, property));

    /// <summary>
    /// Overload trap guard: a lone string argument would otherwise resolve to
    /// <c>In&lt;char&gt;</c> (string is IEnumerable&lt;char&gt;) and silently query
    /// per-character. A single string means a one-element list.
    /// </summary>
    public static Criteria In(string property, string value) => new InList(property, [value]);

    private static T Required<T>(T? values, string property)
        where T : class
        => values ?? throw new ArgumentNullException(
            nameof(values), $"the IN list for '{property}' is null; pass values (an empty list matches no rows)");

    public static Criteria IsNull(string property) => new NullCheck(property, negated: false);

    public static Criteria IsNotNull(string property) => new NullCheck(property, negated: true);

    public static Criteria And(params Criteria[] criteria) => new Composite("and", criteria);

    public static Criteria Or(params Criteria[] criteria) => new Composite("or", criteria);

    public static Criteria Not(Criteria criteria) => new Negation(criteria);

    internal sealed class Comparison(string property, string op, object? value) : Criteria
    {
        public string Property { get; } = property;

        public string Operator { get; } = op;

        public object? Value { get; } = value;
    }

    internal sealed class InList(string property, IReadOnlyList<object?> values) : Criteria
    {
        public string Property { get; } = property;

        public IReadOnlyList<object?> Values { get; } = values;
    }

    internal sealed class NullCheck(string property, bool negated) : Criteria
    {
        public string Property { get; } = property;

        public bool Negated { get; } = negated;
    }

    internal sealed class Composite(string op, IReadOnlyList<Criteria> children) : Criteria
    {
        public string Operator { get; } = op;

        public IReadOnlyList<Criteria> Children { get; } = children;
    }

    internal sealed class Negation(Criteria inner) : Criteria
    {
        public Criteria Inner { get; } = inner;
    }
}

namespace SimpleOrm;

/// <summary>
/// Maps a class to the result set of a stored procedure / set-returning function
/// (ADR-0008 addenda): the name, the body SQL, and the parameter contract as
/// (name, <see cref="Type"/>) token pairs — self-contained like
/// <see cref="StatementAttribute"/>:
/// <c>[Procedure("report", "select … where d &gt;= @since", "since", typeof(DateTime))]</c>.
/// Placeholders in the body must match the declared parameters both ways
/// (PRM-010/011). Read-only and keyless at Level 1 (<c>[Key]</c>, <c>[Generated]</c>,
/// <c>[Version]</c>, <c>[Index]</c> are loader errors). How the procedure is created
/// and invoked is dialect-specific; SQLite has none (<c>SupportsProcedures</c> is
/// false), so this is dormant metadata until a Level 4 dialect.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class ProcedureAttribute : Attribute
{
    public ProcedureAttribute(string name, string sql, params object[] parameters)
    {
        Name = name;
        Sql = sql;
        Parameters = parameters;
    }

    /// <summary>The procedure/function name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>The procedure body, verbatim.</summary>
    public string Sql { get; }

    /// <summary>Raw token pairs: a parameter name string followed by its CLR <see cref="Type"/>.</summary>
    public object[] Parameters { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

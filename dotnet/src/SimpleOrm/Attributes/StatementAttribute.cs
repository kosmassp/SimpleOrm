namespace SimpleOrm;

/// <summary>
/// Maps a class to a custom SQL statement (ADR-0008 addendum 2): the class is the
/// result shape of the SQL given inline in the attribute (a raw string literal keeps
/// multi-line SQL readable — attribute arguments must be constants, and raw strings
/// are). The exception to §7.5's SQL-in-files rule, by owner decision: a statement
/// entity is fully self-contained. A class carries exactly one relation source —
/// <see cref="TableAttribute"/>, <see cref="ViewAttribute"/>,
/// <see cref="MaterializedViewAttribute"/>, <see cref="StatementAttribute"/>, or
/// <see cref="ProcedureAttribute"/>.
///
/// Parameters are declared as (name, type) token pairs after the SQL, read left to
/// right: <c>[Statement("... where created_at >= @since", "since", typeof(DateTime))]</c>.
/// Loader errors: an odd token count, a token that is not a string name or a
/// <see cref="Type"/>, a duplicate name, or a mismatch between declared parameters
/// and the <c>@placeholders</c> in the SQL (both directions, the PRM error family).
///
/// Statement-backed entities are read-only and keyless at Level 1 (<c>[Key]</c>,
/// <c>[Generated]</c>, <c>[Version]</c>, and <c>[Index]</c> are loader errors).
/// SchemaGuard validates the class by preparing the statement and matching its
/// result columns against the mapped properties — no registry entry needed.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class StatementAttribute : Attribute
{
    public StatementAttribute(string sql, params object[] parameters)
    {
        Sql = sql;
        Parameters = parameters;
    }

    /// <summary>The SQL text, verbatim.</summary>
    public string Sql { get; }

    /// <summary>Raw token pairs: a parameter name string followed by its CLR <see cref="Type"/>.</summary>
    public object[] Parameters { get; }
}

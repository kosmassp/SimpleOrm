namespace SimpleOrm;

/// <summary>
/// Maps a class to the result set of a stored procedure or set-returning function
/// (ADR-0008 addendum). The class is the result shape; parameters are bound from an
/// args record at call time, like any query. Read-only and keyless at Level 1
/// (<c>[Key]</c>, <c>[Generated]</c>, <c>[Version]</c>, and <c>[Index]</c> are
/// loader errors). How the call is rendered is dialect-specific
/// (<c>EXEC</c> / <c>SELECT * FROM fn(...)</c> / <c>CALL</c>).
///
/// SQLite has no stored procedures: this attribute is dormant metadata on the
/// reference database, becoming testable when a dialect with them arrives
/// (Level 4). No sample entity until then — SchemaGuard could never validate it
/// against SQLite.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class ProcedureAttribute : Attribute
{
    public ProcedureAttribute(string name) => Name = name;

    /// <summary>The procedure/function name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

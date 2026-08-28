namespace SimpleOrm;

/// <summary>
/// Maps a class to a database view (ADR-0008). A class carries exactly one relation
/// source — <see cref="TableAttribute"/>, <see cref="ViewAttribute"/>,
/// <see cref="MaterializedViewAttribute"/>, <see cref="StatementAttribute"/>, or
/// <see cref="ProcedureAttribute"/>. View-backed entities are read-only: CRUD writes
/// refuse with a named error. <c>[Key]</c> is allowed (enables read-by-key);
/// <c>[Generated]</c>, <c>[Version]</c>, and <c>[Index]</c> are loader errors on a
/// view (a plain view cannot be indexed — a materialized view can, which is why it
/// is a separate attribute).
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class ViewAttribute : Attribute
{
    public ViewAttribute(string name) => Name = name;

    /// <summary>The view name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

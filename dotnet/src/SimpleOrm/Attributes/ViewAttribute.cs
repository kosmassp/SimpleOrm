namespace SimpleOrm;

/// <summary>
/// Maps a class to a database view (ADR-0008 + addendum 3): the view name plus its
/// defining SELECT, so the declaration is self-contained and <c>CREATE VIEW</c> can
/// be generated from metadata (ADR-0011). The defining SQL takes no parameters
/// (a placeholder in it is a loader error). A class carries exactly one relation
/// source. View-backed entities are read-only; <c>[Key]</c> is allowed,
/// <c>[Generated]</c>/<c>[Version]</c>/<c>[Index]</c> are loader errors (a plain
/// view cannot be indexed — a materialized view can, which is why it is a separate
/// attribute).
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class ViewAttribute : Attribute
{
    public ViewAttribute(string name, string sql)
    {
        Name = name;
        Sql = sql;
    }

    /// <summary>The view name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>The defining SELECT, verbatim.</summary>
    public string Sql { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

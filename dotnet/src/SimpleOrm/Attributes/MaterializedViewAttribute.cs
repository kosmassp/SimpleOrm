namespace SimpleOrm;

/// <summary>
/// Maps a class to a materialized view (ADR-0008 addenda): name plus defining
/// SELECT, self-contained like <see cref="ViewAttribute"/>. Distinct from a plain
/// view because the capability profile differs: materialized views are physically
/// stored, so <c>[Index]</c> IS allowed. Read-only; <c>[Key]</c> allowed;
/// <c>[Generated]</c>/<c>[Version]</c> are loader errors. Refresh is a dialect
/// operation.
///
/// SQLite has no materialized views: creating one throws <c>DDL-002</c> until a
/// dialect with <c>SupportsMaterializedViews</c> arrives (Level 4 Postgres).
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class MaterializedViewAttribute : Attribute
{
    public MaterializedViewAttribute(string name, string sql)
    {
        Name = name;
        Sql = sql;
    }

    /// <summary>The materialized view name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>The defining SELECT, verbatim.</summary>
    public string Sql { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

namespace SimpleOrm;

/// <summary>
/// Maps a class to a materialized view (ADR-0008 addendum). Distinct from
/// <see cref="ViewAttribute"/> because the capability profile differs: a
/// materialized view is physically stored, so <c>[Index]</c> IS allowed on it
/// (owner's rationale), while it stays a loader error on a plain view. Still
/// read-only — CRUD writes refuse with a named error; <c>[Key]</c> allowed,
/// <c>[Generated]</c> and <c>[Version]</c> are loader errors. Refreshing is a
/// dialect operation, not a mapping concern.
///
/// SQLite has no materialized views: this attribute is dormant metadata on the
/// reference database, becoming testable when a dialect with them arrives
/// (Level 4 Postgres). No sample entity until then — SchemaGuard could never
/// validate it against SQLite.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class MaterializedViewAttribute : Attribute
{
    public MaterializedViewAttribute(string name) => Name = name;

    /// <summary>The materialized view name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>Optional schema qualifier, mirroring <see cref="TableAttribute.Schema"/>.</summary>
    public string? Schema { get; set; }
}

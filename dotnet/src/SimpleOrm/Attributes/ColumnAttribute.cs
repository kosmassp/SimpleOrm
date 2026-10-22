namespace SimpleOrm;

/// <summary>
/// Marks a property as mapped and optionally names its column. Mapping is opt-in
/// (ADR-0004): in the attribute loader, only properties carrying this attribute are
/// mapped, and a public settable property with neither <see cref="ColumnAttribute"/>
/// nor <see cref="IgnoreAttribute"/> is a loader error — intent is always explicit.
/// </summary>
/// <remarks>
/// The optional <b>storage facets</b> (ADR-0043, metadata-model.md "Storage
/// facets") are dialect-neutral size declarations each dialect renders its own
/// way: <see cref="Length"/> for string and bytes columns (SQL Server
/// <c>nvarchar(50)</c>, PostgreSQL <c>character varying(50)</c>; SQLite ignores
/// it), <see cref="Precision"/>/<see cref="Scale"/> for decimals (SQL Server
/// <c>decimal(18,2)</c>, PostgreSQL <c>numeric(18,2)</c>). Without them the
/// dialect's default applies; a misplaced or malformed facet is <c>MAP-032</c>.
/// </remarks>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ColumnAttribute : Attribute
{
    /// <summary>Maps the property; the column name is derived from the property name by the naming convention (default snake_case, e.g. <c>UserId</c> → <c>user_id</c>).</summary>
    public ColumnAttribute()
    {
    }

    /// <summary>Maps the property to the named column, bypassing the naming convention.</summary>
    public ColumnAttribute(string name) => Name = name;

    /// <summary>The explicit column name, or null when the naming convention derives it.</summary>
    public string? Name { get; }

    /// <summary>The declared length of a string or bytes column (0 = the dialect's default).</summary>
    public int Length { get; init; }

    /// <summary>The declared precision of a decimal column (0 = the dialect's default).</summary>
    public int Precision { get; init; }

    /// <summary>The declared scale of a decimal column; meaningful only with <see cref="Precision"/>.</summary>
    public int Scale { get; init; }
}

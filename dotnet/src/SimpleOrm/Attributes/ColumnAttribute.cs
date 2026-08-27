namespace SimpleOrm;

/// <summary>
/// Maps a property to a column, overriding the naming convention.
/// Prefer SQL aliasing over this attribute when only one query disagrees (CLAUDE.md §7.6).
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ColumnAttribute : Attribute
{
    public ColumnAttribute(string name) => Name = name;

    /// <summary>The column name exactly as it exists in the database.</summary>
    public string Name { get; }
}

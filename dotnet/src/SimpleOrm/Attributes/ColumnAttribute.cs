namespace SimpleOrm;

/// <summary>
/// Marks a property as mapped and optionally names its column. Mapping is opt-in
/// (ADR-0004): in the attribute loader, only properties carrying this attribute are
/// mapped, and a public settable property with neither <see cref="ColumnAttribute"/>
/// nor <see cref="IgnoreAttribute"/> is a loader error — intent is always explicit.
/// </summary>
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
}

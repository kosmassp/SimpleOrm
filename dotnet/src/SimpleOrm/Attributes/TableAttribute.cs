namespace SimpleOrm;

/// <summary>
/// Maps a class to a table. Without it, the naming convention derives the table
/// name from the class name (snake_case by default).
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class TableAttribute : Attribute
{
    public TableAttribute(string name) => Name = name;

    /// <summary>The table name exactly as it exists in the database.</summary>
    public string Name { get; }

    /// <summary>Optional schema qualifier. Rarely used on SQLite; kept because the metadata model is dialect-neutral.</summary>
    public string? Schema { get; set; }
}

namespace SimpleOrm;

/// <summary>
/// Marks a property whose value the database generates (e.g. <c>INTEGER PRIMARY KEY</c>).
/// Generated columns are never written by CRUD; inserts read the value back via
/// <c>RETURNING</c> (CLAUDE.md §7.14).
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class GeneratedAttribute : Attribute;

namespace SimpleOrm;

/// <summary>
/// Declares a property deliberately unmapped. Mapping is opt-in via
/// <see cref="ColumnAttribute"/> (ADR-0004), but absence alone is not enough: a
/// public settable property with neither attribute is a loader error, because
/// silently unmapped members are bugs (CLAUDE.md §2). This attribute is how a
/// property says "not a column" out loud.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class IgnoreAttribute : Attribute;

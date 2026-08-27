namespace SimpleOrm;

/// <summary>
/// Excludes a property from mapping entirely. Explicit by design: silently ignored
/// members are bugs (CLAUDE.md §2), so opting out is always visible in the model.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class IgnoreAttribute : Attribute;

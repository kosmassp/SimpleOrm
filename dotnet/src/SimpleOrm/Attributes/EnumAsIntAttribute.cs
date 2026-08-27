namespace SimpleOrm;

/// <summary>
/// Stores an enum property as its integer value instead of the default TEXT name
/// (matched case-insensitively on read, CLAUDE.md §7.9).
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class EnumAsIntAttribute : Attribute;

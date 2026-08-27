namespace SimpleOrm;

/// <summary>
/// Marks the optimistic-concurrency version column. When present, <c>Update</c> and
/// <c>Delete</c> compare-and-increment it; zero affected rows throws
/// <c>ConcurrencyException</c> (<c>CRUD-010</c>, CLAUDE.md §7.16). At most one per entity.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class VersionAttribute : Attribute;

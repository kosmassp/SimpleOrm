namespace SimpleOrm;

/// <summary>
/// Marks a property as (part of) the primary key. Apply to several properties for a
/// composite key; key order follows declaration order. Combine with
/// <see cref="GeneratedAttribute"/> when the database produces the value.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class KeyAttribute : Attribute;

namespace SimpleOrm;

/// <summary>
/// Declares the audit column the session fills with its clock when a row is
/// inserted and the value is unset (Level 3 M3, spec/hooks.md). A <c>DateTime</c>
/// or <c>DateTimeOffset</c> column, nullable or not; at most one per entity;
/// never a key, version, or generated column — <c>MAP-026</c> otherwise. A value
/// the caller set is kept: the hook fills, it does not overwrite.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class CreatedAtAttribute : Attribute
{
}

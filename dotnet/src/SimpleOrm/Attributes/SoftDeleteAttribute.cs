namespace SimpleOrm;

/// <summary>
/// Declares the flag that turns a delete into an update (Level 3 M3,
/// spec/hooks.md): <c>Delete</c> (both forms) and a flushed <c>Remove</c> set it
/// true — with the <c>[UpdatedAt]</c> stamp and the version rules of an update —
/// instead of removing the row. Reads never filter on it implicitly: the flag
/// is an explicit criteria the caller writes, never a hidden WHERE (§2). A
/// non-nullable <c>bool</c> column; at most one per entity; never a key,
/// version, or generated column — <c>MAP-026</c> otherwise.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class SoftDeleteAttribute : Attribute
{
}

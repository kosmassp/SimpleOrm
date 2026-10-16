namespace SimpleOrm;

/// <summary>
/// Declares the audit column the session stamps with its clock on every write
/// that changes a row (Level 3 M3, spec/hooks.md): a full-row update, a
/// column-list update (the column joins the list), a tracked dirty update, a
/// flush update, and a soft delete — never an insert, never a clean update that
/// writes nothing. A <c>DateTime</c> or <c>DateTimeOffset</c> column, nullable
/// or not; at most one per entity; never a key, version, or generated column —
/// <c>MAP-026</c> otherwise. The library is its writer: a caller's value is
/// overwritten by the stamp.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class UpdatedAtAttribute : Attribute
{
}

namespace SimpleOrm;

/// <summary>
/// Declares the column a <c>[Column]</c> property was previously stored under
/// (Level 3 M5, spec/migrations.md "Draft migrations"): the diff generator reads
/// it as a declared rename — <c>simpleorm diff</c> emits a data-preserving
/// <c>RenameColumn</c> instead of a drop and an add — exactly as a
/// <c>--rename table.old=new</c> flag would, but kept with the model. The
/// declaration is permanent and harmless once applied: when the latest
/// snapshot already has the new column (or has neither), it declares nothing.
/// Carried in the metadata and exported as <c>"renamedFrom"</c>. Requires
/// <c>[Column]</c>, a non-empty name, and a name different from the current
/// column — <c>MAP-027</c> otherwise.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class RenamedFromAttribute(string previousColumn) : Attribute
{
    public string PreviousColumn { get; } = previousColumn;
}

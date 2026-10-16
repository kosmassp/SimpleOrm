namespace SimpleOrm;

/// <summary>
/// Marks a root migration version as a draft (Level 3 M5, spec/migrations.md
/// "Draft migrations"): <c>simpleorm diff --draft</c> stamps it on the version
/// it emits, and the runner refuses to apply or baseline a draft (<c>MIG-013</c>)
/// until the author has reviewed the generated code and removed the marker.
/// A draft still composes, renders, and replays under <c>shadow</c> — it is a
/// version in every respect but applicability.
/// </summary>
[AttributeUsage(AttributeTargets.Class)]
public sealed class DraftAttribute : Attribute
{
}

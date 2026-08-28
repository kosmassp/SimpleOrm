namespace SimpleOrm;

/// <summary>
/// Declares an index on the entity's table. Class-level, repeatable, conventionally
/// placed below <see cref="TableAttribute"/>. This deliberately mixes DDL declaration
/// into mapping metadata (ADR-0007, owner decision): at Level 1 it is
/// declaration-only — recorded in <c>EntityMap</c>, generating nothing — and Level 3
/// draft migrations emit <c>CREATE INDEX</c> from it. Until then the real index
/// lives in migration SQL.
///
/// Columns are referenced by property name (<c>nameof</c>-friendly); the loader
/// resolves them through the column mapping, and an unknown or unmapped property
/// name is a loader error. When <see cref="Name"/> is omitted the loader derives
/// <c>ix_&lt;table&gt;_&lt;column&gt;[_&lt;column&gt;…]</c>.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = true)]
public sealed class IndexAttribute : Attribute
{
    public IndexAttribute(params string[] propertyNames) => PropertyNames = propertyNames;

    /// <summary>Properties making up the index, in index-column order.</summary>
    public string[] PropertyNames { get; }

    /// <summary>Explicit index name; when omitted the loader derives one from the table and columns.</summary>
    public string? Name { get; set; }

    /// <summary>Declares a unique index.</summary>
    public bool Unique { get; set; }
}

namespace SimpleOrm;

/// <summary>
/// Declares an index on the entity's table. Class-level, repeatable, conventionally
/// placed below <see cref="TableAttribute"/>. This deliberately mixes DDL declaration
/// into mapping metadata (ADR-0007, owner decision): at Level 1 it is
/// declaration-only — recorded in <c>EntityMap</c>, generating nothing — and Level 3
/// draft migrations emit <c>CREATE INDEX</c> from it. Until then the real index
/// lives in migration SQL.
///
/// Each column is <c>"PropertyName"</c> or <c>"PropertyName DESC"</c> (direction
/// token <c>ASC</c>/<c>DESC</c>, case-insensitive; omitted means ascending), e.g.
/// <c>[Index(nameof(Status), nameof(CreatedAtUtc) + " DESC")]</c> — constant string
/// concatenation keeps <c>nameof</c> refactor-safety. The attribute stores the raw
/// strings; the loader parses them, resolves property names through the column
/// mapping, and rejects an unknown property or direction token as a loader error.
/// When <see cref="Name"/> is omitted the loader derives
/// <c>ix_&lt;table&gt;_&lt;column&gt;[_&lt;column&gt;…]</c>.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = true)]
public sealed class IndexAttribute : Attribute
{
    public IndexAttribute(params string[] columns) => Columns = columns;

    /// <summary>Index columns in order: <c>"PropertyName"</c> (ascending) or <c>"PropertyName ASC|DESC"</c>.</summary>
    public string[] Columns { get; }

    /// <summary>Explicit index name; when omitted the loader derives one from the table and columns.</summary>
    public string? Name { get; set; }

    /// <summary>Declares a unique index.</summary>
    public bool Unique { get; set; }
}

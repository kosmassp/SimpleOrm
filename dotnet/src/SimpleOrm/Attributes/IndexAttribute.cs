namespace SimpleOrm;

/// <summary>
/// Declares an index on the entity's table. Class-level, repeatable, conventionally
/// placed below <see cref="TableAttribute"/>. This deliberately mixes DDL declaration
/// into mapping metadata (ADR-0007, owner decision): at Level 1 it is
/// declaration-only — recorded in <c>EntityMap</c>, generating nothing — and Level 3
/// draft migrations emit <c>CREATE INDEX</c> from it. Until then the real index
/// lives in migration SQL.
///
/// Columns are a token stream read left to right, like SQL: a string names a
/// property (resolved to its column through the mapping), and a <see cref="SortOrder"/>
/// applies to the column immediately before it; a column without one is ascending.
/// <c>[Index(nameof(Status), nameof(CreatedAtUtc), SortOrder.Desc)]</c> declares
/// <c>(status ASC, created_at DESC)</c>. Loader errors: a leading or doubled
/// <see cref="SortOrder"/>, a token that is neither string nor <see cref="SortOrder"/>,
/// an unknown or unmapped property name, or an empty column list. When
/// <see cref="Name"/> is omitted the loader derives
/// <c>ix_&lt;table&gt;_&lt;column&gt;[_&lt;column&gt;…]</c>.
/// </summary>
[AttributeUsage(AttributeTargets.Class, AllowMultiple = true)]
public sealed class IndexAttribute : Attribute
{
    public IndexAttribute(params object[] columns) => Columns = columns;

    /// <summary>Raw token stream: property-name strings, each optionally followed by a <see cref="SortOrder"/>.</summary>
    public object[] Columns { get; }

    /// <summary>Explicit index name; when omitted the loader derives one from the table and columns.</summary>
    public string? Name { get; set; }

    /// <summary>Declares a unique index.</summary>
    public bool Unique { get; set; }
}

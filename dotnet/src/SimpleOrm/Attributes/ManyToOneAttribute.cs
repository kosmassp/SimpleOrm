namespace SimpleOrm;

/// <summary>
/// Declares a many-to-one navigation property: a model-typed property resolved
/// through the named foreign-key property of the same class
/// (e.g. <c>[ManyToOne(nameof(UserId))] public User? User</c>).
///
/// A navigation property is inherently transient — never a column, never written by
/// CRUD — so it needs no <see cref="ColumnAttribute"/> or <see cref="IgnoreAttribute"/>.
/// At Level 1 the library also never populates it (no hidden queries, CLAUDE.md §2):
/// user code assigns it after an explicit query, which is why it should be nullable.
/// Level 2's explicit/eager loading attaches to this same declaration (ADR-0005).
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ManyToOneAttribute : Attribute
{
    public ManyToOneAttribute(string foreignKeyProperty) => ForeignKeyProperty = foreignKeyProperty;

    /// <summary>Name of the property on this class that holds the foreign-key value.</summary>
    public string ForeignKeyProperty { get; }
}

namespace SimpleOrm;

/// <summary>
/// Declares a many-to-one navigation property: a model-typed property resolved
/// through the named foreign-key property of the same class
/// (e.g. <c>[ManyToOne(nameof(UserId))] public User? User</c>).
///
/// A navigation property is inherently transient — never a column, never written by
/// CRUD — so it needs no <see cref="ColumnAttribute"/> or <see cref="IgnoreAttribute"/>.
/// It must not expose a public setter (declare it <c>{ get; private set; }</c>): the
/// library is its only writer, so it can never disagree with the foreign-key property
/// (ADR-0005 addendum 2); a public setter is a loader error. At Level 1 the library
/// never populates it (no hidden queries, CLAUDE.md §2) — it stays null until
/// Level 2's explicit/eager loading, which attaches to this same declaration.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ManyToOneAttribute : Attribute
{
    public ManyToOneAttribute(string foreignKeyProperty) => ForeignKeyProperty = foreignKeyProperty;

    /// <summary>Name of the property on this class that holds the foreign-key value.</summary>
    public string ForeignKeyProperty { get; }
}

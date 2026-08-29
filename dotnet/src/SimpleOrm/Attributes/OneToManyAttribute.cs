namespace SimpleOrm;

/// <summary>
/// Declares a one-to-many collection navigation: the foreign key lives on the
/// target entity, named by its property
/// (e.g. <c>[OneToMany(nameof(Transaction.UserId))] public IReadOnlyList&lt;Transaction&gt; Transactions</c>).
///
/// A navigation is inherently transient — never a column, never written by CRUD —
/// so it needs no <see cref="ColumnAttribute"/> or <see cref="IgnoreAttribute"/>.
/// It must not expose a public setter (<c>{ get; private set; } = [];</c>): the
/// library is its only writer (ADR-0005 addendum 2, <c>MAP-011</c>). The element
/// type comes from the property's <c>IEnumerable&lt;T&gt;</c> (<c>MAP-020</c>);
/// the named property must exist on the target (<c>MAP-021</c>). Declaration-only
/// until Level 2 milestone 3 (explicit/batch loading) populates it — no hidden
/// queries (§2).
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class OneToManyAttribute : Attribute
{
    public OneToManyAttribute(string targetForeignKeyProperty) => TargetForeignKeyProperty = targetForeignKeyProperty;

    /// <summary>Name of the property on the target (element) type that holds this entity's key.</summary>
    public string TargetForeignKeyProperty { get; }
}

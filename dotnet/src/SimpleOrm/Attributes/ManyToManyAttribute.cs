namespace SimpleOrm;

/// <summary>
/// Declares a many-to-many collection navigation resolved through an explicit
/// link entity — never inferred
/// (e.g. <c>[ManyToMany(typeof(UserRole))] public IReadOnlyList&lt;Role&gt; Roles</c>).
///
/// The link entity's <see cref="ForeignKeyAttribute"/> declarations (ADR-0005)
/// identify which of its properties reference each side; each side must be
/// referenced exactly once (<c>MAP-022</c>). The element type comes from the
/// property's <c>IEnumerable&lt;T&gt;</c> (<c>MAP-020</c>); no public setter
/// (<c>MAP-011</c>). Declaration-only until Level 2 loading populates it.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ManyToManyAttribute : Attribute
{
    public ManyToManyAttribute(Type through) => Through = through;

    /// <summary>The link entity joining this type to the element type.</summary>
    public Type Through { get; }
}

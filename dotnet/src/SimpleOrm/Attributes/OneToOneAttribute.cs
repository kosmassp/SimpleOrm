namespace SimpleOrm;

/// <summary>
/// Declares the inverse side of a one-to-one: a single navigation whose foreign
/// key lives on the target entity, named by its property
/// (e.g. <c>[OneToOne(nameof(UserProfile.UserId))] public UserProfile? Profile</c>).
/// Structurally a one-to-many constrained to at most one row — true 1:1 integrity
/// is the database's job (a unique index on the target's FK column); the
/// FK-holding side declares an ordinary <see cref="ManyToOneAttribute"/>.
///
/// Same rules as every navigation: transient, no public setter (<c>MAP-011</c>),
/// a single entity reference — not a collection (<c>MAP-020</c>) — and the named
/// property must exist on the target (<c>MAP-021</c>). Nothing loads until
/// requested (ADR-0019 add.1): explicit loading or an eager include; never on
/// access.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class OneToOneAttribute : Attribute
{
    public OneToOneAttribute(params string[] targetForeignKeyProperties)
        => TargetForeignKeyProperties = targetForeignKeyProperties;

    /// <summary>
    /// The properties on the target type holding this entity's key — one per key
    /// part, **in this entity's key order** (composite owners pass several,
    /// ADR-0019 add.1).
    /// </summary>
    public IReadOnlyList<string> TargetForeignKeyProperties { get; }
}

namespace SimpleOrm;

/// <summary>
/// Declares that this mapped column references another entity's primary key
/// (e.g. <c>[ForeignKey(typeof(User))]</c> on <c>UserId</c>). Declaration-only
/// metadata (ADR-0005): it changes no query behavior at Level 1. Level 2
/// relationship loading and Level 3 migration drafts build on it. Valid with or
/// without a matching <see cref="ManyToOneAttribute"/> navigation property.
/// </summary>
[AttributeUsage(AttributeTargets.Property)]
public sealed class ForeignKeyAttribute : Attribute
{
    public ForeignKeyAttribute(Type references) => References = references;

    /// <summary>The entity type whose primary key this column references.</summary>
    public Type References { get; }
}

namespace SimpleOrm;

/// <summary>
/// Owned value types (ADR-0030). On a <b>class</b>: this type is a value object
/// with no table, key, version, generated column, relationship, or nested owned
/// type of its own — never an entity, so loaders, the CLI, and SchemaGuard skip
/// it. On a <b>property</b> of such a type: the opt-in navigation whose
/// <c>[Column]</c> members are stored as columns of the owner's table, as
/// <c>&lt;prefix&gt;&lt;column&gt;</c>; a nullable navigation makes every member
/// column nullable and an all-NULL row leaves it null. Both are required: the
/// class declares what the type is, the property declares that it is mapped.
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Property)]
public sealed class OwnedAttribute : Attribute
{
    /// <summary>
    /// Column prefix for the members. Null (the default) derives it from the
    /// navigation's name through the naming convention plus <c>_</c>
    /// (<c>Address</c> → <c>address_</c>); an empty string disables prefixing.
    /// </summary>
    public string? Prefix { get; set; }
}

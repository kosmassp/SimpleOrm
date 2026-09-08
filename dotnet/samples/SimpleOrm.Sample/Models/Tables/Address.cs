namespace SimpleOrm.Sample.Models;

/// <summary>
/// The owned-type fixture (ADR-0030): a value object with no table of its own,
/// stored as columns of <see cref="UserProfile"/> under the <c>address_</c>
/// prefix (<c>address_street</c>, <c>address_city</c>, <c>address_postal_code</c>).
/// Only <c>[Column]</c> members: no key, version, relationships, or nested owned types.
/// The class-level <c>[Owned]</c> is what keeps it out of the entity set.
/// </summary>
[Owned]
public sealed class Address
{
    [Column]
    public string Street { get; set; } = string.Empty;

    [Column]
    public string City { get; set; } = string.Empty;

    [Column]
    public string? PostalCode { get; set; }
}

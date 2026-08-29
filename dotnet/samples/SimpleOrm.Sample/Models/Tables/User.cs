namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>users</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("users")]
[Index(nameof(Email), Unique = true)]
public sealed class User : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public required string Name { get; set; }

    [Column]
    public required string Email { get; set; }

    /// <summary>Added by migration V0002; backfilled from <see cref="Name"/> for pre-existing rows.</summary>
    [Column]
    public string? DisplayName { get; set; }
}

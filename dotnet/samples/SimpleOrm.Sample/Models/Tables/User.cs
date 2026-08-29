namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>users</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("users")]
[Index(nameof(Email), Unique = true)]
[Index(nameof(DisplayName))]
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

    /// <summary>Populated only by the library (Level 2 milestone 3 loading); never a column, never written.</summary>
    [OneToMany(nameof(Transaction.UserId))]
    public IReadOnlyList<Transaction> Transactions { get; private set; } = [];

    /// <summary>Resolved through the <see cref="UserRole"/> link — declared, never inferred (ADR-0019).</summary>
    [ManyToMany(typeof(UserRole))]
    public IReadOnlyList<Role> Roles { get; private set; } = [];

    /// <summary>The inverse side of the 1:1 — the FK (with its unique index) lives on <see cref="UserProfile"/>.</summary>
    [OneToOne(nameof(UserProfile.UserId))]
    public UserProfile? Profile { get; private set; }
}

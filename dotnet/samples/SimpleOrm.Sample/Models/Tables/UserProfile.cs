namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>user_profiles</c> (STRICT) — the one-to-one fixture (ADR-0019 add.1):
/// this side holds the foreign key with a **unique index** (the database is what
/// makes a 1:1 a 1:1), so it declares an ordinary <see cref="ManyToOneAttribute"/>;
/// the inverse single navigation lives on <see cref="Models.User"/>.
/// </summary>
[Table("user_profiles")]
[Index(nameof(UserId), Unique = true)]
public sealed class UserProfile : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    [ForeignKey(typeof(User))]
    public long UserId { get; set; }

    /// <summary>Populated only by the library (Level 2 milestone 3 loading).</summary>
    [ManyToOne(nameof(UserId))]
    public User? User { get; private set; }

    [Column]
    public string? Bio { get; set; }

    [Column]
    public string? AvatarUrl { get; set; }
}

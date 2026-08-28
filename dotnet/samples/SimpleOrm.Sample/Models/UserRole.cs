namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>user_roles</c> (STRICT). Composite key (<c>user_id</c>, <c>role_id</c>),
/// neither part database-generated — the fixture for composite-key support
/// (CLAUDE.md §7.4). Relationships are FK columns only at Level 1; navigation
/// properties are Level 2. <c>CreatedAtUtc</c> from the base doubles as the
/// assignment timestamp.
/// </summary>
[Table("user_roles")]
public sealed class UserRole : BaseModel
{
    [Key]
    [Column]
    [ForeignKey(typeof(User))]
    public long UserId { get; set; }

    [Key]
    [Column]
    [ForeignKey(typeof(Role))]
    public long RoleId { get; set; }

    /// <summary>Populated only by the library (Level 2 loading); no public setter, so it can never disagree with <see cref="UserId"/>.</summary>
    [ManyToOne(nameof(UserId))]
    public User? User { get; private set; }

    /// <summary>Populated only by the library (Level 2 loading); no public setter, so it can never disagree with <see cref="RoleId"/>.</summary>
    [ManyToOne(nameof(RoleId))]
    public Role? Role { get; private set; }
}

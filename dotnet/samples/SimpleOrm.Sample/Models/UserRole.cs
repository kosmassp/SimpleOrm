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
    public long UserId { get; set; }

    [Key]
    [Column]
    public long RoleId { get; set; }
}

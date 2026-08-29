namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>roles</c> (STRICT). Key: <c>id</c>, database-generated.</summary>
[Table("roles")]
[Index(nameof(Name), Unique = true)]
public sealed class Role : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    /// <summary>Column renamed to <c>role_name</c> by migration V0004.</summary>
    [Column("role_name")]
    public required string Name { get; set; }

    /// <summary>The reverse side of <see cref="User.Roles"/>, through the same link (ADR-0019).</summary>
    [ManyToMany(typeof(UserRole))]
    public IReadOnlyList<User> Users { get; private set; } = [];
}

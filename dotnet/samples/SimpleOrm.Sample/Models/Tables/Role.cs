namespace SimpleOrm.Sample.Models;

/// <summary>Table <c>roles</c> (STRICT). Key: <c>id</c>, database-generated. Carries the audit trigger (ADR-0042): the link names the class, the class carries the SQL.</summary>
[Table("roles")]
[Index(nameof(Name), Unique = true)]
[Trigger(typeof(RoleAuditTrigger))]
public sealed class Role : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    /// <summary>Column renamed to <c>role_name</c> by migration V0004.</summary>
    /// <summary>Stored as <c>name</c> until V0004 renamed it: the declared rename the generator reads (Level 3 M5), harmless now that the history has it.</summary>
    [Column("role_name")]
    [RenamedFrom("name")]
    public required string Name { get; set; }

    /// <summary>The soft-delete flag (Level 3 M3, migration V0011): a delete sets it; reads filter on it explicitly, never implicitly.</summary>
    [Column("is_deleted")]
    [SoftDelete]
    public bool IsDeleted { get; set; }

    /// <summary>The reverse side of <see cref="User.Roles"/>, through the same link (ADR-0019).</summary>
    [ManyToMany(typeof(UserRole))]
    public IReadOnlyList<User> Users { get; private set; } = [];
}

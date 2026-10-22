namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>role_changes</c> (STRICT, migration V0013): the audit rows the
/// <see cref="RoleAuditTrigger"/> writes — one per updated role. Not a
/// <c>BaseModel</c>: the trigger is its only writer, so the audit hooks would
/// have nothing to stamp; the library only reads it. Key: <c>id</c>,
/// database-generated. <c>changed_at</c> is the §7.9 UTC text the trigger's
/// <c>{now}</c> token renders on every dialect.
/// </summary>
[Table("role_changes")]
public sealed class RoleChange
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public long RoleId { get; set; }

    [Column]
    public required string RoleName { get; set; }

    [Column("changed_at")]
    public DateTime ChangedAtUtc { get; set; }
}

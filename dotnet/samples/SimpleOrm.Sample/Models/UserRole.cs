namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>user_roles</c> (STRICT). Composite key: (<c>user_id</c>, <c>role_id</c>) —
/// the fixture entity for composite-key support (CLAUDE.md §7.4).
/// Relationships are FK columns only at Level 1; navigation properties are Level 2.
/// </summary>
public sealed record UserRole(
    long UserId,
    long RoleId,
    DateTime AssignedAtUtc);

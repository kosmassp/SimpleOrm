namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>users</c> (STRICT). Key: <c>id</c>, database-generated.
/// Dates are ISO-8601 UTC TEXT per CLAUDE.md §7.9.
/// </summary>
public sealed record User(
    long Id,
    string Name,
    string Email,
    DateTime CreatedAtUtc);

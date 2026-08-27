namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>transactions</c> (STRICT). Key: <c>id</c>, database-generated;
/// <c>user_id</c> references <c>users</c>. Planned to carry the version column for
/// the optimistic-concurrency fixture in milestone 7 (CLAUDE.md §7.16).
/// </summary>
public sealed record Transaction(
    long Id,
    long UserId,
    string Status,
    decimal Amount,
    DateTime CreatedAtUtc);

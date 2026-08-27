namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>transactions</c> (STRICT). Key: <c>id</c>, database-generated;
/// <c>user_id</c> references <c>users</c>. Carries the version column — the fixture
/// entity for optimistic concurrency (CLAUDE.md §7.16, milestone 7).
/// </summary>
[Table("transactions")]
public sealed class Transaction : BaseModel
{
    [Key]
    [Generated]
    public long Id { get; set; }

    public long UserId { get; set; }

    public TransactionStatus Status { get; set; }

    public decimal Amount { get; set; }

    [Version]
    public long Version { get; set; }
}

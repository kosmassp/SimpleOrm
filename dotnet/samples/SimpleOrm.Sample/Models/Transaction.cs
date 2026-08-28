namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>transactions</c> (STRICT). Key: <c>id</c>, database-generated;
/// <c>user_id</c> references <c>users</c>. Carries the version column — the fixture
/// entity for optimistic concurrency (CLAUDE.md §7.16, milestone 7).
/// </summary>
[Table("transactions")]
[Index(nameof(UserId))]
[Index(nameof(Status), nameof(CreatedAtUtc), Name = "ix_transactions_status_created", Descending = new[] { false, true })]
public sealed class Transaction : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    [ForeignKey(typeof(User))]
    public long UserId { get; set; }

    /// <summary>Populated only by the library (Level 2 loading); no public setter, so it can never disagree with <see cref="UserId"/>.</summary>
    [ManyToOne(nameof(UserId))]
    public User? User { get; private set; }

    [Column]
    public TransactionStatus Status { get; set; }

    [Column]
    public decimal Amount { get; set; }

    [Version]
    [Column]
    public long Version { get; set; }
}

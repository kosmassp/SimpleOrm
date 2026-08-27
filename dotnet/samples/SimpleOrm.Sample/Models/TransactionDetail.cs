namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>transaction_details</c> (STRICT). Key: <c>id</c>, database-generated;
/// <c>transaction_id</c> references <c>transactions</c>. The child side of the
/// json_group_array nesting pattern documented at milestone 4 (CLAUDE.md §7.10).
/// </summary>
[Table("transaction_details")]
public sealed class TransactionDetail : BaseModel
{
    [Key]
    [Generated]
    [Column]
    public long Id { get; set; }

    [Column]
    public long TransactionId { get; set; }

    [Column]
    public required string Description { get; set; }

    [Column]
    public int Quantity { get; set; }

    [Column]
    public decimal UnitPrice { get; set; }
}

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
    public long Id { get; set; }

    public long TransactionId { get; set; }

    public required string Description { get; set; }

    public int Quantity { get; set; }

    public decimal UnitPrice { get; set; }
}

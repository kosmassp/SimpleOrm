namespace SimpleOrm.Sample.Models;

/// <summary>
/// Table <c>transaction_details</c> (STRICT). Key: <c>id</c>, database-generated;
/// <c>transaction_id</c> references <c>transactions</c>. The child side of the
/// json_group_array nesting pattern documented at milestone 4 (CLAUDE.md §7.10).
/// </summary>
public sealed record TransactionDetail(
    long Id,
    long TransactionId,
    string Description,
    int Quantity,
    decimal UnitPrice);

namespace SimpleOrm.Sample.Models;

/// <summary>Stored as TEXT by name, matched case-insensitively on read (CLAUDE.md §7.9).</summary>
public enum TransactionStatus
{
    Pending,
    Completed,
    Cancelled,
}

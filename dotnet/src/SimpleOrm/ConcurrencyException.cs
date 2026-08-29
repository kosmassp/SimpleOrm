namespace SimpleOrm;

/// <summary>
/// Optimistic concurrency conflict (§7.16, code <c>CRUD-010</c>): an update or
/// delete carrying a version affected zero rows — someone else changed or removed
/// the row since it was loaded. The caller decides: reload and retry, or surface it.
/// </summary>
public sealed class ConcurrencyException(string target, string message)
    : Exception($"CRUD-010 {target}: {message}")
{
    public string Code => "CRUD-010";

    public string Target { get; } = target;
}

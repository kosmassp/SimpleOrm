using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// The seam between the provider-neutral core and a database provider.
/// Minimal and capability-based; members are added only when a milestone needs them
/// (see CLAUDE.md §7.25 for the full Level 1 member list).
/// </summary>
public interface IDialect
{
    /// <summary>Creates an unopened connection for the given connection string.</summary>
    DbConnection CreateConnection(string connectionString);
}

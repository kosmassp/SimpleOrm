using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// The shadow replayer's throwaway database, per dialect (ADR-0043) — a tooling
/// contract beside <see cref="IDialect"/>, never consulted at runtime. SQLite's is
/// a temp file; SQL Server's and PostgreSQL's create <c>simpleorm_shadow_&lt;hex&gt;</c>
/// on the admin connection the CLI's <c>--db</c> names and drop it afterwards,
/// failure included — the contract the live-test fixtures already follow.
/// </summary>
public interface IShadowHost
{
    /// <summary>Creates the throwaway database and returns the connection string that opens it.</summary>
    Task<string> ProvisionAsync(string? adminConnectionString, CancellationToken ct);

    /// <summary>Drops what <see cref="ProvisionAsync"/> created; safe to call when nothing was.</summary>
    Task DropAsync(CancellationToken ct);

    /// <summary>
    /// Reads a stored trigger back to its declaration where the dialect's catalog
    /// is lossless (SQLite's text as submitted; PostgreSQL's reconstructed header
    /// and verbatim body) — the shadow's self-check against the step-derived
    /// snapshot. Null where it is not: SQL Server renders <c>{new: cols}</c> as
    /// <c>inserted</c>, and the column list is gone.
    /// </summary>
    ITriggerReadBack? TriggerReadBack { get; }
}

/// <summary>Parses a dialect's stored trigger text back to the neutral declaration (the inverse of <see cref="IDialect.CreateTriggerSql"/>).</summary>
public interface ITriggerReadBack
{
    /// <summary>The declaration stored under <paramref name="triggerName"/>, or null when absent or when <paramref name="note"/> says why it could not be read (hand-written).</summary>
    TriggerSnapshot? Read(DbConnection connection, string triggerName, out string? note);
}

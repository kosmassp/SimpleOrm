using System.Data.Common;
using System.Reflection;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>
/// The SQLite shadow database (ADR-0043 <see cref="IShadowHost"/>): a temp file,
/// deleted afterwards — the throwaway the shadow replayer used before every
/// dialect had one. Triggers read back from <c>sqlite_master</c>, which keeps the
/// text as submitted.
/// </summary>
public sealed class SqliteShadowHost : IShadowHost, ITriggerReadBack
{
    private string? _file;

    public Task<string> ProvisionAsync(string? adminConnectionString, CancellationToken ct)
    {
        _file = Path.Combine(Path.GetTempPath(), "simpleorm-shadow-" + Guid.NewGuid().ToString("n") + ".db");
        return Task.FromResult($"Data Source={_file}");
    }

    public Task DropAsync(CancellationToken ct)
    {
        if (_file is null)
        {
            return Task.CompletedTask;
        }

        SqliteConnection.ClearAllPools();
        TryDelete(_file);
        TryDelete(_file + "-wal");
        TryDelete(_file + "-shm");
        _file = null;
        return Task.CompletedTask;
    }

    public ITriggerReadBack? TriggerReadBack => this;

    public TriggerSnapshot? Read(DbConnection connection, string triggerName, out string? note)
        => SqliteTriggerDdl.Read((SqliteConnection)connection, triggerName, out note);

    private static void TryDelete(string path)
    {
        try
        {
            if (File.Exists(path))
            {
                File.Delete(path);
            }
        }
        catch (IOException)
        {
            // A straggling handle on Windows; the temp file is harmless.
        }
    }
}

/// <summary>
/// The shadow replayer on SQLite (ADR-0017): <see cref="SchemaShadow"/> with the
/// SQLite dialect and its temp-file host — kept as the entry point the tests and
/// the pre-ADR-0043 callers know.
/// </summary>
public static class SqliteShadow
{
    public static Task<SchemaShadow.Result> RebuildSnapshotsAsync(
        Assembly assembly,
        string? migrationsNamespace,
        string migrationsDir,
        long fromVersion = 0,
        long toVersion = long.MaxValue,
        CancellationToken ct = default)
        => SchemaShadow.RebuildSnapshotsAsync(
            assembly, migrationsNamespace, migrationsDir, new SqliteDialect(), new SqliteShadowHost(),
            adminConnectionString: null, fromVersion, toVersion, ct);
}

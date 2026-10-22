using System.Data.Common;
using Npgsql;

namespace SimpleOrm.Postgres;

/// <summary>
/// The PostgreSQL shadow database (ADR-0043 <see cref="IShadowHost"/>): the
/// live-test contract reused — <c>simpleorm_shadow_&lt;hex&gt;</c> created on the
/// admin connection the CLI's <c>--db</c> names (CREATEDB rights), dropped
/// afterwards with <c>force</c>, failure included. Triggers read back from the
/// catalog: the header reconstructed from <c>pg_trigger</c>, the body verbatim
/// from <c>pg_proc.prosrc</c> — lossless, so the shadow's self-check runs here.
/// </summary>
public sealed class PostgresShadowHost : IShadowHost, ITriggerReadBack
{
    private string? _admin;
    private string? _database;

    public async Task<string> ProvisionAsync(string? adminConnectionString, CancellationToken ct)
    {
        if (string.IsNullOrWhiteSpace(adminConnectionString))
        {
            throw new SimpleOrmException(
                "CLI", "shadow", "shadow on PostgreSQL needs --db <admin connection string> with CREATEDB rights (it creates and drops simpleorm_shadow_<hex>)");
        }

        _admin = adminConnectionString;
        _database = $"simpleorm_shadow_{Guid.NewGuid():N}";
        using var connection = new NpgsqlConnection(_admin);
        await connection.OpenAsync(ct).ConfigureAwait(false);
        using var command = connection.CreateCommand();
        command.CommandText = $"create database \"{_database}\"";
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        return new NpgsqlConnectionStringBuilder(_admin) { Database = _database }.ConnectionString;
    }

    public async Task DropAsync(CancellationToken ct)
    {
        if (_admin is null || _database is null)
        {
            return;
        }

        NpgsqlConnection.ClearAllPools();
        using var connection = new NpgsqlConnection(_admin);
        await connection.OpenAsync(ct).ConfigureAwait(false);
        using var command = connection.CreateCommand();
        command.CommandText = $"drop database if exists \"{_database}\" with (force)";
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        _database = null;
    }

    public ITriggerReadBack? TriggerReadBack => this;

    public TriggerSnapshot? Read(DbConnection connection, string triggerName, out string? note)
        => PostgresTriggerDdl.Read(connection, triggerName, out note);
}

using Microsoft.Data.SqlClient;

namespace SimpleOrm.SqlServer;

/// <summary>
/// The SQL Server shadow database (ADR-0043 <see cref="IShadowHost"/>): the
/// live-test contract reused — <c>simpleorm_shadow_&lt;hex&gt;</c> created on the
/// admin connection the CLI's <c>--db</c> names, dropped afterwards (single-user
/// with rollback first), failure included. No trigger read-back: the dialect
/// renders <c>{new: cols}</c> as <c>inserted</c>, so the stored text has lost the
/// column list; the shadow snapshots triggers from the step alone here.
/// </summary>
public sealed class SqlServerShadowHost : IShadowHost
{
    private string? _admin;
    private string? _database;

    public async Task<string> ProvisionAsync(string? adminConnectionString, CancellationToken ct)
    {
        if (string.IsNullOrWhiteSpace(adminConnectionString))
        {
            throw new SimpleOrmException(
                "CLI", "shadow", "shadow on SQL Server needs --db <admin connection string> with CREATE DATABASE rights (it creates and drops simpleorm_shadow_<hex>)");
        }

        _admin = adminConnectionString;
        _database = $"simpleorm_shadow_{Guid.NewGuid():N}";
        using var connection = new SqlConnection(_admin);
        await connection.OpenAsync(ct).ConfigureAwait(false);
        using var command = connection.CreateCommand();
        command.CommandText = $"create database [{_database}]";
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        return new SqlConnectionStringBuilder(_admin) { InitialCatalog = _database }.ConnectionString;
    }

    public async Task DropAsync(CancellationToken ct)
    {
        if (_admin is null || _database is null)
        {
            return;
        }

        SqlConnection.ClearAllPools();
        using var connection = new SqlConnection(_admin);
        await connection.OpenAsync(ct).ConfigureAwait(false);
        using var command = connection.CreateCommand();
        command.CommandText =
            $"if db_id(N'{_database}') is not null begin "
            + $"alter database [{_database}] set single_user with rollback immediate; "
            + $"drop database [{_database}]; end";
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        _database = null;
    }

    public ITriggerReadBack? TriggerReadBack => null;
}

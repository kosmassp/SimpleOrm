using Microsoft.Data.SqlClient;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// A real SQL Server test database (ADR-0024): the LocalDB default instance, one
/// database per fixture, dropped afterwards — the SQL Server analog of the
/// temp-file SQLite database (ADR-0003; never a mock). Tests carry
/// <see cref="SqlServerFactAttribute"/> and skip as a group where no LocalDB
/// instance answers, so CI still needs only the .NET SDK.
/// </summary>
public sealed class SqlServerFixture : IAsyncLifetime
{
    // The live-testing contract (spec/dialects.md "Testing contract"): the admin
    // connection string comes from SIMPLEORM_SQLSERVER; without it, the LocalDB
    // default instance. Encrypt=false there: Microsoft.Data.SqlClient defaults to
    // Encrypt=Mandatory since 4.0, which refuses LocalDB's self-signed certificate;
    // the instance is in-process on this machine, nothing crosses a wire.
    private static readonly string MasterConnectionString =
        Environment.GetEnvironmentVariable("SIMPLEORM_SQLSERVER") is { Length: > 0 } configured
            ? configured
            : @"Server=(localdb)\MSSQLLocalDB;Database=master;Integrated Security=true;Encrypt=false;Connect Timeout=60";

    // Probed once per run; the first open also spins the LocalDB instance up.
    private static readonly Lazy<bool> Probe = new(() =>
    {
        try
        {
            using var connection = new SqlConnection(MasterConnectionString);
            connection.Open();
            return true;
        }
        catch (Exception)
        {
            return false;
        }
    });

    private readonly string _database = $"simpleorm_test_{Guid.NewGuid():N}";

    public static bool Available => Probe.Value;

    /// <summary>The admin connection string pointed at this fixture's database.</summary>
    public string ConnectionString =>
        new SqlConnectionStringBuilder(MasterConnectionString) { InitialCatalog = _database }.ConnectionString;

    public async Task InitializeAsync()
    {
        if (!Available)
        {
            return;
        }

        using var connection = new SqlConnection(MasterConnectionString);
        await connection.OpenAsync();
        using var command = connection.CreateCommand();
        command.CommandText = $"create database [{_database}]";
        await command.ExecuteNonQueryAsync();
    }

    public async Task DisposeAsync()
    {
        if (!Available)
        {
            return;
        }

        SqlConnection.ClearAllPools();
        using var connection = new SqlConnection(MasterConnectionString);
        await connection.OpenAsync();
        using var command = connection.CreateCommand();
        command.CommandText =
            $"if db_id(N'{_database}') is not null begin "
            + $"alter database [{_database}] set single_user with rollback immediate; "
            + $"drop database [{_database}]; end";
        await command.ExecuteNonQueryAsync();
    }
}

[CollectionDefinition(Name)]
public sealed class SqlServerCollection : ICollectionFixture<SqlServerFixture>
{
    public const string Name = "sqlserver";
}

/// <summary>A fact that runs only where a SQL Server LocalDB instance answers.</summary>
public sealed class SqlServerFactAttribute : FactAttribute
{
    public SqlServerFactAttribute()
    {
        if (!SqlServerFixture.Available)
        {
            Skip = "SQL Server LocalDB is not available on this machine";
        }
    }
}

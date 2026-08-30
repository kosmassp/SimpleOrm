using Npgsql;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// A real PostgreSQL test database (ADR-0025): one database per fixture on a
/// local server, dropped afterwards — the Postgres analog of the temp-file
/// SQLite database (ADR-0003; never a mock). The admin connection comes from
/// the <c>SIMPLEORM_POSTGRES</c> environment variable (a connection string with
/// CREATEDB rights) or falls back to the conventional local dev server
/// (localhost:5432, postgres/postgres). Tests carry
/// <see cref="PostgresFactAttribute"/> and skip as a group where no server
/// answers, so CI still needs only the .NET SDK.
/// </summary>
public sealed class PostgresFixture : IAsyncLifetime
{
    private static readonly string AdminConnectionString =
        Environment.GetEnvironmentVariable("SIMPLEORM_POSTGRES")
        ?? "Host=localhost;Port=5432;Username=postgres;Password=postgres;Database=postgres;Timeout=5";

    // Probed once per run.
    private static readonly Lazy<bool> Probe = new(() =>
    {
        try
        {
            using var connection = new NpgsqlConnection(AdminConnectionString);
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

    public string ConnectionString =>
        new NpgsqlConnectionStringBuilder(AdminConnectionString) { Database = _database }.ConnectionString;

    public async Task InitializeAsync()
    {
        if (!Available)
        {
            return;
        }

        using var connection = new NpgsqlConnection(AdminConnectionString);
        await connection.OpenAsync();
        using var command = connection.CreateCommand();
        command.CommandText = $"create database \"{_database}\"";
        await command.ExecuteNonQueryAsync();
    }

    public async Task DisposeAsync()
    {
        if (!Available)
        {
            return;
        }

        NpgsqlConnection.ClearAllPools();
        using var connection = new NpgsqlConnection(AdminConnectionString);
        await connection.OpenAsync();
        using var command = connection.CreateCommand();
        command.CommandText = $"drop database if exists \"{_database}\" with (force)";
        await command.ExecuteNonQueryAsync();
    }
}

[CollectionDefinition(Name)]
public sealed class PostgresCollection : ICollectionFixture<PostgresFixture>
{
    public const string Name = "postgres";
}

/// <summary>A fact that runs only where a PostgreSQL server answers.</summary>
public sealed class PostgresFactAttribute : FactAttribute
{
    public PostgresFactAttribute()
    {
        if (!PostgresFixture.Available)
        {
            Skip = "PostgreSQL is not available (set SIMPLEORM_POSTGRES or run a local server on 5432)";
        }
    }
}

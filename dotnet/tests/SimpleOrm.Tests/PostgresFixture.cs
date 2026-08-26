using Npgsql;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Provides the connection string for a real PostgreSQL test database
/// (ADR-0002: local server or CI service container; never a mock).
/// Creates the test database on first use.
/// </summary>
public sealed class PostgresFixture : IAsyncLifetime
{
    private const string DefaultConnectionString =
        "Host=localhost;Port=5432;Username=postgres;Database=simpleorm_test";

    public string ConnectionString { get; } =
        Environment.GetEnvironmentVariable("ORM_TEST_CONNECTION") ?? DefaultConnectionString;

    public async Task InitializeAsync()
    {
        var builder = new NpgsqlConnectionStringBuilder(ConnectionString);
        var databaseName = builder.Database
            ?? throw new InvalidOperationException("ORM_TEST_CONNECTION must specify a database.");

        var adminBuilder = new NpgsqlConnectionStringBuilder(ConnectionString) { Database = "postgres" };
        await using var admin = new NpgsqlConnection(adminBuilder.ConnectionString);
        await admin.OpenAsync();

        await using var exists = new NpgsqlCommand("select 1 from pg_database where datname = @name", admin);
        exists.Parameters.AddWithValue("name", databaseName);
        if (await exists.ExecuteScalarAsync() is null)
        {
            // CREATE DATABASE cannot be parameterized; the name comes from test
            // configuration, not user data, and is quoted as an identifier.
            var quoted = "\"" + databaseName.Replace("\"", "\"\"") + "\"";
            await using var create = new NpgsqlCommand($"create database {quoted}", admin);
            await create.ExecuteNonQueryAsync();
        }
    }

    public Task DisposeAsync() => Task.CompletedTask;
}

[CollectionDefinition(Name)]
public sealed class PostgresCollection : ICollectionFixture<PostgresFixture>
{
    public const string Name = "postgres";
}

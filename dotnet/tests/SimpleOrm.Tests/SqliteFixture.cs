using Microsoft.Data.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Provides the connection string for a real SQLite test database (ADR-0003: a
/// temp-file database per fixture, deleted afterwards; never a mock).
/// </summary>
public sealed class SqliteFixture : IAsyncLifetime
{
    private readonly string _databasePath =
        Path.Combine(Path.GetTempPath(), $"simpleorm_test_{Guid.NewGuid():N}.db");

    public string ConnectionString => $"Data Source={_databasePath}";

    public Task InitializeAsync() => Task.CompletedTask;

    public Task DisposeAsync()
    {
        // Pooled connections keep the file handle open; clear them before deleting.
        SqliteConnection.ClearAllPools();
        File.Delete(_databasePath);
        return Task.CompletedTask;
    }
}

[CollectionDefinition(Name)]
public sealed class SqliteCollection : ICollectionFixture<SqliteFixture>
{
    public const string Name = "sqlite";
}

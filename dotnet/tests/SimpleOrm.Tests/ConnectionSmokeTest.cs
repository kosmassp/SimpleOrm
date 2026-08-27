using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class ConnectionSmokeTest(SqliteFixture fixture)
{
    [Fact]
    public async Task Dialect_connection_reaches_real_sqlite()
    {
        var dialect = new SqliteDialect();
        await using var connection = dialect.CreateConnection(fixture.ConnectionString);
        await connection.OpenAsync();

        await using var command = connection.CreateCommand();
        command.CommandText = "select 1";
        var result = await command.ExecuteScalarAsync();

        Assert.Equal(1L, Assert.IsType<long>(result));
    }

    [Fact]
    public async Task Bundled_sqlite_supports_returning_and_strict_tables()
    {
        // ADR-0003 relies on RETURNING (3.35+) and STRICT tables (3.37+).
        var dialect = new SqliteDialect();
        await using var connection = dialect.CreateConnection(fixture.ConnectionString);
        await connection.OpenAsync();

        await using var command = connection.CreateCommand();
        command.CommandText = "select sqlite_version()";
        var version = Version.Parse(Assert.IsType<string>(await command.ExecuteScalarAsync()));

        Assert.True(version >= new Version(3, 37), $"bundled SQLite {version} is older than 3.37");
    }
}

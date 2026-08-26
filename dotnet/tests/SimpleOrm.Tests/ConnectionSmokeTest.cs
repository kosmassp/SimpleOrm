using SimpleOrm.Postgres;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(PostgresCollection.Name)]
public sealed class ConnectionSmokeTest(PostgresFixture fixture)
{
    [Fact]
    public async Task Dialect_connection_reaches_real_postgres()
    {
        var dialect = new PostgresDialect();
        await using var connection = dialect.CreateConnection(fixture.ConnectionString);
        await connection.OpenAsync();

        await using var command = connection.CreateCommand();
        command.CommandText = "select 1";
        var result = await command.ExecuteScalarAsync();

        Assert.Equal(1, Assert.IsType<int>(result));
    }
}

using System.Globalization;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Round-trips a sample model through a real STRICT table with hand-written SQL.
/// The library has no mapper yet (milestone 4); this proves the sample schema
/// conventions (STRICT, snake_case, ISO-8601 UTC dates) work on the bundled SQLite.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class SampleModelRoundTripTest(SqliteFixture fixture)
{
    [Fact]
    public async Task User_round_trips_through_a_strict_table()
    {
        var dialect = new SqliteDialect();
        await using var connection = dialect.CreateConnection(fixture.ConnectionString);
        await connection.OpenAsync();

        await using (var create = connection.CreateCommand())
        {
            create.CommandText =
                """
                create table if not exists users (
                    id          INTEGER PRIMARY KEY,
                    name        TEXT NOT NULL,
                    email       TEXT NOT NULL,
                    created_at  TEXT NOT NULL
                ) STRICT
                """;
            await create.ExecuteNonQueryAsync();
        }

        var createdAt = new DateTime(2026, 8, 27, 12, 0, 0, DateTimeKind.Utc);

        long id;
        await using (var insert = connection.CreateCommand())
        {
            insert.CommandText =
                "insert into users (name, email, created_at) values (@name, @email, @createdAt) returning id";
            AddParameter(insert, "@name", "Ada");
            AddParameter(insert, "@email", "ada@example.com");
            AddParameter(insert, "@createdAt", createdAt.ToString("o", CultureInfo.InvariantCulture));
            id = Assert.IsType<long>(await insert.ExecuteScalarAsync());
        }

        await using var select = connection.CreateCommand();
        select.CommandText = "select id, name, email, created_at from users where id = @id";
        AddParameter(select, "@id", id);
        await using var reader = await select.ExecuteReaderAsync();
        Assert.True(await reader.ReadAsync());

        var user = new User(
            Id: reader.GetInt64(0),
            Name: reader.GetString(1),
            Email: reader.GetString(2),
            CreatedAtUtc: DateTime.Parse(reader.GetString(3), CultureInfo.InvariantCulture,
                DateTimeStyles.RoundtripKind));

        Assert.Equal(new User(id, "Ada", "ada@example.com", createdAt), user);
        Assert.Equal(DateTimeKind.Utc, user.CreatedAtUtc.Kind);
    }

    private static void AddParameter(System.Data.Common.DbCommand command, string name, object value)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = value;
        command.Parameters.Add(parameter);
    }
}

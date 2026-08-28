using SimpleOrm.Sample;
using SimpleOrm.Sqlite;

namespace SimpleOrm.Tests;

/// <summary>
/// Session setup for integration tests: opens a Db on the fixture database,
/// creates the sample schema, and clears all rows. Domain queries and commands
/// come from the sample registry (SimpleOrm.Sample.Queries/Commands); only
/// test-specific plumbing lives here.
/// </summary>
internal static class TestDb
{
    public static readonly DbOptions Options = new() { Dialect = new SqliteDialect() };

    public static readonly DateTime SeedTime = new(2026, 8, 28, 9, 30, 0, DateTimeKind.Utc);

    public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    public static async Task<Db> OpenAsync(SqliteFixture fixture, CancellationToken ct = default)
    {
        var db = await Db.OpenAsync(fixture.ConnectionString, Options, ct);
        foreach (var create in Schema.All)
        {
            await db.ExecuteAsync(create, EmptyArgs.Value, ct);
        }

        foreach (var table in new[] { "transaction_details", "transactions", "user_roles", "roles", "users" })
        {
            Command<EmptyArgs> clear = Query.Inline("delete from " + table);
            await db.ExecuteAsync(clear, EmptyArgs.Value, ct);
        }

        return db;
    }

    public static Task<int> InsertUserAsync(Db db, string name, string email, CancellationToken ct = default)
        => db.ExecuteAsync(Commands.InsertUser, new InsertUserArgs(name, email, SeedTime), ct);
}

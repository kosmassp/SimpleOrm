using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;

namespace SimpleOrm.Tests;

/// <summary>
/// Session setup for integration tests: opens a Db on the fixture database, creates
/// the whole schema — tables, indexes, and the view — from entity metadata
/// (ADR-0011 + ADR-0008 addendum 3), and clears all rows. No hand-written DDL.
/// </summary>
internal static class TestDb
{
    public static readonly DbOptions Options = new() { Dialect = new SqliteDialect() };

    public static readonly DateTime SeedTime = new(2026, 8, 28, 9, 30, 0, DateTimeKind.Utc);

    public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    public static async Task<Db> OpenAsync(SqliteFixture fixture, CancellationToken ct = default)
    {
        var db = await Db.OpenAsync(fixture.ConnectionString, Options, ct);

        await db.CreateTableAsync<User>(ct);
        await db.CreateTableAsync<Role>(ct);
        await db.CreateTableAsync<UserRole>(ct);
        await db.CreateTableAsync<Transaction>(ct);
        await db.CreateTableAsync<TransactionDetail>(ct);
        await db.CreateViewAsync<UserTransactionTotal>(ct);

        foreach (var table in new[] { "transaction_details", "transactions", "user_roles", "roles", "users" })
        {
            Command<EmptyArgs> clear = Query.Inline("delete from " + table);
            await db.ExecuteAsync(clear, EmptyArgs.Value, ct);
        }

        return db;
    }

    public static async Task<User> InsertUserAsync(Db db, string name, string email, CancellationToken ct = default)
    {
        var user = new User { Name = name, Email = email, CreatedAtUtc = SeedTime };
        await db.InsertAsync(user, ct);
        return user;
    }
}

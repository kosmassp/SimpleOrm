using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;

namespace SimpleOrm.Tests;

/// <summary>Shared session setup and query registry for the milestone 3 tests.</summary>
internal static class TestDb
{
    public static readonly DbOptions Options = new() { Dialect = new SqliteDialect() };

    public static async Task<Db> OpenAsync(SqliteFixture fixture, CancellationToken ct = default)
    {
        var db = await Db.OpenAsync(fixture.ConnectionString, Options, ct);
        await db.ExecuteAsync(Schema.CreateUsers, EmptyArgs.Value, ct);
        await db.ExecuteAsync(Schema.ClearUsers, EmptyArgs.Value, ct);
        return db;
    }

    public static class Schema
    {
        public static readonly Command<EmptyArgs> CreateUsers = Query.Inline(
            """
            create table if not exists users (
                id          INTEGER PRIMARY KEY,
                name        TEXT NOT NULL,
                email       TEXT NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            """);

        public static readonly Command<EmptyArgs> ClearUsers = Query.Inline("delete from users");
    }

    public static class Queries
    {
        public static readonly Query<EmptyArgs, User> AllUsers = Query.Inline(
            "select id, name, email, created_at, updated_at from users order by id");

        public static readonly Query<UserByIdArgs, User> UserById = Query.Inline(
            "select id, name, email, created_at, updated_at from users where id = @Id");

        public static readonly Query<UsersByIdsArgs, User> UsersByIds = Query.Inline(
            "select id, name, email, created_at, updated_at from users where id in (@Ids) order by id");

        public static readonly Query<UserByEmailArgs, User> UserByEmail = Query.Embedded("Users/GetUserByEmail.sql");

        public static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

        public static readonly Query<EmptyArgs, UserEmailRow> EmailRows = Query.Inline(
            "select id, email from users order by id");
    }

    public static class Commands
    {
        public static readonly Command<InsertUserArgs> InsertUser = Query.Inline(
            "insert into users (name, email, created_at) values (@Name, @Email, @CreatedAt)");
    }

    public static readonly DateTime SeedTime = new(2026, 8, 28, 9, 30, 0, DateTimeKind.Utc);

    public static Task<int> InsertUserAsync(Db db, string name, string email, CancellationToken ct = default)
        => db.ExecuteAsync(Commands.InsertUser, new InsertUserArgs(name, email, SeedTime), ct);
}

public sealed record UserByIdArgs(long Id);

public sealed record UsersByIdsArgs(IReadOnlyList<long> Ids);

public sealed record UserByEmailArgs(string Email);

public sealed record InsertUserArgs(string Name, string Email, DateTime CreatedAt);

public sealed record UserEmailRow(long Id, string Email);

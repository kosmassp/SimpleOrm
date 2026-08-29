using SimpleOrm.Sample;
using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class DbQueryTests(SqliteFixture fixture)
{
    private sealed record EmailArgs(string Email);

    // The one test of the optional embedded mechanism; everything else is inline (ADR-0009).
    private static readonly Query<EmailArgs, User> UserByEmailEmbedded =
        Query.Embedded("Users/GetUserByEmail.sql");

    private static readonly Query<EmptyArgs, User> AllUsersInline = Query.Inline(
        "select id, name, email, display_name, created_at, updated_at from users order by id");

    [Fact]
    public async Task Query_maps_entities_through_their_entity_map()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        var users = await db.QueryAllAsync<User>(CancellationToken.None);

        Assert.Equal(2, users.Count);
        Assert.Equal("Ada", users[0].Name);
        Assert.Equal(TestDb.SeedTime, users[0].CreatedAtUtc);            // [Column("created_at")] override applied
        Assert.Equal(DateTimeKind.Utc, users[0].CreatedAtUtc.Kind);      // ISO-8601 Z round trip
        Assert.Null(users[0].UpdatedAtUtc);
    }

    [Fact]
    public async Task Scalar_and_dto_results_share_the_pipeline()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        var count = await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal(1L, count);

        Query<EmptyArgs, UserEmailRow> emailRows = Query.Inline("select id, email from users order by id");
        var rows = await db.QueryAsync(emailRows, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal("ada@example.com", Assert.Single(rows).Email);
    }

    [Fact]
    public async Task QuerySingle_enforces_row_counts_with_codes()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var none = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QuerySingleAsync(AllUsersInline, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal("QRY-001", none.Code);

        Assert.Null(await db.GetOrDefaultAsync<User>(999, CancellationToken.None));

        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        var many = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QuerySingleAsync(AllUsersInline, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal("QRY-002", many.Code);
    }

    [Fact]
    public async Task Stream_yields_rows_lazily()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        var names = new List<string>();
        await foreach (var user in db.StreamAsync(AllUsersInline, EmptyArgs.Value, CancellationToken.None))
        {
            names.Add(user.Name);
        }

        Assert.Equal(["Ada", "Grace"], names);
    }

    [Fact]
    public async Task Embedded_sql_stays_supported_as_the_optional_form()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        var user = await db.QuerySingleAsync(
            UserByEmailEmbedded, new EmailArgs("ada@example.com"), CancellationToken.None);

        Assert.Equal("Ada", user.Name);
    }

    [Fact]
    public async Task Result_column_with_no_mapped_property_is_MAP001()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        Query<EmptyArgs, User> bad = Query.Inline("select id, name, email, created_at, updated_at, 42 as mystery from users");

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(bad, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal("MAP-001", exception.Code);
        Assert.Contains("mystery", exception.Message);
    }
}

public sealed record UserEmailRow(long Id, string Email);

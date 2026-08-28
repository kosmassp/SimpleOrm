using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class DbQueryTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Query_maps_entities_through_their_entity_map()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        var users = await db.QueryAsync(TestDb.Queries.AllUsers, EmptyArgs.Value, CancellationToken.None);

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

        var count = await db.QuerySingleAsync(TestDb.Queries.CountUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal(1L, count);

        var rows = await db.QueryAsync(TestDb.Queries.EmailRows, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal("ada@example.com", Assert.Single(rows).Email);
    }

    [Fact]
    public async Task QuerySingle_enforces_row_counts_with_codes()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var none = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QuerySingleAsync(TestDb.Queries.AllUsers, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal("QRY-001", none.Code);

        Assert.Null(await db.QuerySingleOrDefaultAsync(
            TestDb.Queries.UserById, new UserByIdArgs(999), CancellationToken.None));

        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        var many = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QuerySingleAsync(TestDb.Queries.AllUsers, EmptyArgs.Value, CancellationToken.None));
        Assert.Equal("QRY-002", many.Code);
    }

    [Fact]
    public async Task Stream_yields_rows_lazily()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        var names = new List<string>();
        await foreach (var user in db.StreamAsync(TestDb.Queries.AllUsers, EmptyArgs.Value, CancellationToken.None))
        {
            names.Add(user.Name);
        }

        Assert.Equal(["Ada", "Grace"], names);
    }

    [Fact]
    public async Task Embedded_sql_resolves_from_the_registry_assembly()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        var user = await db.QuerySingleAsync(
            TestDb.Queries.UserByEmail, new UserByEmailArgs("ada@example.com"), CancellationToken.None);

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

using SimpleOrm.Sample;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class DbParameterTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Placeholder_without_property_is_PRM001()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Query<UserByIdArgs, long> bad = Query.Inline("select count(id) from users where id = @Nope");

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(bad, new UserByIdArgs(1), CancellationToken.None));
        Assert.Equal("PRM-001", exception.Code);
    }

    [Fact]
    public async Task Property_without_placeholder_is_PRM002()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Query<UserByIdArgs, long> bad = Query.Inline("select count(id) from users");

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(bad, new UserByIdArgs(1), CancellationToken.None));
        Assert.Equal("PRM-002", exception.Code);
    }

    [Fact]
    public async Task In_list_expands_to_parameterized_placeholders()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await TestDb.InsertUserAsync(db, "Edsger", "edsger@example.com");
        var all = await db.QueryAllAsync<SimpleOrm.Sample.Models.User>(CancellationToken.None);

        var picked = await db.QueryAsync(
            Queries.UsersByIds,
            new UsersByIdsArgs([all[0].Id, all[2].Id]),
            CancellationToken.None);

        Assert.Equal(["Ada", "Edsger"], picked.Select(u => u.Name));
    }

    [Fact]
    public async Task Empty_in_list_matches_no_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        var picked = await db.QueryAsync(
            Queries.UsersByIds, new UsersByIdsArgs([]), CancellationToken.None);

        Assert.Empty(picked);
    }

    [Fact]
    public async Task String_values_bind_as_parameters_not_text()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        // A value that would break the statement if it were concatenated.
        await TestDb.InsertUserAsync(db, "O'Brien; drop table users; --", "obrien@example.com");

        var user = await db.QuerySingleAsync(
            Queries.UserByEmail, new UserByEmailArgs("obrien@example.com"), CancellationToken.None);

        Assert.Equal("O'Brien; drop table users; --", user.Name);
    }
}

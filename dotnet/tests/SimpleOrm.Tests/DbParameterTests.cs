using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class DbParameterTests(SqliteFixture fixture)
{
    private sealed record ProbeArgs(long Id);

    private sealed record IdsArgs(IReadOnlyList<long> Ids);

    [Fact]
    public async Task Placeholder_without_property_is_PRM001()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Query<ProbeArgs, long> bad = Query.Inline("select count(id) from users where id = @Nope");

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(bad, new ProbeArgs(1), CancellationToken.None));
        Assert.Equal("PRM-001", exception.Code);
    }

    [Fact]
    public async Task Property_without_placeholder_is_PRM002()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Query<ProbeArgs, long> bad = Query.Inline("select count(id) from users");

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync(bad, new ProbeArgs(1), CancellationToken.None));
        Assert.Equal("PRM-002", exception.Code);
    }

    [Fact]
    public async Task In_list_expands_to_parameterized_placeholders()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await TestDb.InsertUserAsync(db, "Edsger", "edsger@example.com");
        var all = await db.QueryAllAsync<User>(CancellationToken.None);

        Query<IdsArgs, long> count = Query.Inline("select count(id) from users where id in (@Ids)");
        var picked = await db.QuerySingleAsync(
            count, new IdsArgs([all[0].Id, all[2].Id]), CancellationToken.None);

        Assert.Equal(2L, picked);
    }

    [Fact]
    public async Task Empty_in_list_matches_no_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        Query<IdsArgs, long> count = Query.Inline("select count(id) from users where id in (@Ids)");
        Assert.Equal(0L, await db.QuerySingleAsync(count, new IdsArgs([]), CancellationToken.None));
    }

    [Fact]
    public async Task String_values_bind_as_parameters_not_text()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        // A value that would break the statement if it were concatenated.
        await TestDb.InsertUserAsync(db, "O'Brien; drop table users; --", "obrien@example.com");

        var user = await db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Email), "obrien@example.com"))
            .SingleAsync(CancellationToken.None);

        Assert.Equal("O'Brien; drop table users; --", user.Name);
    }
}

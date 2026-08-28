using Xunit;

namespace SimpleOrm.Tests;

[Collection(SqliteCollection.Name)]
public sealed class DbTransactionTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Committed_transaction_persists()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        await using (var tx = await db.BeginAsync(CancellationToken.None))
        {
            await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
            await tx.CommitAsync(CancellationToken.None);
        }

        var count = await db.QuerySingleAsync(TestDb.Queries.CountUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal(1L, count);
    }

    [Fact]
    public async Task Disposed_uncommitted_transaction_rolls_back()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        await using (await db.BeginAsync(CancellationToken.None))
        {
            await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        }

        var count = await db.QuerySingleAsync(TestDb.Queries.CountUsers, EmptyArgs.Value, CancellationToken.None);
        Assert.Equal(0L, count);
    }

    [Fact]
    public async Task Second_begin_on_the_same_session_is_TX001()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await using var tx = await db.BeginAsync(CancellationToken.None);

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(() => db.BeginAsync(CancellationToken.None));
        Assert.Equal("TX-001", exception.Code);
    }
}

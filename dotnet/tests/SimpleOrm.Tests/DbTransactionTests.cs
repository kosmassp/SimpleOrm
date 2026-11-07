using System.Data.Common;
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

        var count = await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None);
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

        var count = await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None);
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

    private static readonly Command<EmptyArgs> EnforceForeignKeys = Query.Inline("pragma foreign_keys = on");

    private static readonly Command<EmptyArgs> CreateDeferredTables = Query.Inline(
        """
        create table if not exists commit_parents (id integer primary key) strict;
        create table if not exists commit_children (
            id integer primary key,
            parent_id integer not null references commit_parents (id) deferrable initially deferred) strict;
        delete from commit_children;
        """);

    // Checked only at COMMIT: the parent does not exist.
    private static readonly Command<EmptyArgs> InsertOrphan = Query.Inline("insert into commit_children (id, parent_id) values (1, 42)");

    private static readonly Query<EmptyArgs, long> CountChildren = Query.Inline("select count(id) from commit_children");

    [Fact]
    public async Task Refused_commit_ends_the_transaction_and_surfaces_the_driver_error()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.ExecuteAsync(EnforceForeignKeys, EmptyArgs.Value, CancellationToken.None);
        await db.ExecuteAsync(CreateDeferredTables, EmptyArgs.Value, CancellationToken.None);

        var scope = await db.BeginAsync(CancellationToken.None);
        await db.ExecuteAsync(InsertOrphan, EmptyArgs.Value, CancellationToken.None);
        await Assert.ThrowsAnyAsync<DbException>(() => scope.CommitAsync(CancellationToken.None));

        // The scope is completed: disposing it does nothing, and the session has no transaction left.
        await scope.DisposeAsync();
        await using (await db.BeginAsync(CancellationToken.None))
        {
        }

        Assert.Equal(0L, await db.QuerySingleAsync(CountChildren, EmptyArgs.Value, CancellationToken.None));
    }
}

using SimpleOrm.Sample.Dao;
using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The sample DAO layer end to end: instance DAOs over one injected session,
/// generic operations from the base, entity-specific reads on top.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class DaoLayerTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Daos_share_one_session_and_cover_the_flow()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var users = new UserDao(db);
        var transactions = new TransactionDao(db);

        var ada = new User { Name = "Ada", Email = "ada@example.com", CreatedAtUtc = TestDb.SeedTime };
        await users.InsertAsync(ada, CancellationToken.None);                       // generic, from the base

        var found = await users.GetByEmailAsync("ada@example.com", CancellationToken.None);
        Assert.Equal(ada.Id, found.Id);

        Assert.Equal("Ada", (await users.GetAsync(ada.Id, CancellationToken.None)).Name);
        Assert.Null(await users.GetOrDefaultAsync(999_999, CancellationToken.None));

        await transactions.InsertAsync(
            new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 42m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var pending = await transactions.GetByStatusAsync(TransactionStatus.Pending, CancellationToken.None);
        var tx = Assert.Single(pending);

        await transactions.SetStatusAsync(tx.Id, TransactionStatus.Completed, TestDb.SeedTime, CancellationToken.None);
        Assert.Equal(TransactionStatus.Completed, Assert.Single(
            await transactions.GetByUserAsync(ada.Id, CancellationToken.None)).Status);

        var days = await transactions.GetDailySalesAsync(TestDb.SeedTime.AddDays(-1), CancellationToken.None);
        Assert.Equal(42m, Assert.Single(days).TotalAmount);

        Assert.Single(await users.GetAllAsync(CancellationToken.None));            // generic, from the base
    }

    [Fact]
    public async Task Transactions_wrap_dao_work_through_the_shared_session()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var users = new UserDao(db);

        await using (await db.BeginAsync(CancellationToken.None))
        {
            await users.InsertAsync(
                new User { Name = "Ghost", Email = "ghost@example.com", CreatedAtUtc = TestDb.SeedTime },
                CancellationToken.None);
        }   // no commit → rollback

        Assert.Empty(await users.GetAllAsync(CancellationToken.None));
    }
}

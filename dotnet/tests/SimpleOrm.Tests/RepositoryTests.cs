using SimpleOrm.Sample.Models;
using SimpleOrm.Sample.Repositories;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The repository layer end to end: the library's Repository&lt;TEntity&gt; base
/// (ADR-0016) plus the sample's entity-specific methods, over one injected session.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class RepositoryTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Repositories_share_one_session_and_cover_the_flow()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var users = new UserRepository(db);
        var transactions = new TransactionRepository(db);

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

        ada.DisplayName = "The Countess";                                          // generic update from the base
        await users.UpdateAsync(ada, CancellationToken.None);
        Assert.Equal("The Countess", (await users.GetAsync(ada.Id, CancellationToken.None)).DisplayName);

        await users.DeleteAsync(ada.Id, CancellationToken.None);
        Assert.Null(await users.GetOrDefaultAsync(ada.Id, CancellationToken.None));
    }

    [Fact]
    public async Task Transactions_wrap_repository_work_through_the_shared_session()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var users = new UserRepository(db);

        await using (await db.BeginAsync(CancellationToken.None))
        {
            await users.InsertAsync(
                new User { Name = "Ghost", Email = "ghost@example.com", CreatedAtUtc = TestDb.SeedTime },
                CancellationToken.None);
        }   // no commit → rollback

        Assert.Empty(await users.GetAllAsync(CancellationToken.None));
    }
}

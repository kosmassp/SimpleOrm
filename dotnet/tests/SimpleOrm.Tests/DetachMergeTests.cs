using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 3 M3 (spec/unit-of-work.md "Detach and merge") beyond the merge-cases:
/// both need a tracked session, detach cancels what a persist cascade scheduled,
/// and merge leaves navigations alone.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class DetachMergeTests(SqliteFixture fixture)
{
    private async Task<Db> OpenTrackedAsync()
    {
        await using (await TestDb.OpenAsync(fixture))
        {
        }

        return await Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions { Dialect = new SqliteDialect(), Tracking = TrackingMode.Tracked },
            CancellationToken.None);
    }

    [Fact]
    public async Task Detach_and_merge_need_a_tracked_session()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", $"ada-{Guid.NewGuid():N}@example.com");
        Assert.Equal("UOW-003", Assert.Throws<SimpleOrmException>(() => db.Detach(ada)).Code);
        Assert.Equal("UOW-003", (await Assert.ThrowsAsync<SimpleOrmException>(() => db.MergeAsync(ada, CancellationToken.None))).Code);
    }

    [Fact]
    public async Task Detaching_a_scheduled_parent_cancels_the_inserts_its_cascade_scheduled()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, "Ada", $"ada-{Guid.NewGuid():N}@example.com");
        var detail = new TransactionDetail { Description = "d", Quantity = 1, UnitPrice = 1m };
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m };
        typeof(Transaction).GetProperty(nameof(Transaction.Details))!.SetValue(transaction, new List<TransactionDetail> { detail });

        db.Persist(transaction);
        db.Detach(transaction);
        Assert.False(db.HasPendingWork);
        Assert.Equal("UOW-001", Assert.Throws<SimpleOrmException>(() => db.Detach(detail)).Code);
    }

    [Fact]
    public async Task Merge_copies_values_but_not_navigations()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, "Ada", $"ada-{Guid.NewGuid():N}@example.com");
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m };
        await db.InsertAsync(transaction, CancellationToken.None);
        await db.LoadAsync(transaction, nameof(Transaction.User), CancellationToken.None);
        Assert.Same(ada, transaction.User);

        var copy = new Transaction
        {
            Id = transaction.Id, UserId = ada.Id, Status = TransactionStatus.Completed, Amount = 2m,
            Version = transaction.Version, CreatedAtUtc = transaction.CreatedAtUtc,
        };
        var merged = await db.MergeAsync(copy, CancellationToken.None);
        Assert.Same(transaction, merged);
        Assert.Equal(TransactionStatus.Completed, transaction.Status);
        Assert.Equal(2m, transaction.Amount);
        Assert.Same(ada, transaction.User);          // navigations are not merged
        Assert.Equal([nameof(Transaction.Status), nameof(Transaction.Amount)], db.ChangesOf(transaction));
    }
}

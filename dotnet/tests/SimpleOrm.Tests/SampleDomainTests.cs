using SimpleOrm.Sample;
using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>The sample registry end to end: enums as TEXT, decimals, and the view entity.</summary>
[Collection(SqliteCollection.Name)]
public sealed class SampleDomainTests(SqliteFixture fixture)
{
    [Fact]
    public async Task Transactions_round_trip_enum_and_decimal()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var ada = await db.QuerySingleAsync(
            Queries.UserByEmail, new UserByEmailArgs("ada@example.com"), CancellationToken.None);

        await db.ExecuteAsync(
            Commands.InsertTransaction,
            new InsertTransactionArgs(ada.Id, TransactionStatus.Pending, 19.99m, TestDb.SeedTime),
            CancellationToken.None);

        var pending = await db.QueryAsync(
            Queries.TransactionsByStatus,
            new TransactionsByStatusArgs(TransactionStatus.Pending),
            CancellationToken.None);

        var transaction = Assert.Single(pending);
        Assert.Equal(TransactionStatus.Pending, transaction.Status);   // TEXT 'Pending' → enum
        Assert.Equal(19.99m, transaction.Amount);
        Assert.Null(transaction.User);                                 // navigation stays null at Level 1

        await db.ExecuteAsync(
            Commands.SetTransactionStatus,
            new SetTransactionStatusArgs(transaction.Id, TransactionStatus.Completed, TestDb.SeedTime),
            CancellationToken.None);

        var byUser = await db.QueryAsync(
            Queries.TransactionsByUser, new TransactionsByUserArgs(ada.Id), CancellationToken.None);
        Assert.Equal(TransactionStatus.Completed, Assert.Single(byUser).Status);
    }

    [Fact]
    public async Task View_entity_reads_aggregated_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        var users = await db.QueryAsync(Queries.AllUsers, EmptyArgs.Value, CancellationToken.None);

        await db.ExecuteAsync(
            Commands.InsertTransaction,
            new InsertTransactionArgs(users[0].Id, TransactionStatus.Completed, 10.50m, TestDb.SeedTime),
            CancellationToken.None);
        await db.ExecuteAsync(
            Commands.InsertTransaction,
            new InsertTransactionArgs(users[0].Id, TransactionStatus.Completed, 4.50m, TestDb.SeedTime),
            CancellationToken.None);

        var totals = await db.QueryAsync(Queries.UserTransactionTotals, EmptyArgs.Value, CancellationToken.None);

        Assert.Equal(2, totals.Count);
        var ada = totals.Single(t => t.UserName == "Ada");
        Assert.Equal(2, ada.TransactionCount);
        Assert.Equal(15.00m, ada.TotalAmount);
        Assert.Equal(0, totals.Single(t => t.UserName == "Grace").TransactionCount);
    }
}

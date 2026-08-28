using SimpleOrm.Sample;
using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>The sample domain end to end: generated inserts, enums, decimals, views, statements.</summary>
[Collection(SqliteCollection.Name)]
public sealed class SampleDomainTests(SqliteFixture fixture)
{
    private static Transaction NewTransaction(long userId, TransactionStatus status, decimal amount) => new()
    {
        UserId = userId,
        Status = status,
        Amount = amount,
        CreatedAtUtc = TestDb.SeedTime,
    };

    [Fact]
    public async Task Generated_insert_writes_back_the_database_key()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var grace = await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");

        Assert.True(ada.Id > 0);                 // RETURNING id, written onto the entity
        Assert.True(grace.Id > ada.Id);
    }

    [Fact]
    public async Task Transactions_round_trip_enum_and_decimal()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        await db.InsertAsync(NewTransaction(ada.Id, TransactionStatus.Pending, 19.99m), CancellationToken.None);

        var pending = await db.Query<Transaction>()
            .Where(Criteria.Eq(nameof(Transaction.Status), TransactionStatus.Pending))
            .ToListAsync(CancellationToken.None);

        var transaction = Assert.Single(pending);
        Assert.Equal(TransactionStatus.Pending, transaction.Status);   // TEXT 'Pending' → enum
        Assert.Equal(19.99m, transaction.Amount);
        Assert.Null(transaction.User);                                 // navigation stays null at Level 1

        await db.ExecuteAsync(
            Commands.SetTransactionStatus,
            new SetTransactionStatusArgs(transaction.Id, TransactionStatus.Completed, TestDb.SeedTime),
            CancellationToken.None);

        var byUser = await db.Query<Transaction>()
            .Where(Criteria.Eq(nameof(Transaction.UserId), ada.Id))
            .ToListAsync(CancellationToken.None);
        Assert.Equal(TransactionStatus.Completed, Assert.Single(byUser).Status);
    }

    [Fact]
    public async Task Composite_key_entity_inserts_without_returning()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var role = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(role, CancellationToken.None);

        await db.InsertAsync(
            new UserRole { UserId = ada.Id, RoleId = role.Id, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        Query<EmptyArgs, long> countLinks = Query.Inline("select count(user_id) from user_roles");
        Assert.Equal(1L, await db.QuerySingleAsync(countLinks, EmptyArgs.Value, CancellationToken.None));
    }

    [Fact]
    public async Task Insert_refuses_readonly_sources_with_CRUD003()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.InsertAsync(new UserTransactionTotal { UserName = "x" }, CancellationToken.None));
        Assert.Equal("CRUD-003", exception.Code);
    }

    [Fact]
    public async Task Materialized_view_creation_on_sqlite_is_DDL002()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.CreateViewAsync<MonthlySalesTotal>(CancellationToken.None));
        Assert.Equal("DDL-002", exception.Code);
    }

    [Fact]
    public async Task Statement_entity_executes_by_type_without_a_registry_entry()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await db.InsertAsync(NewTransaction(ada.Id, TransactionStatus.Completed, 10.00m), CancellationToken.None);
        await db.InsertAsync(NewTransaction(ada.Id, TransactionStatus.Completed, 5.25m), CancellationToken.None);

        var days = await db.QueryAsync<DailySales>(
            new DailySalesArgs(TestDb.SeedTime.AddDays(-1)), CancellationToken.None);

        var day = Assert.Single(days);
        Assert.Equal(DateOnly.FromDateTime(TestDb.SeedTime), day.SalesDate);
        Assert.Equal(2, day.TransactionCount);
        Assert.Equal(15.25m, day.TotalAmount);
    }

    [Fact]
    public async Task Statement_execution_validates_args_types_and_target_kind()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var wrongType = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync<DailySales>(new { Since = "not a date" }, CancellationToken.None));
        Assert.Equal("PRM-012", wrongType.Code);

        var notStatement = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAsync<User>(new DailySalesArgs(TestDb.SeedTime), CancellationToken.None));
        Assert.Equal("QRY-004", notStatement.Code);
    }

    [Fact]
    public async Task View_entity_reads_aggregated_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await db.InsertAsync(NewTransaction(ada.Id, TransactionStatus.Completed, 10.50m), CancellationToken.None);
        await db.InsertAsync(NewTransaction(ada.Id, TransactionStatus.Completed, 4.50m), CancellationToken.None);

        var totals = await db.QueryAllAsync<UserTransactionTotal>(CancellationToken.None);

        Assert.Equal(2, totals.Count);
        var adaTotals = totals.Single(t => t.UserName == "Ada");
        Assert.Equal(2, adaTotals.TransactionCount);
        Assert.Equal(15.00m, adaTotals.TotalAmount);
        Assert.Equal(0, totals.Single(t => t.UserName == "Grace").TransactionCount);
    }
}

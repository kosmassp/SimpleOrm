using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// §7.10: nested results are produced by the database with
/// json_group_array(json_object(…)) and deserialized by the JSON handler.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class JsonNestingTests(SqliteFixture fixture)
{
    public sealed record DetailLine(string Description, int Quantity, decimal UnitPrice);

    public sealed record TransactionWithDetails(long Id, decimal Amount, List<DetailLine> Details);

    private static readonly Query<TransactionsByUserArgsLocal, TransactionWithDetails> WithDetails = Query.Inline(
        """
        select t.id,
               t.amount,
               (select json_group_array(json_object(
                        'description', d.description,
                        'quantity', d.quantity,
                        'unit_price', d.unit_price))
                from transaction_details d
                where d.transaction_id = t.id) as details
        from transactions t
        where t.user_id = @UserId
        order by t.id
        """);

    public sealed record TransactionsByUserArgsLocal(long UserId);

    [Fact]
    public async Task Children_nest_through_the_json_handler()
    {
        var options = new DbOptions { Dialect = new SqliteDialect() };
        options.TypeHandlers.Json<List<DetailLine>>();
        await using var db = await Db.OpenAsync(fixture.ConnectionString, options, CancellationToken.None);
        await using var setup = await TestDb.OpenAsync(fixture);   // schema + clean rows

        var ada = await TestDb.InsertUserAsync(setup, "Ada", "ada@example.com");
        var tx = new Transaction { UserId = ada.Id, Status = TransactionStatus.Completed, Amount = 15.25m, CreatedAtUtc = TestDb.SeedTime };
        await setup.InsertAsync(tx, CancellationToken.None);
        await setup.InsertAsync(
            new TransactionDetail { TransactionId = tx.Id, Description = "cake", Quantity = 2, UnitPrice = 5.00m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);
        await setup.InsertAsync(
            new TransactionDetail { TransactionId = tx.Id, Description = "candle", Quantity = 1, UnitPrice = 5.25m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var rows = await db.QueryAsync(WithDetails, new TransactionsByUserArgsLocal(ada.Id), CancellationToken.None);

        var row = Assert.Single(rows);
        Assert.Equal(15.25m, row.Amount);
        Assert.Equal(2, row.Details.Count);
        Assert.Equal(new DetailLine("cake", 2, 5.00m), row.Details[0]);
        Assert.Equal(new DetailLine("candle", 1, 5.25m), row.Details[1]);

        // A parent with no children gets an empty list, not null.
        var grace = await TestDb.InsertUserAsync(setup, "Grace", "grace@example.com");
        var lonely = new Transaction { UserId = grace.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
        await setup.InsertAsync(lonely, CancellationToken.None);
        var lonelyRow = Assert.Single(
            await db.QueryAsync(WithDetails, new TransactionsByUserArgsLocal(grace.Id), CancellationToken.None));
        Assert.Empty(lonelyRow.Details);
    }
}

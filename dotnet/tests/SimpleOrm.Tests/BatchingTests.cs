using System.Data.Common;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Statement batching (Level 4 item 3, ADR-0044, spec/session.md "Batching")
/// beyond the batch-cases: the option and its question, the plan's rules the
/// SQLite facts cannot show (the parameter cap; an update reading a key the
/// current message generates), the explicit batch inside the caller's
/// transaction and after disposal, and the statement counter behind the C#
/// realization's one-statement rule.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class BatchingTests(SqliteFixture fixture)
{
    private static readonly Command<IdArgs> TouchUser = Query.Inline("update users set display_name = 'x' where id = @Id");

    private sealed record IdArgs(long Id);

    private async Task<Db> OpenAsync(BatchingMode batching, TrackingMode tracking = TrackingMode.Untracked, IDialect? dialect = null, int maxStatements = 100)
    {
        await using (await TestDb.OpenAsync(fixture))
        {
        }

        return await Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions
            {
                Dialect = dialect ?? new SqliteDialect(), Tracking = tracking, Batching = batching, MaxStatementsPerMessage = maxStatements,
            },
            CancellationToken.None);
    }

    [Fact]
    public async Task Batching_is_off_by_default_and_never_in_effect_on_sqlite()
    {
        Assert.Equal(BatchingMode.Off, new DbOptions { Dialect = new SqliteDialect() }.Batching);
        Assert.Equal(100, new DbOptions { Dialect = new SqliteDialect() }.MaxStatementsPerMessage);
        Assert.False(new SqliteDialect().SupportsStatementBatching);

        await using var off = await OpenAsync(BatchingMode.Off);
        Assert.False(off.BatchingInEffect);
        await using var auto = await OpenAsync(BatchingMode.Auto);
        Assert.False(auto.BatchingInEffect);   // Microsoft.Data.Sqlite has no DbBatch: the plan runs statement by statement
    }

    [Fact]
    public async Task A_statement_cap_below_one_is_an_argument_error_at_open()
    {
        await Assert.ThrowsAsync<ArgumentOutOfRangeException>(() => Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions { Dialect = new SqliteDialect(), Batching = BatchingMode.Auto, MaxStatementsPerMessage = 0 },
            CancellationToken.None));
    }

    [Fact]
    public async Task The_parameter_cap_cuts_a_message_before_it_would_overflow()
    {
        // Every User insert binds five parameters (name, email, display_name, created_at, updated_at).
        await using var db = await OpenAsync(BatchingMode.Auto, dialect: new CappedDialect(new SqliteDialect(), cap: 12));
        var sizes = new List<int>();
        db.MessageObserver = sizes.Add;
        await using var batch = db.Batch();
        for (var i = 0; i < 5; i++)
        {
            batch.Insert(new User { Name = "u" + i, Email = $"u{i}@example.test", CreatedAtUtc = TestDb.SeedTime });
        }

        var result = await batch.SendAsync(CancellationToken.None);
        Assert.Equal([2, 2, 1], sizes);
        Assert.Equal(3, result.Messages);
        Assert.Equal([1, 1, 1, 1, 1], result.Affected);
    }

    [Fact]
    public async Task An_update_waits_for_the_message_that_generates_the_key_its_navigation_reads()
    {
        // Rule 1b for an update: the tracked transaction's navigation points at a user
        // inserted in the same flush. Without the cut its CRUD-004 check would compare
        // the user's not-yet-generated key; with it, Auto refuses exactly as Off does.
        foreach (var mode in new[] { BatchingMode.Off, BatchingMode.Auto })
        {
            await using var db = await OpenAsync(mode, TrackingMode.Tracked);
            var owner = await TestDb.InsertUserAsync(db, "Ada", "ada@example.test");
            var transaction = new Transaction { UserId = owner.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
            await db.InsertAsync(transaction, CancellationToken.None);

            var newcomer = new User { Name = "Linus", Email = "linus@example.test", CreatedAtUtc = TestDb.SeedTime };
            typeof(Transaction).GetProperty(nameof(Transaction.User))!.SetValue(transaction, newcomer);
            transaction.UserId = 0;   // what a not-yet-inserted user's key reads as
            db.Persist(newcomer);
            var sizes = new List<int>();
            db.MessageObserver = sizes.Add;

            var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.FlushAsync(CancellationToken.None));
            Assert.Equal("CRUD-004", refused.Code);   // without the cut, Auto would have compared 0 with 0 and written UserId = 0
            Assert.Equal([1], sizes);                 // the insert went alone; the refused update was never sent
            Assert.Equal(0, newcomer.Id);   // the failed flush undid the generated key
        }
    }

    [Fact]
    public async Task A_send_inside_the_callers_transaction_leaves_it_to_the_caller()
    {
        await using var db = await OpenAsync(BatchingMode.Auto);
        await using (var tx = await db.BeginAsync(CancellationToken.None))
        {
            await using var batch = db.Batch();
            batch.Insert(new User { Name = "Ada", Email = "ada@example.test", CreatedAtUtc = TestDb.SeedTime });
            batch.Insert(new User { Name = "Grace", Email = "grace@example.test", CreatedAtUtc = TestDb.SeedTime });
            await batch.SendAsync(CancellationToken.None);
            Assert.Equal(2, await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
            await tx.RollbackAsync(CancellationToken.None);   // the send neither committed nor rolled back
        }

        Assert.Equal(0, await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
    }

    [Fact]
    public async Task A_disposed_batch_drops_its_queue_and_refuses_with_TX003()
    {
        await using var db = await OpenAsync(BatchingMode.Auto);
        var batch = db.Batch();
        batch.Insert(new User { Name = "Ada", Email = "ada@example.test", CreatedAtUtc = TestDb.SeedTime });
        await batch.DisposeAsync();

        Assert.Equal("TX-003", Assert.Throws<SimpleOrmException>(() => batch.Execute(TouchUser, new IdArgs(1))).Code);
        Assert.Equal("TX-003", (await Assert.ThrowsAsync<SimpleOrmException>(() => batch.SendAsync(CancellationToken.None))).Code);
        Assert.Equal(0, await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
        Assert.Throws<ArgumentNullException>(() => db.Batch().Insert<User>(null!));
    }

    [Fact]
    public async Task The_sequential_realization_reuses_one_command_per_statement_text()
    {
        var counting = new CommandCountingDialect(new SqliteDialect());
        await using var db = await OpenAsync(BatchingMode.Auto, dialect: counting);
        await using var batch = db.Batch();
        for (var i = 0; i < 10; i++)
        {
            batch.Insert(new User { Name = "u" + i, Email = $"u{i}@example.test", CreatedAtUtc = TestDb.SeedTime });
        }

        counting.Reset();
        await batch.SendAsync(CancellationToken.None);
        Assert.Equal(1, counting.DistinctCommandsExecuted);   // ten inserts, one prepared command
        Assert.Equal(10, await db.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
    }

    [Theory]
    [InlineData("update users set name = 'a'", false)]
    [InlineData("update users set name = 'a';", false)]
    [InlineData("update users set name = 'a';  \n ;", false)]
    [InlineData("update users set name = 'a;b' -- trailing; comment", false)]
    [InlineData("update users set name = 'a'; delete from roles", true)]
    [InlineData("/* one; */ update users set name = @Name; select 1", true)]
    public void A_command_holds_several_statements_only_outside_literals_and_comments(string sql, bool several)
        => Assert.Equal(several, SqlPlaceholders.HoldsSeveralStatements(sql));

    [Fact]
    public void System_functions_are_never_placeholders()
    {
        // Found by the PHP port (ADR-0044): the second @ of @@rowcount read as a placeholder.
        Assert.Equal(["Id"], SqlPlaceholders.Find("update users set name = 'x' where id = @Id; select @@rowcount"));
        Assert.Empty(SqlPlaceholders.Find("select @@identity"));
    }

    /// <summary>The SQLite dialect with a parameter cap, to show rule 3 where SQLite has none.</summary>
    private sealed class CappedDialect(IDialect inner, int cap) : ForwardingDialect(inner)
    {
        public override int? MaxParametersPerMessage => cap;
    }

    /// <summary>Counts the distinct command objects that executed, through the connection the dialect creates.</summary>
    private sealed class CommandCountingDialect(IDialect inner) : ForwardingDialect(inner)
    {
        private readonly HashSet<DbCommand> _executed = new(ReferenceEqualityComparer.Instance);

        public int DistinctCommandsExecuted => _executed.Count;

        public void Reset() => _executed.Clear();

        public override DbConnection CreateConnection(string connectionString)
            => new CountingConnection(Inner.CreateConnection(connectionString), _executed);
    }
}

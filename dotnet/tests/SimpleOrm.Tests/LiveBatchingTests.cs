using System.Data.Common;
using SimpleOrm.Postgres;
using SimpleOrm.Sample.Models;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Statement batching on a real engine (ADR-0044, spec/dialects.md "Statement
/// batching" and the testing contract): a batched flush whose generated keys flow
/// across messages, an explicit batch carrying every operation, a stale version
/// and a failing statement in the middle of a message, and a registry command of
/// several statements. The same scenarios run against PostgreSQL
/// (<c>NpgsqlBatch</c>) and SQL Server (<c>SqlBatch</c>); each leg skips where no
/// engine answers.
/// </summary>
public static class LiveBatchingTests
{
    private static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");
    private static readonly Query<EmptyArgs, long> CountTransactions = Query.Inline("select count(id) from transactions");
    private static readonly Query<EmptyArgs, long> CountDetails = Query.Inline("select count(id) from transaction_details");
    private static readonly Query<EmptyArgs, long> CountRoles = Query.Inline("select count(id) from roles");
    private static readonly Query<EmptyArgs, long> OrphanTransactions = Query.Inline(
        "select count(t.id) from transactions t where not exists (select 1 from users u where u.id = t.user_id)");

    private sealed record NameArgs(long Id, string Name);

    private static readonly Command<NameArgs> RenameUser = Query.Inline("update users set display_name = @Name where id = @Id");
    private static readonly Command<NameArgs> RenameUserTwice = Query.Inline(
        "update users set display_name = @Name where id = @Id; update users set name = @Name where id = @Id");

    internal static async Task<Db> OpenAsync(string connectionString, IDialect dialect, TrackingMode tracking)
    {
        var db = await Db.OpenAsync(
            connectionString,
            new DbOptions { Dialect = dialect, Tracking = tracking, Batching = BatchingMode.Auto, Clock = () => TestDb.SeedTime },
            CancellationToken.None);
        await db.CreateTableAsync<User>(CancellationToken.None);
        await db.CreateTableAsync<Role>(CancellationToken.None);
        await db.CreateTableAsync<UserRole>(CancellationToken.None);
        await db.CreateTableAsync<Transaction>(CancellationToken.None);
        await db.CreateTableAsync<TransactionDetail>(CancellationToken.None);
        await db.CreateTableAsync<UserProfile>(CancellationToken.None);
        foreach (var table in new[] { "transaction_details", "transactions", "user_roles", "user_profiles", "roles", "users" })
        {
            Command<EmptyArgs> clear = Query.Inline("delete from " + table);
            await db.ExecuteAsync(clear, EmptyArgs.Value, CancellationToken.None);
        }

        return db;
    }

    private static User NewUser(string name) => new() { Name = name, Email = name + "@example.test", CreatedAtUtc = TestDb.SeedTime };

    private static Transaction NewTransaction(User user, params TransactionDetail[] details)
    {
        var transaction = new Transaction { Status = TransactionStatus.Pending, Amount = 10m, CreatedAtUtc = TestDb.SeedTime };
        typeof(Transaction).GetProperty(nameof(Transaction.User))!.SetValue(transaction, user);
        typeof(Transaction).GetProperty(nameof(Transaction.Details))!.SetValue(transaction, details.ToList());
        return transaction;
    }

    private static TransactionDetail NewDetail(string description)
        => new() { Description = description, Quantity = 1, UnitPrice = 2.5m, CreatedAtUtc = TestDb.SeedTime };

    private static Task<long> CountAsync(Db db, Query<EmptyArgs, long> query) => db.QuerySingleAsync(query, EmptyArgs.Value, CancellationToken.None);

    /// <summary>Three users, two transactions each, two cascaded details each: three levels of generated keys, three messages — where Off takes 21 round trips.</summary>
    internal static async Task BatchedFlushFlowsKeysAcrossMessagesAsync(Func<TrackingMode, Task<Db>> open)
    {
        await using var db = await open(TrackingMode.Tracked);
        Assert.True(db.BatchingInEffect);
        var sizes = new List<int>();
        db.MessageObserver = sizes.Add;

        var users = Enumerable.Range(0, 3).Select(i => NewUser("u" + i)).ToList();
        var transactions = users.SelectMany(u => new[] { NewTransaction(u, NewDetail("a"), NewDetail("b")), NewTransaction(u, NewDetail("c"), NewDetail("d")) }).ToList();
        foreach (var user in users)
        {
            db.Persist(user);
        }

        foreach (var transaction in transactions)
        {
            db.Persist(transaction);   // cascades its details
        }

        var summary = await db.FlushAsync(CancellationToken.None);
        Assert.Equal(new FlushSummary(21, 0, 0), summary);
        Assert.Equal([3, 6, 12], sizes);
        Assert.All(transactions, t => Assert.Equal(t.User!.Id, t.UserId));
        Assert.All(transactions.SelectMany(t => t.Details), d => Assert.NotEqual(0, d.TransactionId));
        Assert.Equal(3, await CountAsync(db, CountUsers));
        Assert.Equal(6, await CountAsync(db, CountTransactions));
        Assert.Equal(12, await CountAsync(db, CountDetails));
        Assert.Equal(0, await CountAsync(db, OrphanTransactions));

        // A dirty update and a removal in one more batched flush.
        users[0].Name = "renamed";
        db.Remove(transactions[5]);
        sizes.Clear();
        Assert.Equal(new FlushSummary(0, 1, 3), await db.FlushAsync(CancellationToken.None));
        Assert.Equal([4], sizes);
        Assert.Equal(5, await CountAsync(db, CountTransactions));
    }

    /// <summary>Every operation in one send; on SQL Server the soft delete reads its reported count after the command, so it starts a second message (rule 2).</summary>
    internal static async Task ExplicitBatchCarriesEveryOperationAsync(Func<TrackingMode, Task<Db>> open, int expectedMessages)
    {
        await using var db = await open(TrackingMode.Untracked);
        var ada = NewUser("ada");
        var grace = NewUser("grace");
        await db.InsertAsync(ada, CancellationToken.None);
        await db.InsertAsync(grace, CancellationToken.None);
        var kept = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
        var gone = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 2m, CreatedAtUtc = TestDb.SeedTime };
        var retired = new Role { Name = "retired", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(kept, CancellationToken.None);
        await db.InsertAsync(gone, CancellationToken.None);
        await db.InsertAsync(retired, CancellationToken.None);

        var role = new Role { Name = "reader", CreatedAtUtc = TestDb.SeedTime };
        ada.DisplayName = "Ada";
        kept.Note = "kept";
        await using var batch = db.Batch();
        batch.Insert(role);
        batch.UpdateOnly(ada, [nameof(User.DisplayName)]);
        batch.Update(kept);
        batch.Delete<Transaction>(gone);
        batch.Execute(RenameUser, new NameArgs(grace.Id, "Grace"));
        batch.Delete<Role>(retired.Id);   // a soft delete: the flag-setting update
        var result = await batch.SendAsync(CancellationToken.None);

        Assert.Equal([1, 1, 1, 1, 1, 1], result.Affected);
        Assert.Equal(expectedMessages, result.Messages);
        Assert.NotEqual(0, role.Id);
        Assert.Equal(1, kept.Version);
        Assert.Equal(1, await CountAsync(db, CountTransactions));
        Assert.Equal("Grace", (await db.GetAsync<User>(grace.Id, CancellationToken.None)).DisplayName);
        Assert.True((await db.GetAsync<Role>(retired.Id, CancellationToken.None)).IsDeleted);
    }

    /// <summary>A stale version in the middle of a message: CRUD-010, nothing applied, every instance as it was — the statements after it ran on the engine and were rolled back.</summary>
    internal static async Task StaleVersionMidMessageUndoesEverythingAsync(Func<TrackingMode, Task<Db>> open)
    {
        await using var db = await open(TrackingMode.Untracked);
        var ada = NewUser("ada");
        await db.InsertAsync(ada, CancellationToken.None);
        var current = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(current, CancellationToken.None);
        var stale = await db.GetAsync<Transaction>(current.Id, CancellationToken.None);
        current.Note = "first";
        await db.UpdateAsync(current, CancellationToken.None);

        var newcomer = NewUser("linus");
        stale.Note = "second";
        ada.DisplayName = "after the failure";
        await using var batch = db.Batch();
        batch.Insert(newcomer);
        batch.Update(stale);
        batch.UpdateOnly(ada, [nameof(User.DisplayName)]);
        await Assert.ThrowsAsync<ConcurrencyException>(() => batch.SendAsync(CancellationToken.None));

        Assert.Equal(0, newcomer.Id);
        Assert.Equal(0, stale.Version);
        Assert.Null(stale.UpdatedAtUtc);
        Assert.Null(ada.UpdatedAtUtc);
        Assert.Equal(1, await CountAsync(db, CountUsers));
        Assert.Null((await db.GetAsync<User>(ada.Id, CancellationToken.None)).DisplayName);
        Assert.Equal("first", (await db.GetAsync<Transaction>(current.Id, CancellationToken.None)).Note);
    }

    /// <summary>A provider error in the middle of a message (a duplicate natural key): the provider's own exception, nothing applied, the generated keys undone.</summary>
    internal static async Task FailingStatementMidMessageRollsBackAsync(Func<TrackingMode, Task<Db>> open)
    {
        await using var db = await open(TrackingMode.Untracked);
        var ada = NewUser("ada");
        var admin = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(ada, CancellationToken.None);
        await db.InsertAsync(admin, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);

        var newcomer = NewUser("linus");
        var reader = new Role { Name = "reader", CreatedAtUtc = TestDb.SeedTime };
        await using var batch = db.Batch();
        batch.Insert(newcomer);
        batch.Insert(new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime });   // the duplicate
        batch.Insert(reader);
        await Assert.ThrowsAnyAsync<DbException>(() => batch.SendAsync(CancellationToken.None));

        Assert.Equal(0, newcomer.Id);
        Assert.Equal(0, reader.Id);
        Assert.Equal(1, await CountAsync(db, CountUsers));
        Assert.Equal(1, await CountAsync(db, CountRoles));

        // The failing statement last in its message, whose result the session never
        // reads: the drain surfaces it before anything is judged or committed.
        await using var trailing = db.Batch();
        trailing.Insert(newcomer);
        trailing.Insert(new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime });
        await Assert.ThrowsAnyAsync<DbException>(() => trailing.SendAsync(CancellationToken.None));
        Assert.Equal(0, newcomer.Id);
        Assert.Equal(1, await CountAsync(db, CountUsers));
    }

    /// <summary>A registry command of several statements in a batch: the C# realization sends it on its own; its count is the driver's for the whole command.</summary>
    internal static async Task SeveralStatementCommandRunsInTheBatchAsync(Func<TrackingMode, Task<Db>> open)
    {
        await using var db = await open(TrackingMode.Untracked);
        var ada = NewUser("ada");
        await db.InsertAsync(ada, CancellationToken.None);
        var role = new Role { Name = "reader", CreatedAtUtc = TestDb.SeedTime };
        await using var batch = db.Batch();
        batch.Execute(RenameUser, new NameArgs(ada.Id, "one"));
        batch.Execute(RenameUserTwice, new NameArgs(ada.Id, "two"));
        batch.Insert(role);
        var result = await batch.SendAsync(CancellationToken.None);

        Assert.Equal([1, 2, 1], result.Affected);
        var read = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        Assert.Equal("two", read.DisplayName);
        Assert.Equal("two", read.Name);
        Assert.NotEqual(0, role.Id);
    }

    [Collection(PostgresCollection.Name)]
    public sealed class OnPostgres(PostgresFixture fixture)
    {
        private Task<Db> Open(TrackingMode tracking) => OpenAsync(fixture.ConnectionString, new PostgresDialect(), tracking);

        [PostgresFact]
        public Task A_batched_flush_flows_generated_keys_across_messages() => BatchedFlushFlowsKeysAcrossMessagesAsync(Open);

        [PostgresFact]
        public Task An_explicit_batch_carries_every_operation_in_one_message() => ExplicitBatchCarriesEveryOperationAsync(Open, expectedMessages: 1);

        [PostgresFact]
        public Task A_stale_version_mid_message_undoes_everything() => StaleVersionMidMessageUndoesEverythingAsync(Open);

        [PostgresFact]
        public Task A_failing_statement_mid_message_rolls_back() => FailingStatementMidMessageRollsBackAsync(Open);

        [PostgresFact]
        public Task A_command_of_several_statements_runs_in_a_batch() => SeveralStatementCommandRunsInTheBatchAsync(Open);
    }

    [Collection(SqlServerCollection.Name)]
    public sealed class OnSqlServer(SqlServerFixture fixture)
    {
        private Task<Db> Open(TrackingMode tracking) => OpenAsync(fixture.ConnectionString, new SqlServerDialect(), tracking);

        [SqlServerFact]
        public Task A_batched_flush_flows_generated_keys_across_messages() => BatchedFlushFlowsKeysAcrossMessagesAsync(Open);

        [SqlServerFact]
        public Task An_explicit_batch_carries_every_operation_in_one_message() => ExplicitBatchCarriesEveryOperationAsync(Open, expectedMessages: 2);

        [SqlServerFact]
        public Task A_stale_version_mid_message_undoes_everything() => StaleVersionMidMessageUndoesEverythingAsync(Open);

        [SqlServerFact]
        public Task A_failing_statement_mid_message_rolls_back() => FailingStatementMidMessageRollsBackAsync(Open);

        [SqlServerFact]
        public Task A_command_of_several_statements_runs_in_a_batch() => SeveralStatementCommandRunsInTheBatchAsync(Open);
    }
}

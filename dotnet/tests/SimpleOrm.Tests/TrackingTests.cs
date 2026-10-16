using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 3 M1 (ADR-0034 add.1) beyond the tracking-cases: every read path
/// consults the identity map — streams, registry queries, criteria, eager
/// loading in each fetch mode — change detection compares values (byte arrays
/// by content), a dirty update bumps the version while a clean one writes
/// nothing, and the default mode leaves Level 0–2 behaviour untouched.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class TrackingTests(SqliteFixture fixture)
{
    private static readonly Query<UserById, User> ByIdQuery = Query.Inline(
        "select id, name, email, display_name, created_at, updated_at from users where id = @Id");

    /// <summary>A tracked session on the fixture database, migrated and cleared by the untracked helper first.</summary>
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
    public async Task The_default_session_is_untracked_and_materializes_fresh_instances()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        Assert.False(db.IsTracking);
        var ada = await TestDb.InsertUserAsync(db, $"T{Guid.NewGuid():N}", $"t-{Guid.NewGuid():N}@example.com");

        var first = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        var second = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        Assert.NotSame(first, second);
        Assert.False(db.IsTracked(first));
        Assert.Equal("UOW-001", Assert.Throws<SimpleOrmException>(() => db.IsDirty(first)).Code);
    }

    [Fact]
    public async Task Every_read_path_returns_the_tracked_instance()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"T{Guid.NewGuid():N}", $"t-{Guid.NewGuid():N}@example.com");
        Assert.True(db.IsTracked(ada));
        await db.InsertAsync(
            new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 5m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var byKey = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        var byCriteria = Assert.Single(await db.Query<User>().Where(Criteria.Eq(nameof(User.Id), ada.Id)).ToListAsync(CancellationToken.None));
        var byRegistry = Assert.Single(await db.QueryAsync(ByIdQuery, new UserById(ada.Id), CancellationToken.None));
        User? streamed = null;
        await foreach (var user in db.StreamAsync(ByIdQuery, new UserById(ada.Id), CancellationToken.None))
        {
            streamed = user;
        }

        Assert.Same(ada, byKey);
        Assert.Same(ada, byCriteria);
        Assert.Same(ada, byRegistry);
        Assert.Same(ada, streamed);

        foreach (var mode in new[] { FetchMode.MultiQuery, FetchMode.SubSelect, FetchMode.Join })
        {
            var transaction = Assert.Single(await db.Query<Transaction>()
                .Where(Criteria.Eq(nameof(Transaction.UserId), ada.Id))
                .Include(nameof(Transaction.User))
                .Fetch(mode)
                .ToListAsync(CancellationToken.None));
            Assert.Same(ada, transaction.User);
        }
    }

    [Fact]
    public async Task Byte_array_changes_are_detected_by_content()
    {
        await using var db = await OpenTrackedAsync();
        await db.CreateTableAsync<Blob>(CancellationToken.None);
        var blob = new Blob { Payload = [1, 2, 3] };
        await db.InsertAsync(blob, CancellationToken.None);
        Assert.False(db.IsDirty(blob));

        blob.Payload[1] = 9;                       // in place: the snapshot copied the array
        Assert.Equal([nameof(Blob.Payload)], db.ChangesOf(blob));

        await db.UpdateAsync(blob, CancellationToken.None);
        blob.Payload = [1, 9, 3];                  // equal content, new array: clean
        Assert.False(db.IsDirty(blob));
    }

    [Fact]
    public async Task A_dirty_tracked_update_bumps_the_version_and_writes_only_the_change()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"T{Guid.NewGuid():N}", $"t-{Guid.NewGuid():N}@example.com");
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 5m, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(transaction, CancellationToken.None);

        await db.UpdateAsync(transaction, CancellationToken.None);   // clean: no statement, no bump
        Assert.Equal(0, transaction.Version);

        transaction.Note = "paid";
        await db.UpdateAsync(transaction, CancellationToken.None);
        Assert.Equal(1, transaction.Version);
        Assert.False(db.IsDirty(transaction));

        await using var other = await Db.OpenAsync(fixture.ConnectionString, TestDb.Options, CancellationToken.None);
        var stored = await other.GetAsync<Transaction>(transaction.Id, CancellationToken.None);
        Assert.Equal("paid", stored.Note);
        Assert.Equal(1, stored.Version);
    }

    [Fact]
    public async Task Join_mode_children_are_the_tracked_instances()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"T{Guid.NewGuid():N}", $"t-{Guid.NewGuid():N}@example.com");
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 5m, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(transaction, CancellationToken.None);

        var user = Assert.Single(await db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), ada.Id))
            .Include(nameof(User.Transactions))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None));
        Assert.Same(ada, user);
        Assert.Same(transaction, Assert.Single(user.Transactions));
    }

    [Fact]
    public async Task A_concurrent_change_still_refuses_a_dirty_tracked_update()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"T{Guid.NewGuid():N}", $"t-{Guid.NewGuid():N}@example.com");
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 5m, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(transaction, CancellationToken.None);

        await using (var other = await Db.OpenAsync(fixture.ConnectionString, TestDb.Options, CancellationToken.None))
        {
            var theirs = await other.GetAsync<Transaction>(transaction.Id, CancellationToken.None);
            theirs.Note = "theirs";
            await other.UpdateAsync(theirs, CancellationToken.None);
        }

        transaction.Note = "mine";
        var refused = await Assert.ThrowsAsync<ConcurrencyException>(() => db.UpdateAsync(transaction, CancellationToken.None));
        Assert.Equal("CRUD-010", refused.Code);
        Assert.True(db.IsDirty(transaction));      // the snapshot did not move: the write did not happen

        await db.RefreshAsync(transaction, CancellationToken.None);
        Assert.Equal("theirs", transaction.Note);
        Assert.False(db.IsDirty(transaction));
    }

    private sealed record UserById(long Id);

    [Table("tracking_blobs")]
    public sealed class Blob
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public byte[] Payload { get; set; } = [];
    }
}

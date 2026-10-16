using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 3 M2 (ADR-0034 §7c, spec/unit-of-work.md) beyond the flush-cases: the
/// declaration rules for cascades, a cycle among scheduled instances, a failed
/// flush that owns its transaction, cancelling a cascaded persist, cascading
/// through a one-to-one, and the immediate operations beside the unit of work.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class UnitOfWorkTests(SqliteFixture fixture)
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
    public void Cascade_is_declared_on_navigations_only()
    {
        var loader = new EntityMapLoader();
        Assert.Equal(CascadeOperations.All, loader.Load<Transaction>().Relationships.Single(r => r.PropertyName == nameof(Transaction.Details)).Cascade);
        Assert.Equal(CascadeOperations.None, loader.Load<User>().Relationships.Single(r => r.PropertyName == nameof(User.Transactions)).Cascade);

        var onColumn = Assert.Throws<MappingException>(() => loader.Load<CascadeOnColumn>());
        Assert.Contains(onColumn.Errors, e => e.Code == "MAP-025");
        var onLink = Assert.Throws<MappingException>(() => loader.Load<CascadeOnManyToMany>());
        Assert.Contains(onLink.Errors, e => e.Code == "MAP-025");
    }

    [Fact]
    public async Task A_cycle_among_scheduled_instances_is_refused_before_any_sql()
    {
        await using var db = await OpenTrackedAsync();
        await db.CreateTableAsync<Node>(CancellationToken.None);
        var a = new Node { Label = "a" };
        var b = new Node { Label = "b" };
        typeof(Node).GetProperty(nameof(Node.Parent))!.SetValue(a, b);
        typeof(Node).GetProperty(nameof(Node.Parent))!.SetValue(b, a);
        db.Persist(a);
        db.Persist(b);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.FlushAsync(CancellationToken.None));
        Assert.Equal("UOW-004", refused.Code);
        Assert.True(db.HasPendingWork);   // the schedule is kept

        // A self-referencing chain without a cycle orders parent first.
        typeof(Node).GetProperty(nameof(Node.Parent))!.SetValue(b, null);
        var summary = await db.FlushAsync(CancellationToken.None);
        Assert.Equal(new FlushSummary(2, 0, 0), summary);
        Assert.Equal(b.Id, a.ParentId);
    }

    [Fact]
    public async Task A_failed_flush_that_owns_its_transaction_applies_nothing_and_keeps_the_schedule()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"U{Guid.NewGuid():N}", $"u-{Guid.NewGuid():N}@example.com");
        var fine = new User { Name = "fine", Email = $"fine-{Guid.NewGuid():N}@example.com", CreatedAtUtc = TestDb.SeedTime };
        var duplicate = new User { Name = "dup", Email = ada.Email, CreatedAtUtc = TestDb.SeedTime };   // the unique index refuses
        db.Persist(fine);
        db.Persist(duplicate);

        await Assert.ThrowsAnyAsync<Exception>(() => db.FlushAsync(CancellationToken.None));
        Assert.True(db.HasPendingWork);
        await using (var other = await Db.OpenAsync(fixture.ConnectionString, TestDb.Options, CancellationToken.None))
        {
            Assert.Equal(1, await other.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
        }

        db.Remove(duplicate);              // cancels the bad insert
        Assert.Equal(new FlushSummary(1, 0, 0), await db.FlushAsync(CancellationToken.None));
    }

    [Fact]
    public async Task Removing_a_scheduled_parent_cancels_the_inserts_it_cascaded()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"U{Guid.NewGuid():N}", $"u-{Guid.NewGuid():N}@example.com");
        var detail = new TransactionDetail { Description = "d", Quantity = 1, UnitPrice = 1m, CreatedAtUtc = TestDb.SeedTime };
        var transaction = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
        typeof(Transaction).GetProperty(nameof(Transaction.Details))!.SetValue(transaction, new List<TransactionDetail> { detail });

        db.Persist(transaction);
        db.Remove(transaction);
        Assert.False(db.HasPendingWork);
        Assert.Equal(new FlushSummary(0, 0, 0), await db.FlushAsync(CancellationToken.None));
    }

    [Fact]
    public async Task A_one_to_one_cascade_persists_and_removes_the_single_target()
    {
        await using var db = await OpenTrackedAsync();
        await db.CreateTableAsync<Owner>(CancellationToken.None);
        await db.CreateTableAsync<Passport>(CancellationToken.None);
        var owner = new Owner { Label = "o" };
        var passport = new Passport { Number = "X1" };
        typeof(Owner).GetProperty(nameof(Owner.Passport))!.SetValue(owner, passport);

        db.Persist(owner);
        Assert.Equal(new FlushSummary(2, 0, 0), await db.FlushAsync(CancellationToken.None));
        Assert.Equal(owner.Id, passport.OwnerId);

        db.Remove(owner);
        Assert.Equal(new FlushSummary(0, 0, 2), await db.FlushAsync(CancellationToken.None));
        Assert.False(db.IsTracked(passport));

        // A null singular navigation cascades nothing — the database's foreign key is the authority.
        var alone = new Owner { Label = "alone" };
        db.Persist(alone);
        await db.FlushAsync(CancellationToken.None);
        db.Remove(alone);
        Assert.Equal(new FlushSummary(0, 0, 1), await db.FlushAsync(CancellationToken.None));
    }

    [Fact]
    public async Task Immediate_operations_keep_working_beside_the_unit_of_work()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"U{Guid.NewGuid():N}", $"u-{Guid.NewGuid():N}@example.com");
        db.Persist(ada);                      // tracked already: schedules nothing
        Assert.False(db.HasPendingWork);

        ada.Name = "immediate";
        await db.UpdateAsync(ada, CancellationToken.None);
        Assert.Equal(new FlushSummary(0, 0, 0), await db.FlushAsync(CancellationToken.None));   // nothing left dirty
    }

    [Table("uow_nodes")]
    public sealed class Node
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [Column]
        [ForeignKey(typeof(Node))]
        public long? ParentId { get; set; }

        [ManyToOne(nameof(ParentId))]
        public Node? Parent { get; private set; }
    }

    [Table("uow_owners")]
    public sealed class Owner
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [OneToOne(nameof(Passport.OwnerId))]
        [Cascade(CascadeOperations.All)]
        public Passport? Passport { get; private set; }
    }

    [Table("uow_passports")]
    [Index(nameof(OwnerId), Unique = true)]
    public sealed class Passport
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        [ForeignKey(typeof(Owner))]
        public long OwnerId { get; set; }

        [Column]
        public string? Number { get; set; }
    }

    [Table("uow_bad_column")]
    public sealed class CascadeOnColumn
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [Cascade(CascadeOperations.Persist)]
        public string? Label { get; set; }
    }

    [Table("uow_bad_link")]
    public sealed class CascadeOnManyToMany
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [ManyToMany(typeof(UserRole))]
        [Cascade(CascadeOperations.Persist)]
        public IReadOnlyList<Role> Roles { get; private set; } = [];
    }
}

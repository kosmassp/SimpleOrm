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

        ada.Name = "renamed before the failure";
        await Assert.ThrowsAnyAsync<Exception>(() => db.FlushAsync(CancellationToken.None));
        Assert.True(db.HasPendingWork);
        await using (var other = await Db.OpenAsync(fixture.ConnectionString, TestDb.Options, CancellationToken.None))
        {
            Assert.Equal(1, await other.QuerySingleAsync(TestDb.CountUsers, EmptyArgs.Value, CancellationToken.None));
        }

        // The statements that ran are undone in memory too: the first insert's key
        // and tracking, and the update's snapshot — so the retry flushes everything.
        Assert.False(db.IsTracked(fine));
        Assert.Equal(0, fine.Id);
        Assert.True(db.IsDirty(ada));

        db.Remove(duplicate);              // cancels the bad insert
        Assert.Equal(new FlushSummary(1, 1, 0), await db.FlushAsync(CancellationToken.None));
        Assert.True(db.IsTracked(fine));
        Assert.False(db.IsDirty(ada));
    }

    [Fact]
    public async Task Work_done_immediately_is_dropped_from_the_schedule_and_an_empty_flush_opens_no_transaction()
    {
        await using var db = await OpenTrackedAsync();
        var user = new User { Name = "now", Email = $"now-{Guid.NewGuid():N}@example.com", CreatedAtUtc = TestDb.SeedTime };
        db.Persist(user);
        await db.InsertAsync(user, CancellationToken.None);      // written immediately: the schedule entry is done, not work
        Assert.Equal(default, await db.FlushAsync(CancellationToken.None));
        Assert.False(db.HasPendingWork);

        db.Remove(user);
        await db.DeleteAsync<User>(user, CancellationToken.None);
        Assert.Equal(default, await db.FlushAsync(CancellationToken.None));

        await using var scope = await db.BeginAsync(CancellationToken.None);
        Assert.Equal(default, await db.FlushAsync(CancellationToken.None));   // nothing to write: the scope is untouched
        await scope.CommitAsync(CancellationToken.None);
    }

    [Fact]
    public async Task A_dirty_tracked_read_only_instance_refuses_the_flush_instead_of_being_skipped()
    {
        await using var db = await OpenTrackedAsync();
        var ada = await TestDb.InsertUserAsync(db, $"U{Guid.NewGuid():N}", $"u-{Guid.NewGuid():N}@example.com");
        await db.InsertAsync(
            new Transaction { UserId = ada.Id, Status = TransactionStatus.Completed, Amount = 3m, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);
        var total = Assert.Single(await db.Query<UserTransactionTotal>()
            .Where(Criteria.Eq(nameof(UserTransactionTotal.UserId), ada.Id))
            .ToListAsync(CancellationToken.None));
        Assert.True(db.IsTracked(total));

        total.TotalAmount += 1;
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.FlushAsync(CancellationToken.None));
        Assert.Equal("CRUD-003", refused.Code);
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

        // A child persisted by name survives its cancelled owner — the user asked for it.
        var other = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 2m, CreatedAtUtc = TestDb.SeedTime };
        var kept = new TransactionDetail { TransactionId = 0, Description = "kept", Quantity = 1, UnitPrice = 1m, CreatedAtUtc = TestDb.SeedTime };
        typeof(Transaction).GetProperty(nameof(Transaction.Details))!.SetValue(other, new List<TransactionDetail> { kept });
        db.Persist(kept);
        db.Persist(other);
        db.Remove(other);
        Assert.True(db.HasPendingWork);
        db.Remove(kept);              // cancel it too: nothing to flush
        Assert.False(db.HasPendingWork);
    }

    [Fact]
    public async Task A_child_two_scheduled_owners_reach_survives_the_cancellation_of_one()
    {
        await using var db = await OpenTrackedAsync();
        await db.CreateTableAsync<Owner>(CancellationToken.None);
        await db.CreateTableAsync<Passport>(CancellationToken.None);
        var first = new Owner { Label = "first" };
        var second = new Owner { Label = "second" };
        var shared = new Passport { Number = "shared" };
        typeof(Owner).GetProperty(nameof(Owner.Passport))!.SetValue(first, shared);
        typeof(Owner).GetProperty(nameof(Owner.Passport))!.SetValue(second, shared);
        db.Persist(first);
        db.Persist(second);

        db.Remove(first);                        // cancels first; shared still has a scheduled owner
        Assert.True(db.HasPendingWork);
        Assert.Equal(new FlushSummary(2, 0, 0), await db.FlushAsync(CancellationToken.None));
        Assert.Equal(second.Id, shared.OwnerId);
    }

    [Fact]
    public async Task Types_in_a_dependency_cycle_share_a_rank_so_the_schedule_decides()
    {
        await using var db = await OpenTrackedAsync();
        await db.CreateTableAsync<Left>(CancellationToken.None);
        await db.CreateTableAsync<Right>(CancellationToken.None);
        var options = new DbOptions { Dialect = new RecordingDialectForTests(new SqliteDialect()), Tracking = TrackingMode.Tracked };
        await using var recording = await Db.OpenAsync(fixture.ConnectionString, options, CancellationToken.None);
        var dialect = (RecordingDialectForTests)options.Dialect;

        recording.Persist(new Right { Label = "r" });
        recording.Persist(new Left { Label = "l" });
        await recording.FlushAsync(CancellationToken.None);
        Assert.Equal(["Right", "Left"], dialect.Inserted);   // no navigation set: schedule order inside the cycle
    }

    [Fact]
    public void An_empty_cascade_declaration_is_refused()
    {
        var error = Assert.Throws<MappingException>(() => new EntityMapLoader().Load<EmptyCascade>());
        Assert.Contains(error.Errors, e => e.Code == "MAP-025");
    }

    [Table("uow_lefts")]
    public sealed class Left
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [Column]
        [ForeignKey(typeof(Right))]
        public long? RightId { get; set; }

        [ManyToOne(nameof(RightId))]
        public Right? Right { get; private set; }
    }

    [Table("uow_rights")]
    public sealed class Right
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [Column]
        [ForeignKey(typeof(Left))]
        public long? LeftId { get; set; }

        [ManyToOne(nameof(LeftId))]
        public Left? Left { get; private set; }
    }

    /// <summary>Records the entity names of the inserts rendered, in order — the seam the flush-cases observe.</summary>
    private sealed class RecordingDialectForTests(IDialect inner) : IDialect
    {
        public List<string> Inserted { get; } = [];

        public string InsertSql(EntityMap map)
        {
            Inserted.Add(map.EntityType.Name);
            return inner.InsertSql(map);
        }

        public System.Data.Common.DbConnection CreateConnection(string connectionString) => inner.CreateConnection(connectionString);
        public string QuoteIdentifier(string identifier) => inner.QuoteIdentifier(identifier);
        public string CreateTableSql(EntityMap map) => inner.CreateTableSql(map);
        public IReadOnlyList<string> CreateIndexSql(EntityMap map) => inner.CreateIndexSql(map);
        public string CreateViewSql(EntityMap map) => inner.CreateViewSql(map);
        public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => inner.LimitOffsetClause(limitParameter, offsetParameter);
        public bool PagingRequiresOrderBy => inner.PagingRequiresOrderBy;
        public bool SupportsRowValueIn => inner.SupportsRowValueIn;
        public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter) => inner.SelectSql(select, bindParameter);
        public bool SupportsArrayParameters => inner.SupportsArrayParameters;
        public bool BindsTemporalsNatively => inner.BindsTemporalsNatively;
        public string ColumnsInfoSql => inner.ColumnsInfoSql;
        public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt) => inner.IsDeclaredTypeCompatible(declaredType, clrType, enumAsInt);
        public string ViewDefinitionSql => inner.ViewDefinitionSql;
        public string IndexesInfoSql => inner.IndexesInfoSql;
        public string StorageType(PropertyMap property) => inner.StorageType(property);
        public bool SupportsMaterializedViews => inner.SupportsMaterializedViews;
        public bool SupportsProcedures => inner.SupportsProcedures;
        public bool SupportsTransactionalDdl => inner.SupportsTransactionalDdl;
        public System.Data.Common.DbTransaction BeginMigrationRunLock(System.Data.Common.DbConnection connection) => inner.BeginMigrationRunLock(connection);
        public string VersionTableSql => inner.VersionTableSql;
        public string RenameTableSql(string fromName, string toName) => inner.RenameTableSql(fromName, toName);
        public string RenameColumnSql(string table, string fromName, string toName) => inner.RenameColumnSql(table, fromName, toName);
        public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql) => inner.AddColumnSql(table, column, storageType, nullable, defaultSql);
        public string DropColumnSql(string table, string column) => inner.DropColumnSql(table, column);
        public string DropTableSql(string table) => inner.DropTableSql(table);
        public string DropIndexSql(string table, string index) => inner.DropIndexSql(table, index);
        public string UpdateSql(EntityMap map) => inner.UpdateSql(map);
        public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties) => inner.UpdateOnlySql(map, properties);
        public string DeleteSql(EntityMap map, bool checkVersion) => inner.DeleteSql(map, checkVersion);
    }

    [Table("uow_empty_cascade")]
    public sealed class EmptyCascade
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [OneToMany(nameof(TransactionDetail.TransactionId))]
        [Cascade(CascadeOperations.None)]
        public IReadOnlyList<TransactionDetail> Details { get; private set; } = [];
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

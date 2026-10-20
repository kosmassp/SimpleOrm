using SimpleOrm.Sample.Models;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The SQL Server dialect against a real LocalDB database (ADR-0024): the same
/// fixture entities, schema created from metadata (ADR-0011), no mocks. Each test
/// skips where LocalDB is absent, so CI still needs only the .NET SDK. Covers the
/// seam points ADR-0023 predicted live: scope_identity key write-back,
/// OFFSET/FETCH paging (with and without ORDER BY), the composite-membership
/// EXISTS rewrite, sp_getapplock migrations, and the datetimeoffset date
/// convention (markerless datetime2 refuses with VAL-020).
/// </summary>
[Collection(SqlServerCollection.Name)]
public sealed class SqlServerIntegrationTests(SqlServerFixture fixture)
{
    private static readonly DbOptions Options = new() { Dialect = new SqlServerDialect() };

    private static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    private async Task<Db> OpenAsync()
    {
        var db = await Db.OpenAsync(fixture.ConnectionString, Options, CancellationToken.None);
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

    private static async Task<User> InsertUserAsync(Db db, string name)
    {
        var user = new User { Name = name, Email = name + "@example.test", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(user, CancellationToken.None);
        return user;
    }

    [SqlServerFact]
    public async Task Insert_writes_back_the_identity_key_and_values_roundtrip()
    {
        await using var db = await OpenAsync();
        var user = await InsertUserAsync(db, "Ada");
        Assert.True(user.Id > 0);

        var read = await db.GetAsync<User>(user.Id, CancellationToken.None);
        Assert.Equal("Ada", read.Name);
        Assert.Equal(TestDb.SeedTime, read.CreatedAtUtc);
        Assert.Equal(DateTimeKind.Utc, read.CreatedAtUtc.Kind);   // datetimeoffset reads back marked
        Assert.Null(read.UpdatedAtUtc);
    }

    private sealed record AvatarArgs(byte[] Avatar);

    private static readonly Query<AvatarArgs, long> ProfileIdsByAvatar = Query.Inline(
        "select id from user_profiles where avatar = @Avatar");

    [SqlServerFact]
    public async Task Bytes_round_trip_through_crud_criteria_and_registry_queries()
    {
        // ADR-0040 add.1: a NUL, a backslash, 0xff, and an invalid UTF-8 sequence, then bytes
        // spelling an escape literal — what text binding would corrupt.
        byte[] first = [0x00, 0x5c, 0xff, 0x27, 0x41, 0xc3, 0x28, 0x0a];
        byte[] second = [0x5c, 0x78, 0x34, 0x31, 0x5c, 0x5c, 0x00];
        await using var db = await OpenAsync();
        var user = await InsertUserAsync(db, "Ada");
        var profile = new UserProfile { UserId = user.Id, Avatar = first, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(profile, CancellationToken.None);

        Assert.Equal(first, (await db.GetAsync<UserProfile>(profile.Id, CancellationToken.None)).Avatar);
        Assert.Single(await db.Query<UserProfile>()
            .Where(Criteria.Eq("Avatar", first)).ToListAsync(CancellationToken.None));
        Assert.Equal([profile.Id], await db.QueryAsync(ProfileIdsByAvatar, new AvatarArgs(first), CancellationToken.None));

        profile.Avatar = second;
        await db.UpdateOnlyAsync(profile, [nameof(UserProfile.Avatar)], CancellationToken.None);
        Assert.Equal(second, (await db.GetAsync<UserProfile>(profile.Id, CancellationToken.None)).Avatar);

        // A NULL into varbinary(max) — the probe for the ruling waiting for a SQL Server engine
        // (spec/dialects.md): an untyped null parameter may arrive as nvarchar and refuse (error 257).
        profile.Avatar = null;
        await db.UpdateAsync(profile, CancellationToken.None);
        Assert.Null((await db.GetAsync<UserProfile>(profile.Id, CancellationToken.None)).Avatar);
    }

    [SqlServerFact]
    public async Task Criteria_pages_with_and_without_explicit_ordering()
    {
        await using var db = await OpenAsync();
        foreach (var name in new[] { "a", "b", "c", "d", "e" })
        {
            await InsertUserAsync(db, name);
        }

        // No ORDER BY: the renderer supplies the placeholder ordering OFFSET/FETCH needs.
        var unordered = await db.Query<User>().Limit(2).ToListAsync(CancellationToken.None);
        Assert.Equal(2, unordered.Count);

        var page = await db.Query<User>()
            .Where(Criteria.Or(Criteria.Eq("Name", "b"), Criteria.In("Name", ["c", "d", "e"])))
            .OrderBy("Name", SortOrder.Desc)
            .Limit(2)
            .Offset(1)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(["d", "c"], page.Select(u => u.Name));
    }

    [SqlServerFact]
    public async Task Reserved_word_identifiers_work_because_everything_brackets()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<ReservedOrder>(CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(Query.Inline("delete from [order]"), EmptyArgs.Value, CancellationToken.None);

        var order = new ReservedOrder { Description = "brackets everywhere" };
        await db.InsertAsync(order, CancellationToken.None);

        var read = await db.Query<ReservedOrder>()
            .Where(Criteria.Eq("Description", "brackets everywhere"))
            .OrderBy("Description")
            .SingleAsync(CancellationToken.None);
        Assert.Equal(order.Id, read.Id);
        Assert.Equal("brackets everywhere", (await db.GetAsync<ReservedOrder>(order.Id, CancellationToken.None)).Description);
    }

    [SqlServerFact]
    public async Task Update_and_delete_enforce_the_version_column()
    {
        await using var db = await OpenAsync();
        var user = await InsertUserAsync(db, "Ada");
        var transaction = new Transaction
        {
            UserId = user.Id,
            Status = TransactionStatus.Pending,
            Amount = 12.34m,
            CreatedAtUtc = TestDb.SeedTime,
        };
        await db.InsertAsync(transaction, CancellationToken.None);

        var stale = await db.GetAsync<Transaction>(transaction.Id, CancellationToken.None);
        Assert.Equal(12.34m, stale.Amount);
        Assert.Equal(TransactionStatus.Pending, stale.Status);

        transaction.Amount = 56.78m;
        await db.UpdateAsync(transaction, CancellationToken.None);

        stale.Amount = 99m;
        await Assert.ThrowsAsync<ConcurrencyException>(() => db.UpdateAsync(stale, CancellationToken.None));
        await Assert.ThrowsAsync<ConcurrencyException>(() => db.DeleteAsync<Transaction>(stale, CancellationToken.None));

        await db.DeleteAsync<Transaction>(transaction, CancellationToken.None);
        var missing = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.DeleteAsync<Transaction>(transaction.Id, CancellationToken.None));
        Assert.Equal("CRUD-001", missing.Code);
    }

    private static readonly Query<EmptyArgs, long> CountTriggerLog = Query.Inline("select count(id) from trigger_log");

    [SqlServerFact]
    public async Task Generated_writes_judge_their_own_row_count_under_a_trigger()
    {
        // ADR-0040 add.3 (item 3): an AFTER trigger that inserts two log rows per
        // statement without SET NOCOUNT ON — the shape Clarification 22 measured.
        // The driver then reports the trigger's rows with the statement's, so a
        // stale version-checked write (0 rows of its own, 2 from the trigger) would
        // pass for a success. The @@rowcount rendering judges the statement alone.
        await using var db = await OpenAsync();
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline(
                "if object_id(N'trigger_log', N'U') is null "
                + "create table trigger_log (id bigint identity(1,1) primary key, event nvarchar(20) not null)"),
            EmptyArgs.Value, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline(
                "create or alter trigger trg_transactions_audit on transactions after update, delete as begin "
                + "insert into trigger_log (event) values (N'first'); insert into trigger_log (event) values (N'second'); end"),
            EmptyArgs.Value, CancellationToken.None);
        try
        {
            var user = await InsertUserAsync(db, "Ada");
            var transaction = new Transaction
            {
                UserId = user.Id,
                Status = TransactionStatus.Pending,
                Amount = 12.34m,
                CreatedAtUtc = TestDb.SeedTime,
            };
            await db.InsertAsync(transaction, CancellationToken.None);
            var stale = await db.GetAsync<Transaction>(transaction.Id, CancellationToken.None);

            transaction.Amount = 56.78m;
            await db.UpdateAsync(transaction, CancellationToken.None);               // one row of its own, two of the trigger's
            Assert.Equal(1, transaction.Version);
            transaction.Note = "column list";
            await db.UpdateOnlyAsync(transaction, [nameof(Transaction.Note)], CancellationToken.None);
            Assert.Equal(2, transaction.Version);

            // The stale writes affect no row of their own; the trigger still fires
            // (once per statement, rows or not) and logs two. Without the rendering
            // the driver reports 2 and the conflict goes unnoticed.
            stale.Amount = 99m;
            await Assert.ThrowsAsync<ConcurrencyException>(() => db.UpdateAsync(stale, CancellationToken.None));
            await Assert.ThrowsAsync<ConcurrencyException>(() => db.DeleteAsync<Transaction>(stale, CancellationToken.None));
            Assert.Equal(0, stale.Version);

            // A registry command on the same connection keeps the driver's count:
            // the batch restored SET NOCOUNT, which is session-scoped on SQL Server.
            Command<EmptyArgs> touch = Query.Inline("update users set display_name = display_name");
            Assert.Equal(1, await db.ExecuteAsync(touch, EmptyArgs.Value, CancellationToken.None));

            await db.DeleteAsync<Transaction>(transaction, CancellationToken.None);
            var missing = await Assert.ThrowsAsync<SimpleOrmException>(
                () => db.DeleteAsync<Transaction>(transaction.Id, CancellationToken.None));
            Assert.Equal("CRUD-001", missing.Code);

            // Six statements fired the trigger: two updates, two stale writes, the
            // delete, and the by-key delete of the gone row.
            Assert.Equal(12, await db.QuerySingleAsync(CountTriggerLog, EmptyArgs.Value, CancellationToken.None));
        }
        finally
        {
            await db.ExecuteAsync<EmptyArgs>(
                Query.Inline("drop trigger trg_transactions_audit"), EmptyArgs.Value, CancellationToken.None);
        }
    }

    [SqlServerFact]
    public async Task Transaction_scope_commits_and_rolls_back()
    {
        await using var db = await OpenAsync();

        await using (await db.BeginAsync(CancellationToken.None))
        {
            await InsertUserAsync(db, "Rolled");
        }   // disposing an uncommitted scope rolls back

        Assert.Equal(0, await db.QuerySingleAsync(CountUsers, EmptyArgs.Value, CancellationToken.None));

        await using (var scope = await db.BeginAsync(CancellationToken.None))
        {
            await InsertUserAsync(db, "Committed");
            await scope.CommitAsync(CancellationToken.None);
        }

        Assert.Equal(1, await db.QuerySingleAsync(CountUsers, EmptyArgs.Value, CancellationToken.None));
    }

    [SqlServerFact]
    public async Task Loading_works_explicitly_in_batch_and_through_the_link()
    {
        await using var db = await OpenAsync();
        var ada = await InsertUserAsync(db, "Ada");
        var grace = await InsertUserAsync(db, "Grace");
        foreach (var (owner, amount) in new[] { (ada, 1m), (ada, 2m), (grace, 3m) })
        {
            await db.InsertAsync(
                new Transaction { UserId = owner.Id, Amount = amount, CreatedAtUtc = TestDb.SeedTime },
                CancellationToken.None);
        }

        var admin = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(admin, CancellationToken.None);
        await db.InsertAsync(
            new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        var users = await db.Query<User>().OrderBy("Name").ToListAsync(CancellationToken.None);
        Assert.Throws<SimpleOrmException>(() => users[0].Transactions.Count);   // REL-004: unloaded access is a bug

        await db.LoadEachAsync(users, nameof(User.Transactions), CancellationToken.None);
        Assert.Equal(2, users.Single(u => u.Id == ada.Id).Transactions.Count);
        Assert.Single(users.Single(u => u.Id == grace.Id).Transactions);

        await db.LoadAsync(users.Single(u => u.Id == ada.Id), nameof(User.Roles), CancellationToken.None);
        Assert.Equal("admin", users.Single(u => u.Id == ada.Id).Roles.Single().Name);
    }

    [SqlServerFact]
    public async Task Subselect_includes_page_correctly_on_offset_fetch()
    {
        await using var db = await OpenAsync();
        foreach (var name in new[] { "a", "b", "c" })
        {
            var user = await InsertUserAsync(db, name);
            await db.InsertAsync(
                new Transaction { UserId = user.Id, Amount = 1m, CreatedAtUtc = TestDb.SeedTime },
                CancellationToken.None);
        }

        // The navigation filter is IN (select id from the paged root): ORDER BY +
        // OFFSET/FETCH inside a subquery — legal exactly because the page is there.
        var page = await db.Query<User>()
            .OrderBy("Name")
            .Limit(2)
            .Include(nameof(User.Transactions))
            .Fetch(FetchMode.SubSelect)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(["a", "b"], page.Select(u => u.Name));
        Assert.All(page, u => Assert.Single(u.Transactions));
    }

    [SqlServerFact]
    public async Task Composite_key_subselect_rewrites_membership_as_exists()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<CompParent>(CancellationToken.None);
        await db.CreateTableAsync<CompChild>(CancellationToken.None);
        foreach (var table in new[] { "comp_children", "comp_parents" })
        {
            await db.ExecuteAsync<EmptyArgs>(Query.Inline("delete from " + table), EmptyArgs.Value, CancellationToken.None);
        }

        foreach (var (a, b) in new[] { (1L, 1L), (1L, 2L), (2L, 1L) })
        {
            await db.InsertAsync(new CompParent { PartA = a, PartB = b, Label = $"{a}/{b}" }, CancellationToken.None);
            await db.InsertAsync(new CompChild { ParentA = a, ParentB = b, Tag = $"child of {a}/{b}" }, CancellationToken.None);
        }

        // The owner filter is (parent_a, parent_b) in (select part_a, part_b …) —
        // no row-value IN on SQL Server, so this runs only through the EXISTS rewrite.
        var parents = await db.Query<CompParent>()
            .Where(Criteria.Eq("PartA", 1L))
            .Include(nameof(CompParent.Children))
            .Fetch(FetchMode.SubSelect)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(2, parents.Count);
        Assert.All(parents, p => Assert.Equal($"child of {p.PartA}/{p.PartB}", p.Children.Single().Tag));
    }

    [SqlServerFact]
    public async Task Migrations_apply_record_and_revert_under_the_applock()
    {
        await using var db = await Db.OpenAsync(fixture.ConnectionString, Options, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline("if object_id(N'mig_widgets', N'U') is not null drop table mig_widgets"),
            EmptyArgs.Value, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline("if object_id(N'schema_version', N'U') is not null delete from schema_version"),
            EmptyArgs.Value, CancellationToken.None);

        var runner = new MigrationRunner(db, [new V0001(), new V0002()]);
        Assert.Equal(2, await runner.MigrateAsync(CancellationToken.None));
        Assert.Equal(0, await runner.MigrateAsync(CancellationToken.None));
        Assert.False(await runner.HasPendingAsync(CancellationToken.None));

        // The manual Down() override reverts V0002 (no snapshots on SQL Server yet).
        Assert.Equal(1, await runner.MigrateDownAsync(1, CancellationToken.None));
        Assert.True(await runner.HasPendingAsync(CancellationToken.None));
        Assert.Equal(1, await runner.MigrateAsync(CancellationToken.None));
    }

    [SqlServerFact]
    public async Task Markerless_datetime2_refuses_with_VAL020()
    {
        await using var db = await Db.OpenAsync(fixture.ConnectionString, Options, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline(
                "if object_id(N'legacy_stamps', N'U') is not null drop table legacy_stamps; "
                + "create table legacy_stamps (id bigint identity(1,1) primary key, stamped_at datetime2 not null); "
                + "insert into legacy_stamps (stamped_at) values ('2026-01-01T00:00:00')"),
            EmptyArgs.Value, CancellationToken.None);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAllAsync<LegacyStamp>(CancellationToken.None));
        Assert.Equal("VAL-020", refused.Code);
    }

    // --- fixtures -------------------------------------------------------------------

    /// <summary>Reserved words on purpose: table <c>order</c>, column <c>desc</c> (ADR-0024 quoting).</summary>
    [Table("order")]
    public sealed class ReservedOrder
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column("desc")]
        public string? Description { get; set; }
    }

    [Table("comp_parents")]
    public sealed class CompParent
    {
        [Key]
        [Column]
        public long PartA { get; set; }

        [Key]
        [Column]
        public long PartB { get; set; }

        [Column]
        public string? Label { get; set; }

        /// <summary>Composite owner key: the FK list pairs with (PartA, PartB) in key order.</summary>
        [OneToMany(nameof(CompChild.ParentA), nameof(CompChild.ParentB))]
        public IReadOnlyList<CompChild> Children { get; private set; } = [];
    }

    [Table("comp_children")]
    public sealed class CompChild
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long ParentA { get; set; }

        [Column]
        public long ParentB { get; set; }

        [Column]
        public string? Tag { get; set; }
    }

    [Table("legacy_stamps")]
    public sealed class LegacyStamp
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public DateTime StampedAt { get; set; }
    }

    [Table("mig_widgets")]
    public sealed class MigWidget
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Name { get; set; }

        [Column]
        public string? Note { get; set; }
    }

    public sealed class V0001_CreateMigWidgets : TableMigration<MigWidget>
    {
        // Frozen to literal SQL (ADR-0013): V0002 adds note, so V0001 must stay
        // the shape users had then — metadata would already include the column.
        public override void Action(TableActions actions) => actions.Sql(
            "create table mig_widgets (id bigint identity(1,1) primary key, name nvarchar(max) not null)");
    }

    public sealed class V0002_AddNote : TableMigration<MigWidget>
    {
        public override void Action(TableActions actions) => actions.AddColumn("note", "nvarchar(max)");

        public override void Down(TableActions actions) => actions.RemoveColumn("note");
    }

    public sealed class V0001 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<V0001_CreateMigWidgets>();
    }

    public sealed class V0002 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<V0002_AddNote>();
    }
}

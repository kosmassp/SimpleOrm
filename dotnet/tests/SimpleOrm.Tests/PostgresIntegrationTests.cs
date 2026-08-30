using SimpleOrm.Postgres;
using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The PostgreSQL dialect against a real local server (ADR-0025): same fixture
/// entities, schema from metadata (ADR-0011), no mocks; skips as a group where
/// no server answers. Beyond the shared dialect suite, this pins the seams that
/// exist *for* Postgres: native array parameters (<c>= any(@ids)</c>, one typed
/// parameter, empty matches no rows), native temporal binding
/// (<c>timestamptz >= @p</c> — a text parameter would refuse), row-value IN for
/// composite subselects, real materialized views, the advisory-lock migration
/// run, and the VAL-020 refusal on markerless <c>timestamp</c>.
/// </summary>
[Collection(PostgresCollection.Name)]
public sealed class PostgresIntegrationTests(PostgresFixture fixture)
{
    private static readonly DbOptions Options = new() { Dialect = new PostgresDialect() };

    private static readonly Query<EmptyArgs, long> CountUsers = Query.Inline("select count(id) from users");

    private sealed record IdsArgs(IReadOnlyList<long> Ids);

    private static readonly Query<IdsArgs, User> UsersByIds = Query.Inline(
        "select id, name, email, display_name, created_at, updated_at from users where id = any(@Ids) order by id");

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

    [PostgresFact]
    public async Task Insert_writes_back_the_key_via_returning_and_values_roundtrip()
    {
        await using var db = await OpenAsync();
        var user = await InsertUserAsync(db, "Ada");
        Assert.True(user.Id > 0);

        var read = await db.GetAsync<User>(user.Id, CancellationToken.None);
        Assert.Equal("Ada", read.Name);
        Assert.Equal(TestDb.SeedTime, read.CreatedAtUtc);
        Assert.Equal(DateTimeKind.Utc, read.CreatedAtUtc.Kind);   // timestamptz reads back marked
        Assert.Null(read.UpdatedAtUtc);
    }

    [PostgresFact]
    public async Task Array_parameters_bind_as_one_typed_parameter()
    {
        await using var db = await OpenAsync();
        var ada = await InsertUserAsync(db, "Ada");
        var grace = await InsertUserAsync(db, "Grace");
        await InsertUserAsync(db, "Linus");

        // The SQL says = any(@Ids), the collection binds as ONE bigint[] parameter
        // (§7.12 realized) — no placeholder expansion, no SQL rewriting.
        var picked = await db.QueryAsync(UsersByIds, new IdsArgs([ada.Id, grace.Id]), CancellationToken.None);
        Assert.Equal(["Ada", "Grace"], picked.Select(u => u.Name));

        // An empty collection is a typed empty array: matches no rows, never errors.
        Assert.Empty(await db.QueryAsync(UsersByIds, new IdsArgs([]), CancellationToken.None));
    }

    [PostgresFact]
    public async Task Native_temporal_binding_compares_against_timestamptz()
    {
        await using var db = await OpenAsync();
        await InsertUserAsync(db, "Ada");

        // timestamptz >= text refuses on Postgres — this passes only because the
        // DateTime binds natively (BindsTemporalsNatively, ADR-0025).
        var since = await db.Query<User>()
            .Where(Criteria.Ge("CreatedAtUtc", TestDb.SeedTime.AddDays(-1)))
            .ToListAsync(CancellationToken.None);
        Assert.Single(since);
    }

    [PostgresFact]
    public async Task Criteria_pages_and_filters()
    {
        await using var db = await OpenAsync();
        foreach (var name in new[] { "a", "b", "c", "d", "e" })
        {
            await InsertUserAsync(db, name);
        }

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

    [PostgresFact]
    public async Task Reserved_word_identifiers_work_because_everything_quotes()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<ReservedOrder>(CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(Query.Inline("delete from \"order\""), EmptyArgs.Value, CancellationToken.None);

        var order = new ReservedOrder { Description = "quotes everywhere" };
        await db.InsertAsync(order, CancellationToken.None);

        var read = await db.Query<ReservedOrder>()
            .Where(Criteria.Eq("Description", "quotes everywhere"))
            .OrderBy("Description")
            .SingleAsync(CancellationToken.None);
        Assert.Equal(order.Id, read.Id);
    }

    [PostgresFact]
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

        await db.DeleteAsync<Transaction>(transaction, CancellationToken.None);
        var missing = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.DeleteAsync<Transaction>(transaction.Id, CancellationToken.None));
        Assert.Equal("CRUD-001", missing.Code);
    }

    [PostgresFact]
    public async Task Transaction_scope_commits_and_rolls_back()
    {
        await using var db = await OpenAsync();

        await using (await db.BeginAsync(CancellationToken.None))
        {
            await InsertUserAsync(db, "Rolled");
        }

        Assert.Equal(0, await db.QuerySingleAsync(CountUsers, EmptyArgs.Value, CancellationToken.None));

        await using (var scope = await db.BeginAsync(CancellationToken.None))
        {
            await InsertUserAsync(db, "Committed");
            await scope.CommitAsync(CancellationToken.None);
        }

        Assert.Equal(1, await db.QuerySingleAsync(CountUsers, EmptyArgs.Value, CancellationToken.None));
    }

    [PostgresFact]
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
        Assert.Throws<SimpleOrmException>(() => users[0].Transactions.Count);   // REL-004

        await db.LoadEachAsync(users, nameof(User.Transactions), CancellationToken.None);
        Assert.Equal(2, users.Single(u => u.Id == ada.Id).Transactions.Count);
        Assert.Single(users.Single(u => u.Id == grace.Id).Transactions);

        await db.LoadAsync(users.Single(u => u.Id == ada.Id), nameof(User.Roles), CancellationToken.None);
        Assert.Equal("admin", users.Single(u => u.Id == ada.Id).Roles.Single().Name);
    }

    [PostgresFact]
    public async Task Composite_key_subselect_uses_native_row_value_in()
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

        // (parent_a, parent_b) in (select part_a, part_b …) — the row-value form
        // SQL Server has to rewrite renders natively here (SupportsRowValueIn).
        var parents = await db.Query<CompParent>()
            .Where(Criteria.Eq("PartA", 1L))
            .Include(nameof(CompParent.Children))
            .Fetch(FetchMode.SubSelect)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(2, parents.Count);
        Assert.All(parents, p => Assert.Equal($"child of {p.PartA}/{p.PartB}", p.Children.Single().Tag));
    }

    [PostgresFact]
    public async Task Materialized_views_create_and_read()
    {
        await using var db = await OpenAsync();
        await InsertUserAsync(db, "Ada");
        await InsertUserAsync(db, "Grace");

        // Alive for the first time (ADR-0008 called them dormant until a dialect
        // with them arrived): created WITH DATA, read like any relation.
        await db.CreateViewAsync<NamedUser>(CancellationToken.None);
        var rows = await db.QueryAllAsync<NamedUser>(CancellationToken.None);
        Assert.Equal(["Ada", "Grace"], rows.Select(r => r.Name).OrderBy(n => n, StringComparer.Ordinal));
    }

    [PostgresFact]
    public async Task Migrations_apply_record_and_revert_under_the_advisory_lock()
    {
        await using var db = await Db.OpenAsync(fixture.ConnectionString, Options, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline("drop table if exists mig_widgets; drop table if exists schema_version"),
            EmptyArgs.Value, CancellationToken.None);

        var runner = new MigrationRunner(db, [new V0001(), new V0002()]);
        Assert.Equal(2, await runner.MigrateAsync(CancellationToken.None));
        Assert.Equal(0, await runner.MigrateAsync(CancellationToken.None));
        Assert.False(await runner.HasPendingAsync(CancellationToken.None));

        Assert.Equal(1, await runner.MigrateDownAsync(1, CancellationToken.None));
        Assert.True(await runner.HasPendingAsync(CancellationToken.None));
        Assert.Equal(1, await runner.MigrateAsync(CancellationToken.None));
    }

    [PostgresFact]
    public async Task Markerless_timestamp_refuses_with_VAL020()
    {
        await using var db = await Db.OpenAsync(fixture.ConnectionString, Options, CancellationToken.None);
        await db.ExecuteAsync<EmptyArgs>(
            Query.Inline(
                "drop table if exists legacy_stamps; "
                + "create table legacy_stamps (id bigint generated by default as identity primary key, stamped_at timestamp not null); "
                + "insert into legacy_stamps (stamped_at) values ('2026-01-01T00:00:00')"),
            EmptyArgs.Value, CancellationToken.None);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.QueryAllAsync<LegacyStamp>(CancellationToken.None));
        Assert.Equal("VAL-020", refused.Code);
    }

    // --- fixtures -------------------------------------------------------------------

    /// <summary>Reserved words on purpose: table <c>order</c>, column <c>desc</c> (ADR-0025 quoting).</summary>
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

    /// <summary>A real materialized view over <c>users</c> — portable defining SQL.</summary>
    [MaterializedView("named_users", "select id, name from users")]
    public sealed class NamedUser
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Name { get; set; }
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
        // Frozen to literal SQL (ADR-0013): V0002 adds note.
        public override void Action(TableActions actions) => actions.Sql(
            "create table mig_widgets (id bigint generated by default as identity primary key, name text not null)");
    }

    public sealed class V0002_AddNote : TableMigration<MigWidget>
    {
        public override void Action(TableActions actions) => actions.AddColumn("note", "text");

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

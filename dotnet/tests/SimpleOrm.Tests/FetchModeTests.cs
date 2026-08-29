using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0022 add.1 (owner: "can it be configurable? … depends on the need"): the
/// three fetch modes — MultiQuery (default), SubSelect (IN over the root query),
/// Join (one SELECT with LEFT JOINs) — must produce **identical loaded graphs**;
/// they differ only in round trips and data shape. Join mode refuses paging
/// (REL-005) and multiple collections (REL-006) instead of going wrong quietly.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class FetchModeTests(SqliteFixture fixture)
{
    private static readonly FetchMode[] AllModes = [FetchMode.MultiQuery, FetchMode.SubSelect, FetchMode.Join];

    private static async Task<(User Ada, User Grace, Role Admin, Role Member)> SeedGraphAsync(Db db)
    {
        var ada = await TestDb.InsertUserAsync(db, $"FetchAda{Guid.NewGuid():N}", $"fetch-ada-{Guid.NewGuid():N}@example.com");
        var grace = await TestDb.InsertUserAsync(db, $"FetchGrace{Guid.NewGuid():N}", $"fetch-grace-{Guid.NewGuid():N}@example.com");
        foreach (var amount in new[] { 1m, 2m })
        {
            await db.InsertAsync(
                new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = amount, CreatedAtUtc = TestDb.SeedTime },
                CancellationToken.None);
        }

        await db.InsertAsync(
            new UserProfile { UserId = ada.Id, Bio = "graph", CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        var admin = new Role { Name = $"fa{Guid.NewGuid():N}", CreatedAtUtc = TestDb.SeedTime };
        var member = new Role { Name = $"fm{Guid.NewGuid():N}", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(admin, CancellationToken.None);
        await db.InsertAsync(member, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = member.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        return (ada, grace, admin, member);
    }

    [Fact]
    public async Task All_modes_load_the_identical_graph()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, _, _) = await SeedGraphAsync(db);

        foreach (var mode in AllModes)
        {
            var users = await db.Query<User>()
                .Where(Criteria.In(nameof(User.Id), ada.Id, grace.Id))
                .OrderBy(nameof(User.Id))
                .Include(nameof(User.Transactions), nameof(User.Profile))
                .Fetch(mode)
                .ToListAsync(CancellationToken.None);

            Assert.Equal(2, users.Count);
            Assert.Equal([1m, 2m], users[0].Transactions.Select(t => t.Amount));   // target-key order
            Assert.Equal("graph", users[0].Profile!.Bio);
            Assert.Empty(users[1].Transactions);
            Assert.Null(users[1].Profile);
        }
    }

    [Fact]
    public async Task Many_to_many_loads_identically_in_every_mode()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, admin, member) = await SeedGraphAsync(db);

        foreach (var mode in AllModes)
        {
            var users = await db.Query<User>()
                .Where(Criteria.In(nameof(User.Id), ada.Id, grace.Id))
                .OrderBy(nameof(User.Id))
                .Include(nameof(User.Roles))
                .Fetch(mode)
                .ToListAsync(CancellationToken.None);

            Assert.Equal([admin.Id, member.Id], users[0].Roles.Select(r => r.Id));
            Assert.Empty(users[1].Roles);
        }
    }

    [Fact]
    public async Task SubSelect_pages_correctly_and_loads_only_the_page()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, _, _) = await SeedGraphAsync(db);

        // Page of one: only Ada comes back, and only Ada's children load —
        // the subselect carries the root query's paging into the child query.
        var page = await db.Query<User>()
            .Where(Criteria.In(nameof(User.Id), ada.Id, grace.Id))
            .OrderBy(nameof(User.Id))
            .Limit(1)
            .Include(nameof(User.Transactions))
            .Fetch(FetchMode.SubSelect)
            .ToListAsync(CancellationToken.None);

        var only = Assert.Single(page);
        Assert.Equal(ada.Id, only.Id);
        Assert.Equal(2, only.Transactions.Count);
    }

    [Fact]
    public async Task Join_mode_refuses_paging_and_double_collections()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, _, _) = await SeedGraphAsync(db);

        var paged = await Assert.ThrowsAsync<SimpleOrmException>(() => db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), ada.Id))
            .Limit(5)
            .Include(nameof(User.Transactions))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None));
        Assert.Equal("REL-005", paged.Code);

        var cartesian = await Assert.ThrowsAsync<SimpleOrmException>(() => db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), ada.Id))
            .Include(nameof(User.Transactions), nameof(User.Roles))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None));
        Assert.Equal("REL-006", cartesian.Code);

        // One collection plus any number of to-one navigations is fine.
        var loaded = await db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), ada.Id))
            .Include(nameof(User.Transactions), nameof(User.Profile))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None);
        Assert.Equal(2, Assert.Single(loaded).Transactions.Count);
    }

    [Fact]
    public async Task Join_mode_pages_fine_with_only_to_one_includes()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, _, _) = await SeedGraphAsync(db);

        // To-one joins never multiply root rows, so paging is sound (ADR-0022
        // add.2); only a collection include refuses (REL-005).
        var page = await db.Query<User>()
            .Where(Criteria.In(nameof(User.Id), ada.Id, grace.Id))
            .OrderBy(nameof(User.Id))
            .Limit(1)
            .Include(nameof(User.Profile))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None);

        var only = Assert.Single(page);
        Assert.Equal(ada.Id, only.Id);
        Assert.Equal("graph", only.Profile!.Bio);
    }

    [Fact]
    public async Task Join_loaded_children_guard_their_own_unloaded_collections()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, _, _) = await SeedGraphAsync(db);

        // A join-loaded child is a database-read entity like any other: its own
        // collection navigations throw REL-004 until loaded (ADR-0022 add.2).
        var transactions = await db.Query<Transaction>()
            .Where(Criteria.Eq(nameof(Transaction.UserId), ada.Id))
            .Include(nameof(Transaction.User))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None);

        var user = transactions[0].User!;
        Assert.Equal("REL-004", Assert.Throws<SimpleOrmException>(() => user.Transactions.Count).Code);
    }

    [Fact]
    public async Task Join_mode_refuses_keyless_views_with_a_named_error()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<KeylessParentRow>(CancellationToken.None);
        await db.CreateViewAsync<KeylessChildTotal>(CancellationToken.None);
        await db.InsertAsync(new KeylessParentRow { Label = "k" }, CancellationToken.None);

        // The keyless target loads fine in MultiQuery; join mode needs identity
        // and refuses with a named code instead of an unnamed crash.
        var rows = await db.Query<KeylessParentRow>()
            .Include(nameof(KeylessParentRow.Children))
            .ToListAsync(CancellationToken.None);
        Assert.Empty(Assert.Single(rows).Children);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.Query<KeylessParentRow>()
            .Include(nameof(KeylessParentRow.Children))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None));
        Assert.Equal("REL-003", refused.Code);
    }

    [Fact]
    public async Task Join_mode_with_one_navigation_still_detects_duplicate_one_to_one_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<DriftOwner>(CancellationToken.None);
        await db.CreateTableAsync<DriftChild>(CancellationToken.None);
        var owner = new DriftOwner { Label = "drift" };
        await db.InsertAsync(owner, CancellationToken.None);
        await db.InsertAsync(new DriftChild { OwnerId = owner.Id, Tag = "first" }, CancellationToken.None);
        await db.InsertAsync(new DriftChild { OwnerId = owner.Id, Tag = "second" }, CancellationToken.None);

        // No unique index on the FK: drifted 1:1 data. With a single included
        // navigation there is no join fan-out, so raw row counting fires REL-002
        // exactly as MultiQuery does (ADR-0022 add.2).
        foreach (var mode in AllModes)
        {
            var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.Query<DriftOwner>()
                .Where(Criteria.Eq(nameof(DriftOwner.Id), owner.Id))
                .Include(nameof(DriftOwner.Single))
                .Fetch(mode)
                .ToListAsync(CancellationToken.None));
            Assert.Equal("REL-002", refused.Code);
        }
    }

    [Fact]
    public async Task Join_mode_deduplicates_roots_and_replaces_the_unloaded_guard()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, _, _) = await SeedGraphAsync(db);

        // Two transaction rows join into ONE root instance; the included
        // collection is real, the non-included one still guards (REL-004).
        var users = await db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), ada.Id))
            .Include(nameof(User.Transactions))
            .Fetch(FetchMode.Join)
            .ToListAsync(CancellationToken.None);

        var user = Assert.Single(users);
        Assert.Equal(2, user.Transactions.Count);
        Assert.Equal("REL-004", Assert.Throws<SimpleOrmException>(() => user.Roles.Count).Code);
    }

    // --- fixtures -------------------------------------------------------------------

    [Table("keyless_parent_rows")]
    public sealed class KeylessParentRow
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [OneToMany(nameof(KeylessChildTotal.ParentId))]
        public IReadOnlyList<KeylessChildTotal> Children { get; private set; } = [];
    }

    /// <summary>Keyless view target: legal metadata, loadable in MultiQuery, refused by join mode.</summary>
    [View("keyless_child_totals", "select -1 as parent_id, 0 as total")]
    public sealed class KeylessChildTotal
    {
        [Column]
        public long ParentId { get; set; }

        [Column]
        public long Total { get; set; }
    }

    [Table("drift_owners")]
    public sealed class DriftOwner
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        /// <summary>Declared one-to-one, but no unique index on the FK: drifted data is possible.</summary>
        [OneToOne(nameof(DriftChild.OwnerId))]
        public DriftChild? Single { get; private set; }
    }

    [Table("drift_children")]
    public sealed class DriftChild
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long OwnerId { get; set; }

        [Column]
        public string? Tag { get; set; }
    }
}

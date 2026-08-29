using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 2 milestone 3 (ADR-0021): explicit and batch loading against a real
/// database. Nothing loads implicitly — navigations stay empty/null until a
/// LoadAsync/LoadEachAsync call fills them; a batch is one query per navigation
/// (two for many-to-many), never one per entity.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class RelationshipLoadingTests(SqliteFixture fixture)
{
    private static async Task<(User Ada, User Grace, Transaction T1, Transaction T2, Transaction T3)> SeedAsync(Db db)
    {
        var ada = await TestDb.InsertUserAsync(db, $"LoadAda{Guid.NewGuid():N}", $"load-ada-{Guid.NewGuid():N}@example.com");
        var grace = await TestDb.InsertUserAsync(db, $"LoadGrace{Guid.NewGuid():N}", $"load-grace-{Guid.NewGuid():N}@example.com");
        var t1 = new Transaction { UserId = ada.Id, Status = TransactionStatus.Pending, Amount = 1m, CreatedAtUtc = TestDb.SeedTime };
        var t2 = new Transaction { UserId = ada.Id, Status = TransactionStatus.Completed, Amount = 2m, CreatedAtUtc = TestDb.SeedTime };
        var t3 = new Transaction { UserId = grace.Id, Status = TransactionStatus.Pending, Amount = 3m, CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(t1, CancellationToken.None);
        await db.InsertAsync(t2, CancellationToken.None);
        await db.InsertAsync(t3, CancellationToken.None);
        return (ada, grace, t1, t2, t3);
    }

    [Fact]
    public async Task Nothing_loads_until_asked_then_many_to_one_loads_and_shares_instances()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, t1, t2, _) = await SeedAsync(db);

        var transactions = await db.Query<Transaction>()
            .Where(Criteria.In(nameof(Transaction.Id), t1.Id, t2.Id))
            .OrderBy(nameof(Transaction.Id))
            .ToListAsync(CancellationToken.None);
        Assert.All(transactions, t => Assert.Null(t.User));               // default is unloaded

        await db.LoadEachAsync(transactions, nameof(Transaction.User), CancellationToken.None);
        Assert.All(transactions, t => Assert.Equal(ada.Id, t.User!.Id));
        Assert.Same(transactions[0].User, transactions[1].User);          // one batch, one instance per key
    }

    [Fact]
    public async Task One_to_many_batch_fills_each_owner_with_its_rows()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, t1, t2, t3) = await SeedAsync(db);
        var users = new[] { ada, grace };
        Assert.All(users, u => Assert.Empty(u.Transactions));             // default is unloaded

        await db.LoadEachAsync(users, nameof(User.Transactions), CancellationToken.None);
        Assert.Equal([t1.Id, t2.Id], ada.Transactions.Select(t => t.Id));  // ordered by target key
        Assert.Equal([t3.Id], grace.Transactions.Select(t => t.Id));
    }

    [Fact]
    public async Task Many_to_many_loads_through_the_link_in_two_visible_queries()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, _, _, _) = await SeedAsync(db);

        // Explicit ids whose key order and ordinal string order diverge:
        // 999999999 < 1000000000 numerically, but "1000000000" < "999999999"
        // ordinally — the contract is key order (ADR-0021 add.1).
        const long lowId = 999_999_999;
        const long highId = 1_000_000_000;
        await SchemaSync.ApplyAsync(
            db,
            [$"insert into roles (id, role_name, created_at) values ({lowId}, 'low-{Guid.NewGuid():N}', '2026-01-01T00:00:00Z')",
             $"insert into roles (id, role_name, created_at) values ({highId}, 'high-{Guid.NewGuid():N}', '2026-01-01T00:00:00Z')"],
            CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = highId, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = lowId, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = grace.Id, RoleId = lowId, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);

        await db.LoadEachAsync(new[] { ada, grace }, nameof(User.Roles), CancellationToken.None);
        Assert.Equal([lowId, highId], ada.Roles.Select(r => r.Id));   // key order, asserted unsorted
        Assert.Equal([lowId], grace.Roles.Select(r => r.Id));
    }

    [Fact]
    public async Task One_to_one_loads_single_or_null_and_refuses_duplicates()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, _, _, _) = await SeedAsync(db);
        await db.InsertAsync(
            new UserProfile { UserId = ada.Id, Bio = "First programmer", CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        await db.LoadEachAsync(new[] { ada, grace }, nameof(User.Profile), CancellationToken.None);
        Assert.Equal("First programmer", ada.Profile!.Bio);
        Assert.Null(grace.Profile);

        // The unique index enforces 1:1 in the database; simulate drift by hand
        // and the loader refuses with REL-002 instead of picking one silently.
        // (The fixture database is shared per collection: restore afterwards.)
        await SchemaSync.ApplyAsync(
            db,
            ["drop index ix_user_profiles_user_id",
             $"insert into user_profiles (user_id, bio, created_at) values ({ada.Id}, 'dup', '2026-01-01T00:00:00Z')"],
            CancellationToken.None);
        try
        {
            var refused = await Assert.ThrowsAsync<SimpleOrmException>(
                () => db.LoadAsync(ada, nameof(User.Profile), CancellationToken.None));
            Assert.Equal("REL-002", refused.Code);
        }
        finally
        {
            await SchemaSync.ApplyAsync(
                db,
                [$"delete from user_profiles where user_id = {ada.Id} and bio = 'dup'",
                 "create unique index if not exists ix_user_profiles_user_id on user_profiles (user_id)"],
                CancellationToken.None);
        }
    }

    [Fact]
    public async Task Composite_foreign_keys_load_through_or_and_tuples()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, _, _, _) = await SeedAsync(db);
        var admin = new Role { Name = "admin", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(admin, CancellationToken.None);
        await db.InsertAsync(new UserRole { UserId = ada.Id, RoleId = admin.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);
        await db.CreateTableAsync<GrantNote>(CancellationToken.None);
        var note = new GrantNote { UserId = ada.Id, RoleId = admin.Id, Note = "granted at onboarding" };
        await db.InsertAsync(note, CancellationToken.None);

        await db.LoadAsync(note, nameof(GrantNote.Grant), CancellationToken.None);
        Assert.Equal(ada.Id, note.Grant!.UserId);
        Assert.Equal(admin.Id, note.Grant.RoleId);
    }

    [Fact]
    public async Task Unknown_navigation_is_REL001_naming_what_is_declared()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, _, _, _) = await SeedAsync(db);
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.LoadAsync(ada, "NoSuchNavigation", CancellationToken.None));
        Assert.Equal("REL-001", refused.Code);
        Assert.Contains(nameof(User.Transactions), refused.Message);

        // A wrong name is a bug regardless of batch size: the empty batch validates too.
        var emptyBatch = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.LoadEachAsync(Array.Empty<User>(), "NoSuchNavigation", CancellationToken.None));
        Assert.Equal("REL-001", emptyBatch.Code);
    }

    [Fact]
    public async Task Unmapped_target_foreign_key_is_REL003_at_load_time()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<ShapeMismatchOwner>(CancellationToken.None);
        var owner = new ShapeMismatchOwner { Label = "shape" };
        await db.InsertAsync(owner, CancellationToken.None);

        // The FK property exists on the target CLR type (declaration-time check
        // passes) but is [Ignore]d — not a mapped column — so loading refuses.
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.LoadAsync(owner, nameof(ShapeMismatchOwner.Children), CancellationToken.None));
        Assert.Equal("REL-003", refused.Code);
    }

    [Fact]
    public async Task Include_eager_loads_with_the_query()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, grace, t1, t2, t3) = await SeedAsync(db);
        await db.InsertAsync(
            new UserProfile { UserId = ada.Id, Bio = "Included", CreatedAtUtc = TestDb.SeedTime },
            CancellationToken.None);

        // Requested eagerly → loaded automatically with the query (ADR-0022):
        // one root query + one batch load per included navigation.
        var users = await db.Query<User>()
            .Where(Criteria.In(nameof(User.Id), ada.Id, grace.Id))
            .OrderBy(nameof(User.Id))
            .Include(nameof(User.Transactions), nameof(User.Profile))
            .ToListAsync(CancellationToken.None);

        Assert.Equal([t1.Id, t2.Id], users[0].Transactions.Select(t => t.Id));
        Assert.Equal("Included", users[0].Profile!.Bio);
        Assert.Equal([t3.Id], users[1].Transactions.Select(t => t.Id));
        Assert.Null(users[1].Profile);

        // The single-row terminals eager-load too.
        var single = await db.Query<Transaction>()
            .Where(Criteria.Eq(nameof(Transaction.Id), t1.Id))
            .Include(nameof(Transaction.User))
            .SingleAsync(CancellationToken.None);
        Assert.Equal(ada.Id, single.User!.Id);

        // An unknown navigation refuses even when the query matches nothing.
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.Query<User>()
            .Where(Criteria.Eq(nameof(User.Id), -1))
            .Include("NoSuchNavigation")
            .ToListAsync(CancellationToken.None));
        Assert.Equal("REL-001", refused.Code);
    }

    [Fact]
    public async Task Unloaded_collection_access_on_a_queried_entity_throws_REL004()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, t1, t2, _) = await SeedAsync(db);

        // Read back from the database: the FKs prove children may exist, so an
        // unloaded collection read is a bug, not an empty result (ADR-0021 add.2).
        var queried = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        var refused = Assert.Throws<SimpleOrmException>(() => queried.Transactions.Count);
        Assert.Equal("REL-004", refused.Code);
        Assert.Contains(nameof(User.Transactions), refused.Message);

        // Loading replaces the guard; a legitimate empty result reads as empty.
        await db.LoadAsync(queried, nameof(User.Transactions), CancellationToken.None);
        Assert.Equal([t1.Id, t2.Id], queried.Transactions.Select(t => t.Id));
        await db.LoadAsync(queried, nameof(User.Roles), CancellationToken.None);
        Assert.Empty(queried.Roles);

        // An entity constructed by user code keeps its own initializer — a new
        // entity genuinely has nothing, no guard.
        var constructed = new User { Name = "New", Email = "new@example.com", CreatedAtUtc = TestDb.SeedTime };
        Assert.Empty(constructed.Transactions);
    }

    [Fact]
    public async Task Dead_link_loads_as_null_not_an_error()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var (ada, _, t1, _, _) = await SeedAsync(db);

        // The FK exists but its target row is gone: loading resolves to null —
        // "there is no real model to go there" (owner, ADR-0021 add.2). The
        // strict REL-004 guard is about *not loading*; a dead link *was* loaded.
        await SchemaSync.ApplyAsync(
            db, [$"delete from users where id = {ada.Id}"], CancellationToken.None);
        var orphaned = await db.GetAsync<Transaction>(t1.Id, CancellationToken.None);
        Assert.Equal(ada.Id, orphaned.UserId);                          // the FK is still there

        await db.LoadAsync(orphaned, nameof(Transaction.User), CancellationToken.None);
        Assert.Null(orphaned.User);
    }

    [Fact]
    public async Task Null_foreign_key_leaves_the_navigation_null()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<OptionalOwner>(CancellationToken.None);
        var orphan = new OptionalOwner { Label = "orphan" };
        await db.InsertAsync(orphan, CancellationToken.None);

        await db.LoadAsync(orphan, nameof(OptionalOwner.Owner), CancellationToken.None);
        Assert.Null(orphan.Owner);
    }

    // --- fixtures -------------------------------------------------------------------

    [Table("grant_notes")]
    public sealed class GrantNote
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long UserId { get; set; }

        [Column]
        public long RoleId { get; set; }

        [Column]
        public string? Note { get; set; }

        /// <summary>Composite-key target: the FK list pairs with (UserId, RoleId) in key order.</summary>
        [ManyToOne(nameof(UserId), nameof(RoleId))]
        public UserRole? Grant { get; private set; }
    }

    [Table("shape_children")]
    public sealed class ShapeChild
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        /// <summary>Exists on the CLR type — passes MAP-021 — but is not a mapped column.</summary>
        [Ignore]
        public long OwnerId { get; set; }
    }

    [Table("shape_mismatch_owners")]
    public sealed class ShapeMismatchOwner
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Label { get; set; }

        [OneToMany(nameof(ShapeChild.OwnerId))]
        public IReadOnlyList<ShapeChild> Children { get; private set; } = [];
    }

    [Table("optional_owners")]
    public sealed class OptionalOwner
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public long? UserId { get; set; }

        [Column]
        public string? Label { get; set; }

        [ManyToOne(nameof(UserId))]
        public User? Owner { get; private set; }
    }
}

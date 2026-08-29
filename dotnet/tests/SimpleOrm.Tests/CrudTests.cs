using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Milestone 7: generated Update/Delete, optimistic concurrency, and the client-GUID key strategy.</summary>
[Collection(SqliteCollection.Name)]
public sealed class CrudTests(SqliteFixture fixture)
{
    private static Transaction NewTransaction(long userId) => new()
    {
        UserId = userId,
        Status = TransactionStatus.Pending,
        Amount = 10m,
        CreatedAtUtc = TestDb.SeedTime,
    };

    [Fact]
    public async Task Update_writes_the_full_row_by_key()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        ada.Name = "Ada Lovelace";
        ada.UpdatedAtUtc = TestDb.SeedTime;
        await db.UpdateAsync(ada, CancellationToken.None);

        var loaded = await db.GetAsync<User>(ada.Id, CancellationToken.None);
        Assert.Equal("Ada Lovelace", loaded.Name);
        Assert.Equal(TestDb.SeedTime, loaded.UpdatedAtUtc);
    }

    [Fact]
    public async Task Update_of_missing_row_is_CRUD001()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ghost = new User { Id = 999_999, Name = "Ghost", Email = "ghost@example.com", CreatedAtUtc = TestDb.SeedTime };

        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.UpdateAsync(ghost, CancellationToken.None));
        Assert.Equal("CRUD-001", exception.Code);
    }

    [Fact]
    public async Task Versioned_update_increments_and_detects_conflicts()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        await db.InsertAsync(NewTransaction(ada.Id), CancellationToken.None);

        var first = await db.Query<Transaction>().Where(Criteria.Eq("UserId", ada.Id)).SingleAsync(CancellationToken.None);
        var stale = await db.GetAsync<Transaction>(first.Id, CancellationToken.None);
        Assert.Equal(0L, first.Version);

        first.Status = TransactionStatus.Completed;
        await db.UpdateAsync(first, CancellationToken.None);
        Assert.Equal(1L, first.Version);                             // bumped in memory (§7.16)

        first.Amount = 12m;
        await db.UpdateAsync(first, CancellationToken.None);         // sequential updates keep working
        Assert.Equal(2L, first.Version);

        stale.Amount = 99m;                                          // still version 0
        var conflict = await Assert.ThrowsAsync<ConcurrencyException>(
            () => db.UpdateAsync(stale, CancellationToken.None));
        Assert.Equal("CRUD-010", conflict.Code);

        var current = await db.GetAsync<Transaction>(first.Id, CancellationToken.None);
        Assert.Equal(12m, current.Amount);                           // the stale write changed nothing
        Assert.Equal(2L, current.Version);
    }

    [Fact]
    public async Task Delete_by_key_and_versioned_delete_by_entity()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");

        await db.DeleteAsync<User>(ada.Id, CancellationToken.None);
        Assert.Null(await db.GetOrDefaultAsync<User>(ada.Id, CancellationToken.None));

        var missing = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.DeleteAsync<User>(ada.Id, CancellationToken.None));
        Assert.Equal("CRUD-001", missing.Code);

        // Version-checked delete: a stale entity may not delete the row.
        var grace = await TestDb.InsertUserAsync(db, "Grace", "grace@example.com");
        await db.InsertAsync(NewTransaction(grace.Id), CancellationToken.None);
        var tx = await db.Query<Transaction>().Where(Criteria.Eq("UserId", grace.Id)).SingleAsync(CancellationToken.None);
        var staleTx = await db.GetAsync<Transaction>(tx.Id, CancellationToken.None);

        tx.Status = TransactionStatus.Cancelled;
        await db.UpdateAsync(tx, CancellationToken.None);            // bumps the row to version 1

        var conflict = await Assert.ThrowsAsync<ConcurrencyException>(
            () => db.DeleteAsync<Transaction>(staleTx, CancellationToken.None));
        Assert.Equal("CRUD-010", conflict.Code);

        await db.DeleteAsync<Transaction>(tx, CancellationToken.None);   // fresh version deletes fine
        Assert.Null(await db.GetOrDefaultAsync<Transaction>(tx.Id, CancellationToken.None));
    }

    [Fact]
    public async Task Composite_key_delete_by_tuple()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        var ada = await TestDb.InsertUserAsync(db, "Ada", "ada@example.com");
        var role = new Role { Name = "auditor", CreatedAtUtc = TestDb.SeedTime };
        await db.InsertAsync(role, CancellationToken.None);
        await db.InsertAsync(
            new UserRole { UserId = ada.Id, RoleId = role.Id, CreatedAtUtc = TestDb.SeedTime }, CancellationToken.None);

        await db.DeleteAsync<UserRole>((ada.Id, role.Id), CancellationToken.None);
        Assert.Null(await db.GetOrDefaultAsync<UserRole>((ada.Id, role.Id), CancellationToken.None));
    }

    [Fact]
    public async Task Update_guards_navigation_consistency_and_readonly_sources()
    {
        await using var db = await TestDb.OpenAsync(fixture);

        var readOnly = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.UpdateAsync(new UserTransactionTotal { UserName = "x" }, CancellationToken.None));
        Assert.Equal("CRUD-003", readOnly.Code);

        var readOnlyDelete = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.DeleteAsync<UserTransactionTotal>(1L, CancellationToken.None));
        Assert.Equal("CRUD-003", readOnlyDelete.Code);
    }

    [Table("guid_docs")]
    public sealed class GuidDoc
    {
        [Key]
        [Column]
        public Guid Id { get; set; }

        [Column]
        public required string Title { get; set; }
    }

    [Fact]
    public async Task Client_guid_key_strategy_round_trips()
    {
        await using var db = await TestDb.OpenAsync(fixture);
        await db.CreateTableAsync<GuidDoc>(CancellationToken.None);
        Command<EmptyArgs> clear = Query.Inline("delete from guid_docs");
        await db.ExecuteAsync(clear, EmptyArgs.Value, CancellationToken.None);

        var doc = new GuidDoc { Title = "spec" };
        Assert.Equal(Guid.Empty, doc.Id);
        await db.InsertAsync(doc, CancellationToken.None);
        Assert.NotEqual(Guid.Empty, doc.Id);                          // client-assigned on insert

        var loaded = await db.GetAsync<GuidDoc>(doc.Id, CancellationToken.None);
        Assert.Equal("spec", loaded.Title);

        loaded.Title = "spec v2";
        await db.UpdateAsync(loaded, CancellationToken.None);
        Assert.Equal("spec v2", (await db.GetAsync<GuidDoc>(doc.Id, CancellationToken.None)).Title);

        await db.DeleteAsync<GuidDoc>(doc.Id, CancellationToken.None);
        Assert.Null(await db.GetOrDefaultAsync<GuidDoc>(doc.Id, CancellationToken.None));
    }
}

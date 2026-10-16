using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 3 M3 (spec/hooks.md) beyond the hook-cases: the declaration rules
/// (<c>MAP-026</c>), a soft delete on a versioned entity (the entity form checks
/// the version, the by-key form refuses <c>CRUD-012</c>), a non-UTC clock, a
/// <c>DateTimeOffset</c> audit column, and a failed flush undoing a soft delete.
/// </summary>
[Collection(SqliteCollection.Name)]
public sealed class HooksTests(SqliteFixture fixture)
{
    private static readonly DateTime Clock = new(2026, 8, 29, 0, 0, 0, DateTimeKind.Utc);

    private async Task<Db> OpenAsync(TrackingMode mode = TrackingMode.Untracked, Func<DateTime>? clock = null)
    {
        await using (await TestDb.OpenAsync(fixture))
        {
        }

        return await Db.OpenAsync(
            fixture.ConnectionString,
            new DbOptions { Dialect = new SqliteDialect(), Tracking = mode, Clock = clock ?? (() => Clock) },
            CancellationToken.None);
    }

    [Fact]
    public void Hook_declarations_are_validated()
    {
        var loader = new EntityMapLoader();
        Assert.Equal(AuditRole.CreatedAt, loader.Load<User>().CreatedAtProperty!.Audit);
        Assert.Equal("updated_at", loader.Load<User>().UpdatedAtProperty!.ColumnName);
        Assert.Equal("is_deleted", loader.Load<Role>().SoftDeleteProperty!.ColumnName);

        foreach (var type in new[] { typeof(TextCreatedAt), typeof(NullableFlag), typeof(HookOnKey), typeof(TwoStamps), typeof(TwoHooksOnOne) })
        {
            var error = Assert.Throws<MappingException>(() => loader.Load(type));
            Assert.Contains(error.Errors, e => e.Code == "MAP-026");
        }
    }

    [Fact]
    public async Task A_versioned_soft_delete_checks_the_version_in_the_entity_form_and_refuses_the_key_form()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<Memo>(CancellationToken.None);
        var memo = new Memo { Text = "m" };
        await db.InsertAsync(memo, CancellationToken.None);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => db.DeleteAsync<Memo>(memo.Id, CancellationToken.None));
        Assert.Equal("CRUD-012", refused.Code);

        var stale = await db.GetAsync<Memo>(memo.Id, CancellationToken.None);
        memo.Text = "bumped";
        await db.UpdateAsync(memo, CancellationToken.None);                 // version 1
        var conflict = await Assert.ThrowsAsync<ConcurrencyException>(() => db.DeleteAsync<Memo>(stale, CancellationToken.None));
        Assert.Equal("CRUD-010", conflict.Code);

        await db.DeleteAsync<Memo>(memo, CancellationToken.None);
        Assert.True(memo.Deleted);
        Assert.Equal(2, memo.Version);
        Assert.Equal(Clock, memo.Touched);
        var row = await db.GetAsync<Memo>(memo.Id, CancellationToken.None);  // reads never filter on the flag
        Assert.True(row.Deleted);
        Assert.Equal(2, row.Version);
    }

    [Fact]
    public async Task The_clock_must_be_utc()
    {
        await using var db = await OpenAsync(clock: () => new DateTime(2026, 8, 29, 0, 0, 0, DateTimeKind.Local));
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => db.InsertAsync(new User { Name = "x", Email = $"x-{Guid.NewGuid():N}@example.com" }, CancellationToken.None));
        Assert.Equal("VAL-020", refused.Code);
    }

    [Fact]
    public async Task Audit_columns_may_be_date_time_offsets()
    {
        await using var db = await OpenAsync();
        await db.CreateTableAsync<Memo>(CancellationToken.None);
        var memo = new Memo { Text = "offset" };
        await db.InsertAsync(memo, CancellationToken.None);
        Assert.Equal(new DateTimeOffset(Clock), memo.Born);
        Assert.Null(memo.Touched);

        memo.Text = "again";
        await db.UpdateAsync(memo, CancellationToken.None);
        Assert.Equal(new DateTimeOffset(Clock), memo.Touched);
    }

    [Fact]
    public async Task A_failed_flush_undoes_a_soft_delete_too()
    {
        await using var db = await OpenAsync(TrackingMode.Tracked);
        var role = new Role { Name = $"r{Guid.NewGuid():N}" };
        await db.InsertAsync(role, CancellationToken.None);
        var duplicate = new User { Name = "dup", Email = "ada@example.com" };
        await db.InsertAsync(new User { Name = "ada", Email = "ada@example.com" }, CancellationToken.None);

        db.Remove(role);
        db.Persist(duplicate);                                              // the unique index refuses the insert
        await Assert.ThrowsAnyAsync<Exception>(() => db.FlushAsync(CancellationToken.None));

        Assert.True(db.IsTracked(role));
        Assert.False(role.IsDeleted);
        Assert.Null(role.UpdatedAtUtc);
        Assert.False(db.IsDirty(role));
    }

    [Table("hook_memos")]
    public sealed class Memo
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public string? Text { get; set; }

        [Version]
        [Column]
        public long Version { get; set; }

        [Column]
        [CreatedAt]
        public DateTimeOffset Born { get; set; }

        [Column]
        [UpdatedAt]
        public DateTimeOffset? Touched { get; set; }

        [Column]
        [SoftDelete]
        public bool Deleted { get; set; }
    }

    [Table("hook_bad_1")]
    public sealed class TextCreatedAt
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [CreatedAt]
        public string? When { get; set; }
    }

    [Table("hook_bad_2")]
    public sealed class NullableFlag
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [SoftDelete]
        public bool? Deleted { get; set; }
    }

    [Table("hook_bad_3")]
    public sealed class HookOnKey
    {
        [Key]
        [Column]
        [CreatedAt]
        public DateTime Id { get; set; }
    }

    [Table("hook_bad_4")]
    public sealed class TwoStamps
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [UpdatedAt]
        public DateTime? A { get; set; }

        [Column]
        [UpdatedAt]
        public DateTime? B { get; set; }
    }

    [Table("hook_bad_5")]
    public sealed class TwoHooksOnOne
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [CreatedAt]
        [UpdatedAt]
        public DateTime When { get; set; }
    }
}

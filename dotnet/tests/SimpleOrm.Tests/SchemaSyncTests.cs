using GenModels;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0017: force sync (<c>migrate --force</c>). Additive fixes are planned and
/// applied; deletions are planned separately (gated by --allow-delete, DDL-003);
/// type and nullability changes are never auto-applied (DDL-004).
/// </summary>
public sealed class SchemaSyncTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"simpleorm_sync_{Guid.NewGuid():N}.db");

    private Task<Db> OpenAsync()
        => Db.OpenAsync($"Data Source={_path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    [Fact]
    public async Task Missing_table_is_created_additively()
    {
        await using var db = await OpenAsync();
        var plan = await SchemaSync.PlanAsync(db, [typeof(SyncNew)], CancellationToken.None);

        Assert.Contains(plan.Additive, sql => sql.StartsWith("create table if not exists sync_new_widgets"));
        Assert.Empty(plan.Deletions);
        Assert.Empty(plan.Unsupported);

        await SchemaSync.ApplyAsync(db, plan.Additive, CancellationToken.None);
        Assert.True((await SchemaSync.PlanAsync(db, [typeof(SyncNew)], CancellationToken.None)).IsEmpty);
    }

    [Fact]
    public async Task Missing_nullable_column_is_additive_and_extra_column_is_a_deletion()
    {
        await using var db = await OpenAsync();
        await SchemaSync.ApplyAsync(
            db,
            ["create table sync_add_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, junk INTEGER) STRICT"],
            CancellationToken.None);

        var plan = await SchemaSync.PlanAsync(db, [typeof(SyncAdd)], CancellationToken.None);
        Assert.Equal("alter table sync_add_widgets add column note TEXT", Assert.Single(plan.Additive));
        Assert.Equal("alter table sync_add_widgets drop column junk", Assert.Single(plan.Deletions));
        Assert.Empty(plan.Unsupported);

        await SchemaSync.ApplyAsync(db, plan.Additive.Concat(plan.Deletions), CancellationToken.None);
        Assert.True((await SchemaSync.PlanAsync(db, [typeof(SyncAdd)], CancellationToken.None)).IsEmpty);
    }

    [Fact]
    public async Task Index_matching_is_structural_not_by_name()
    {
        await using var db = await OpenAsync();

        // The model (GenModels.Widget) declares ix_gen_widgets_name on (name); the
        // DBA added the same index under another name in an urgency: implemented.
        await SchemaSync.ApplyAsync(
            db,
            ["create table gen_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT) STRICT",
             "create index idx_dba_hotfix on gen_widgets (name)"],
            CancellationToken.None);
        Assert.True((await SchemaSync.PlanAsync(db, [typeof(Widget)], CancellationToken.None)).IsEmpty);

        // A structurally different index is not: the model's gets created, the
        // stranger is a (gated) deletion.
        await SchemaSync.ApplyAsync(
            db,
            ["drop index idx_dba_hotfix", "create index idx_dba_hotfix on gen_widgets (note)"],
            CancellationToken.None);
        var plan = await SchemaSync.PlanAsync(db, [typeof(Widget)], CancellationToken.None);
        Assert.Contains("ix_gen_widgets_name", Assert.Single(plan.Additive));
        Assert.Equal("drop index idx_dba_hotfix", Assert.Single(plan.Deletions));
        Assert.Empty(plan.Unsupported);
    }

    [Fact]
    public async Task Type_and_nullability_changes_are_never_auto_applied()
    {
        await using var db = await OpenAsync();
        await SchemaSync.ApplyAsync(
            db,
            ["create table sync_bad_widgets (id INTEGER PRIMARY KEY, note INTEGER) STRICT"],
            CancellationToken.None);

        var plan = await SchemaSync.PlanAsync(db, [typeof(SyncBad)], CancellationToken.None);
        Assert.Empty(plan.Additive);
        Assert.Empty(plan.Deletions);
        Assert.Contains(plan.Unsupported, m => m.Contains("sync_bad_widgets.note") && m.Contains("INTEGER"));
    }
}

using GuardFixture;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0017 add.1 (owner): views get adjusted outside the code in urgencies, so a
/// view step's <c>ExpectDefinition</c> guard compares the live definition against
/// the expected previous one before applying — match applies, drift refuses with
/// <c>MIG-012</c>, and only <c>--force</c> recreates over the drift (with notice).
/// </summary>
public sealed class ViewGuardTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"simpleorm_guard_{Guid.NewGuid():N}.db");

    private Task<Db> OpenAsync()
        => Db.OpenAsync($"Data Source={_path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    private static SqlVersion V1() => new(1, new SqlVersion.Step(
        "guarded_totals", "create", [V0002_ChangeGuarded.V1Ddl], ["drop view guarded_totals"]));

    private static MigrationVersion[] Plan() => [V1(), new V0002()];

    [Fact]
    public async Task Matching_previous_definition_applies()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, Plan());
        Assert.Equal(2, await runner.MigrateAsync(CancellationToken.None));

        var rows = await db.QueryAllAsync<GuardedTotals>(CancellationToken.None);
        Assert.Equal(2, Assert.Single(rows).Answer);
    }

    [Fact]
    public async Task Outside_drift_refuses_with_MIG012_and_applies_nothing()
    {
        await using var db = await OpenAsync();
        await new MigrationRunner(db, [V1()]).MigrateAsync(CancellationToken.None);

        // The urgency hotfix: the view is patched directly in the database.
        await SchemaSync.ApplyAsync(
            db, ["drop view guarded_totals", "create view guarded_totals as select 99 as answer"], CancellationToken.None);

        var runner = new MigrationRunner(db, Plan());
        var exception = await Assert.ThrowsAsync<SimpleOrmException>(
            () => runner.MigrateAsync(CancellationToken.None));
        Assert.Equal("MIG-012", exception.Code);

        // The whole run rolled back: the hotfixed definition is untouched.
        var rows = await db.QueryAllAsync<GuardedTotals>(CancellationToken.None);
        Assert.Equal(99, Assert.Single(rows).Answer);
    }

    [Fact]
    public async Task Force_recreates_over_the_drift_and_notifies()
    {
        await using var db = await OpenAsync();
        await new MigrationRunner(db, [V1()]).MigrateAsync(CancellationToken.None);
        await SchemaSync.ApplyAsync(
            db, ["drop view guarded_totals", "create view guarded_totals as select 99 as answer"], CancellationToken.None);

        var notices = new List<string>();
        var runner = new MigrationRunner(db, Plan());
        Assert.Equal(1, await runner.MigrateAsync(allowViewDrift: true, notices.Add, CancellationToken.None));

        Assert.Contains("guarded_totals", Assert.Single(notices));
        var rows = await db.QueryAllAsync<GuardedTotals>(CancellationToken.None);
        Assert.Equal(2, Assert.Single(rows).Answer);
    }

    [Fact]
    public async Task Down_is_guarded_the_same_way()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, Plan());
        await runner.MigrateAsync(CancellationToken.None);

        await SchemaSync.ApplyAsync(
            db, ["drop view guarded_totals", "create view guarded_totals as select 99 as answer"], CancellationToken.None);
        Assert.Equal("MIG-012", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => runner.MigrateDownAsync(1, CancellationToken.None))).Code);

        Assert.Equal(1, await runner.MigrateDownAsync(
            1, allowViewDrift: true, notify: null, CancellationToken.None));
        var rows = await db.QueryAllAsync<GuardedTotals>(CancellationToken.None);
        Assert.Equal(1, Assert.Single(rows).Answer);
    }
}

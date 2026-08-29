using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>Milestone 5: the versioned migration runner, per scenario and per code.</summary>
public sealed class MigrationRunnerTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"simpleorm_mig_{Guid.NewGuid():N}.db");

    private Task<Db> OpenAsync()
        => Db.OpenAsync($"Data Source={_path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    private static SqlVersion Widgets1(string sql = "create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT")
        => new(1, new SqlVersion.Step("widgets", "create", [sql], ["drop table widgets"]));

    [Fact]
    public async Task Sample_migrations_apply_once_and_seed()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");

        Assert.True(await runner.HasPendingAsync(CancellationToken.None));
        Assert.Equal(9, await runner.MigrateAsync(CancellationToken.None));      // V0001..V0009
        Assert.Equal(0, await runner.MigrateAsync(CancellationToken.None));      // idempotent
        Assert.False(await runner.HasPendingAsync(CancellationToken.None));

        // Seeds survived the V0004 rename; V0005 added the second role.
        var roles = await db.QueryAllAsync<Role>(CancellationToken.None);
        Assert.Equal(["admin", "user"], roles.Select(r => r.Name));

        var status = await runner.StatusAsync(CancellationToken.None);
        Assert.Equal(15, status.Count);                                          // one row per (version, object)
        Assert.All(status, e => Assert.Equal(MigrationState.Applied, e.State));
    }

    private static SqlVersion GadgetV1() => new(1, new SqlVersion.Step("gadgets", "create",
        ["create table gadgets (id INTEGER PRIMARY KEY, label TEXT, legacy TEXT) STRICT",
         "insert into gadgets (label, legacy) values ('hello', 'old')"],
        ["drop table gadgets"]));

    private sealed class V0002_Restructure : TableMigration<Models2.Gadget>
    {
        public override void Action(TableActions actions)
        {
            // Deliberately declared out of order: the renderer must run rename → add → remove.
            actions.AddColumn("label", "TEXT")
                .Post("update gadgets set label = 'L-' || title");
            actions.RemoveColumn("legacy")
                .Pre("update gadgets set title = title || '-' || legacy");
            actions.RenameColumn("label", "title");
        }

        public override void Down(TableActions actions) => actions.RemoveColumn("label");

        public override void PreDown(MigrationSql sql)
            => sql.Sql("update gadgets set title = label");        // works only while label still exists

        public override void PostDown(MigrationSql sql)
            => sql.Sql("update gadgets set title = title || '!'"); // runs after the DDL
    }

    private sealed class GadgetV2 : MigrationVersion
    {
        public override long Version => 2;

        public override void Compose(VersionBuilder version) => version.Apply<V0002_Restructure>();
    }

    [Fact]
    public async Task Actions_reorder_to_rename_add_remove_with_hooks_in_place()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, [GadgetV1(), new GadgetV2()]);
        await runner.MigrateAsync(CancellationToken.None);

        Query<EmptyArgs, GadgetRow> read = Query.Inline("select title, label from gadgets");
        var row = await db.QuerySingleAsync(read, EmptyArgs.Value, CancellationToken.None);

        Assert.Equal("hello-old", row.Title);   // pre-remove ran after the add group, before remove
        Assert.Equal("L-hello", row.Label);     // post-add backfilled from the renamed column

        // Down: PreDown (needs label) → DDL (drops label) → PostDown.
        await runner.MigrateDownAsync(1, CancellationToken.None);
        Query<EmptyArgs, string> title = Query.Inline("select title from gadgets");
        Assert.Equal("L-hello!", await db.QuerySingleAsync(title, EmptyArgs.Value, CancellationToken.None));
    }

    public sealed record GadgetRow(string Title, string Label);

    [Fact]
    public async Task Down_reverts_in_reverse_and_requires_down_statements()
    {
        await using var db = await OpenAsync();
        var reversible = new MigrationRunner(db, [Widgets1()]);
        await reversible.MigrateAsync(CancellationToken.None);
        Assert.Equal(1, await reversible.MigrateDownAsync(0, CancellationToken.None));
        var status = await reversible.StatusAsync(CancellationToken.None);
        Assert.DoesNotContain(status, e => e.State == MigrationState.Applied);

        var irreversible = new MigrationRunner(
            db, [new SqlVersion(1, new SqlVersion.Step("widgets", "create", ["create table widgets (id INTEGER PRIMARY KEY) STRICT"]))]);
        await irreversible.MigrateAsync(CancellationToken.None);
        var refused = await Assert.ThrowsAsync<SimpleOrmException>(
            () => irreversible.MigrateDownAsync(0, CancellationToken.None));
        Assert.Equal("MIG-020", refused.Code);
    }

    [Fact]
    public async Task Checksum_drift_and_unknown_history_fail_before_executing()
    {
        await using var db = await OpenAsync();
        await new MigrationRunner(db, [Widgets1()]).MigrateAsync(CancellationToken.None);

        var drifted = new MigrationRunner(db, [Widgets1("create table widgets (id INTEGER PRIMARY KEY, extra TEXT) STRICT")]);
        Assert.Equal("MIG-010", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => drifted.MigrateAsync(CancellationToken.None))).Code);

        var emptied = new MigrationRunner(db, Array.Empty<MigrationVersion>());
        Assert.Equal("MIG-011", (await Assert.ThrowsAsync<SimpleOrmException>(
            () => emptied.MigrateAsync(CancellationToken.None))).Code);
    }

    [Fact]
    public async Task Failed_run_rolls_back_every_version()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db,
        [
            Widgets1(),
            new SqlVersion(2, new SqlVersion.Step("widgets", "boom", ["this is not sql"])),
        ]);

        await Assert.ThrowsAnyAsync<Exception>(() => runner.MigrateAsync(CancellationToken.None));

        // BEGIN IMMEDIATE run: nothing survives — not even V1.
        var clean = new MigrationRunner(db, [Widgets1()]);
        var status = await clean.StatusAsync(CancellationToken.None);
        Assert.Equal(MigrationState.Pending, Assert.Single(status).State);
    }

    [Fact]
    public async Task Baseline_records_without_running()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, [Widgets1()]);
        await runner.BaselineAsync(1, CancellationToken.None);

        Assert.False(await runner.HasPendingAsync(CancellationToken.None));
        Assert.Equal(0, await runner.MigrateAsync(CancellationToken.None));

        // The table was never actually created — baseline only records.
        Query<EmptyArgs, long> exists = Query.Inline(
            "select count(name) from sqlite_master where type = 'table' and name = 'widgets'");
        Assert.Equal(0L, await db.QuerySingleAsync(exists, EmptyArgs.Value, CancellationToken.None));
    }

    [Fact]
    public async Task Composition_violations_have_their_codes()
    {
        await using var db = await OpenAsync();

        Assert.Equal("MIG-002", Assert.Throws<SimpleOrmException>(
            () => new MigrationRunner(db, [Widgets1(), Widgets1()])).Code);

        Assert.Equal("MIG-003", Assert.Throws<SimpleOrmException>(
            () => new MigrationRunner(db, [new MismatchRoot()])).Code);

        Assert.Equal("MIG-001", Assert.Throws<SimpleOrmException>(
            () => new MigrationRunner(db, [new BadNameRoot()])).Code);

        Assert.Equal("MIG-004", Assert.Throws<SimpleOrmException>(
            () => new MigrationRunner(db, typeof(MigrationRunnerTests).Assembly, "SimpleOrm.Tests.MigOrphanFixture")).Code);
    }

    private sealed class MismatchRoot : MigrationVersion
    {
        public override long Version => 1;

        public override void Compose(VersionBuilder version) => version.Apply<V0002_Restructure>();
    }

    private sealed class BadNameRoot : MigrationVersion
    {
        public override long Version => 1;

        public override void Compose(VersionBuilder version) => version.Apply<WronglyNamedStep>();
    }

    public sealed class WronglyNamedStep : TableMigration<Models2.Gadget>
    {
        public override void Action(TableActions actions) => actions.Sql("select 1");
    }
}

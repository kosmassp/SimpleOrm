using Microsoft.Data.Sqlite;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0018 (owner): "Down could be deducted from the previous schema" — nobody
/// writes rollback DDL. The runner derives it at migrate-down time from the
/// versioned snapshots (embedded in the migrations assembly), inverting the
/// step's typed renames data-preservingly and diffing the rest. Down() remains
/// the manual override; missing snapshots still refuse (MIG-020).
/// </summary>
public sealed class DerivedDownTests : IDisposable
{
    private readonly string _path = Path.Combine(Path.GetTempPath(), $"simpleorm_derived_{Guid.NewGuid():N}.db");

    private Task<Db> OpenAsync()
        => Db.OpenAsync($"Data Source={_path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

    public void Dispose()
    {
        SqliteConnection.ClearAllPools();
        File.Delete(_path);
    }

    [Fact]
    public async Task The_whole_sample_history_rolls_back_and_reapplies_without_any_down_code()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");

        Assert.Equal(8, await runner.MigrateAsync(CancellationToken.None));

        // No sample step overrides Down(); every rollback below is derived —
        // reverse renames, removed columns restored, added columns dropped,
        // indexes structurally reverted, the view's previous definition restored,
        // creates dropped.
        Assert.Equal(8, await runner.MigrateDownAsync(0, CancellationToken.None));
        Assert.Equal(0, await CountObjectsAsync());

        // And the same plan applies cleanly again: down really reached V0000.
        Assert.Equal(8, await runner.MigrateAsync(CancellationToken.None));
        var roles = await db.QueryAllAsync<Role>(CancellationToken.None);
        Assert.Equal(["admin", "user"], roles.Select(r => r.Name));
    }

    [Fact]
    public async Task Partial_rollback_reverses_the_rename_and_keeps_data()
    {
        await using var db = await OpenAsync();
        var runner = new MigrationRunner(db, typeof(User).Assembly, "SimpleOrm.Sample.Migrations");
        await runner.MigrateAsync(CancellationToken.None);

        // Down past V0004 (roles.name -> role_name): the derived rollback inverts
        // the rename, so the V0001 'admin' seed survives in the old column.
        await runner.MigrateDownAsync(3, CancellationToken.None);

        using var connection = new SqliteConnection($"Data Source={_path}");
        connection.Open();
        using var query = connection.CreateCommand();
        query.CommandText = "select name from roles order by name";
        var names = new List<string>();
        using var reader = query.ExecuteReader();
        while (reader.Read())
        {
            names.Add(reader.GetString(0));
        }

        Assert.Contains("admin", names);
    }

    [Fact]
    public async Task Underivable_constraint_is_noticed_not_guessed()
    {
        var snapshotDir = Path.Combine(Path.GetTempPath(), $"simpleorm_snap_{Guid.NewGuid():N}");
        Directory.CreateDirectory(snapshotDir);
        try
        {
            // History: V1 creates widgets(id, label NOT NULL); V2 drops label.
            File.WriteAllText(Path.Combine(snapshotDir, "V0001.schema.json"), SchemaSnapshot.Export(
                new TableSchema("derived_widgets",
                    [new TableSchema.Column("id", "INTEGER", nullable: false, key: true, generated: true),
                     new TableSchema.Column("label", "TEXT", nullable: false)],
                    []),
                asOfVersion: 1, DateTimeOffset.UtcNow));
            File.WriteAllText(Path.Combine(snapshotDir, "V0002.schema.json"), SchemaSnapshot.Export(
                new TableSchema("derived_widgets",
                    [new TableSchema.Column("id", "INTEGER", nullable: false, key: true, generated: true)],
                    []),
                asOfVersion: 2, DateTimeOffset.UtcNow));

            var versions = new MigrationVersion[]
            {
                new SqlVersion(1, new SqlVersion.Step("derived_widgets", "create",
                    ["create table derived_widgets (id INTEGER PRIMARY KEY, label TEXT NOT NULL) STRICT"])),
                new SqlVersion(2, new SqlVersion.Step("derived_widgets", "drop label",
                    ["alter table derived_widgets drop column label"])),
            };

            await using var db = await OpenAsync();
            var runner = new MigrationRunner(db, versions, SnapshotSet.FromDirectory(snapshotDir));
            await runner.MigrateAsync(CancellationToken.None);

            // The structure returns nullable; the NOT NULL constraint (and data) can't derive.
            var notices = new List<string>();
            Assert.Equal(1, await runner.MigrateDownAsync(1, allowViewDrift: false, notices.Add, CancellationToken.None));
            Assert.Contains(notices, n => n.Contains("label") && n.Contains("not derivable"));

            // Without snapshots the same plan still refuses honestly (MIG-020).
            await runner.MigrateAsync(CancellationToken.None);
            var blind = new MigrationRunner(db, versions);
            Assert.Equal("MIG-020", (await Assert.ThrowsAsync<SimpleOrmException>(
                () => blind.MigrateDownAsync(1, CancellationToken.None))).Code);
        }
        finally
        {
            Directory.Delete(snapshotDir, recursive: true);
        }
    }

    private async Task<long> CountObjectsAsync()
    {
        using var connection = new SqliteConnection($"Data Source={_path}");
        connection.Open();
        using var command = connection.CreateCommand();
        command.CommandText =
            "select count(*) from sqlite_master where type in ('table', 'view') "
            + "and name not like 'sqlite_%' and name <> 'schema_version'";
        return (long)(await command.ExecuteScalarAsync())!;
    }
}

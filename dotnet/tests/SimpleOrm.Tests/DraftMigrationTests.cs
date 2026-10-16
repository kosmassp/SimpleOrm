using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// Level 3 M5 (spec/migrations.md "Draft migrations"): <c>[RenamedFrom]</c> as a
/// declared rename the generator reads, permanent and harmless once applied;
/// <c>[Draft]</c> as the marker the runner refuses (<c>MIG-013</c>) and status
/// reports; the generated root carrying it under <c>--draft</c>.
/// </summary>
public sealed class DraftMigrationTests
{
    [Fact]
    public void Renamed_from_is_metadata_the_export_carries()
    {
        var map = new EntityMapLoader().Load<Role>();
        Assert.Equal("name", map.Properties.Single(p => p.PropertyName == nameof(Role.Name)).RenamedFrom);

        var loader = new EntityMapLoader();
        Assert.Contains(Assert.Throws<MappingException>(() => loader.Load<RenamedWithoutColumn>()).Errors, e => e.Code == "MAP-027");
        Assert.Contains(Assert.Throws<MappingException>(() => loader.Load<RenamedFromNothing>()).Errors, e => e.Code == "MAP-027");
    }

    [Fact]
    public void A_declared_rename_applies_once_and_an_explicit_one_must_hold()
    {
        var map = new EntityMapLoader().Load<Role>();
        var dialect = new SqliteDialect();
        var before = SchemaSnapshot.FromMap(new EntityMapLoader().Load<RoleBeforeV0004>(), dialect);   // has "name"
        var after = SchemaSnapshot.FromMap(map, dialect);                                              // has "role_name"

        var rename = MigrationGenerator.Diff(map, dialect, before, new Dictionary<string, string>());
        Assert.Equal([("name", "role_name")], rename.Renamed);
        Assert.Empty(rename.Added);

        var applied = MigrationGenerator.Diff(map, dialect, after, new Dictionary<string, string>());
        Assert.False(applied.HasChanges);

        var strict = MigrationGenerator.Diff(map, dialect, after, new Dictionary<string, string> { ["name"] = "role_name" });
        Assert.Contains(strict.Unsupported, m => m.Contains("not in the snapshot"));
    }

    [Fact]
    public void The_generated_root_carries_the_draft_marker_only_when_asked()
    {
        var plain = MigrationGenerator.EmitRoot("App.Migrations", 12, ["Table.User.V0012_X"], "sqlite");
        Assert.DoesNotContain("[Draft]", plain);
        var draft = MigrationGenerator.EmitRoot("App.Migrations", 12, ["Table.User.V0012_X"], "sqlite", draft: true);
        Assert.Contains("[Draft]", draft);
        Assert.Contains(MigrationGenerator.GeneratedMarker, draft);
    }

    [Fact]
    public async Task The_runner_refuses_a_draft_and_status_reports_it()
    {
        // Its own database: the shared fixture carries the sample's recorded history, which is MIG-011 to this plan.
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_draft_{Guid.NewGuid():N}.db");
        await using var db = await Db.OpenAsync($"Data Source={path};Pooling=False", TestDb.Options, CancellationToken.None);
        var versions = new MigrationVersion[]
        {
            new SqlVersion(1, new SqlVersion.Step("draft_widgets", "create", ["create table draft_widgets (id INTEGER PRIMARY KEY) STRICT"])),
            new SqlVersion(2, new SqlVersion.Step("draft_widgets", "add_note", ["alter table draft_widgets add column note TEXT"])) { Draft = true },
        };
        var runner = new MigrationRunner(db, versions);

        var refused = await Assert.ThrowsAsync<SimpleOrmException>(() => runner.MigrateAsync(CancellationToken.None));
        Assert.Equal("MIG-013", refused.Code);
        Assert.Equal("MIG-013", (await Assert.ThrowsAsync<SimpleOrmException>(() => runner.BaselineAsync(2, CancellationToken.None))).Code);
        Assert.Equal([MigrationState.Pending, MigrationState.Draft], (await runner.StatusAsync(CancellationToken.None)).Select(e => e.State));

        await runner.BaselineAsync(1, CancellationToken.None);   // below the draft: fine
        Assert.Equal([MigrationState.Applied, MigrationState.Draft], (await runner.StatusAsync(CancellationToken.None)).Select(e => e.State));
    }

    [Table("roles")]
    public sealed class RoleBeforeV0004
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column("name")]
        public string? Name { get; set; }

        [Column("is_deleted")]
        public bool IsDeleted { get; set; }

        [Column("created_at")]
        public DateTime CreatedAtUtc { get; set; }

        [Column("updated_at")]
        public DateTime? UpdatedAtUtc { get; set; }
    }

    [Table("renamed_bad_1")]
    public sealed class RenamedWithoutColumn
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [RenamedFrom("old")]
        public string? Label { get; set; }
    }

    [Table("renamed_bad_2")]
    public sealed class RenamedFromNothing
    {
        [Key]
        [Column]
        public long Id { get; set; }

        [Column]
        [RenamedFrom("")]
        public string? Label { get; set; }
    }
}

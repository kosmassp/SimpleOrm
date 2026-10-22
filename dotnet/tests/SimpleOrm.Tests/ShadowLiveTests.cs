using System.Text.RegularExpressions;
using SimpleOrm.Postgres;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The shadow replayer on a live engine (ADR-0043): a four-version tree — a table
/// from metadata, a view, a trigger, then a literal column addition in the
/// dialect's own type — replays into a throwaway database the dialect's shadow
/// host provisions, and the snapshots it writes must equal the metadata-side
/// export byte for byte apart from <c>generatedAt</c>: tables by introspection
/// (identity key, the storage types as the engine reports them, the index), views
/// and triggers from the replayed step, with the lossless catalog read-back
/// agreeing where the dialect has one. Skips where no server answers.
/// </summary>
public static class ShadowLiveTests
{
    private static string Normalize(string json)
        => Regex.Replace(json, "\"generatedAt\": \"[^\"]+\"", "\"generatedAt\": \"-\"").Replace("\r", string.Empty).TrimEnd();

    internal static async Task RoundTripAsync(IDialect dialect, IShadowHost host, string admin, MigrationVersion first, MigrationVersion fourth)
    {
        var loader = new EntityMapLoader();
        var versions = new[] { first, new ShadowTree.V0002(), new ShadowTree.V0003(), fourth };
        var entityTypes = new[] { typeof(ShadowTree.ShadowWidget), typeof(ShadowTree.ShadowWidgetName) };
        var outDir = Path.Combine(Path.GetTempPath(), $"simpleorm_shadow_live_{Guid.NewGuid():N}");
        try
        {
            var result = await SchemaShadow.RebuildSnapshotsAsync(versions, entityTypes, outDir, dialect, host, admin, ct: CancellationToken.None);
            Assert.DoesNotContain(result.Notes, n => n.Contains("differently") || n.Contains("skipped"));
            Assert.Equal(4, result.WrittenFiles.Count);

            var at = DateTimeOffset.UtcNow;
            var widget = loader.Load<ShadowTree.ShadowWidget>();
            var names = loader.Load<ShadowTree.ShadowWidgetName>();

            // The latest table snapshot is the model; the first one is the model without the column V0004 added.
            var v4 = File.ReadAllText(Path.Combine(outDir, "Table", nameof(ShadowTree.ShadowWidget), "V0004.schema.json"));
            Assert.Equal(Normalize(SchemaSnapshot.Export(widget, dialect, 4, at)), Normalize(v4));
            var (v1, _) = SchemaSnapshot.Parse(File.ReadAllText(Path.Combine(outDir, "Table", nameof(ShadowTree.ShadowWidget), "V0001.schema.json")));
            Assert.Equal(["id", "name"], v1.Columns.Select(c => c.Name));
            Assert.True(v1.Columns.Single(c => c.Name == "id").Generated);
            Assert.Equal("ix_shadow_widgets_name", Assert.Single(v1.Indexes).Name);
            Assert.Equal(dialect.Name, SchemaSnapshot.DialectOf(v4));

            var view = File.ReadAllText(Path.Combine(outDir, "View", nameof(ShadowTree.ShadowWidgetName), "V0002.schema.json"));
            Assert.Equal(
                Normalize(SchemaSnapshot.ExportDdl(names.RelationName!, "view", dialect.CreateViewSql(names), 2, at, dialect.Name)),
                Normalize(view));

            var trigger = File.ReadAllText(Path.Combine(outDir, "Trigger", nameof(ShadowTree.ShadowWidgetTrigger), "V0003.schema.json"));
            Assert.Equal(
                Normalize(SchemaSnapshot.ExportTrigger(
                    new TriggerSnapshot(widget.RelationName!, false, widget.Triggers.Single().Definition), 3, at, dialect.Name)),
                Normalize(trigger));
        }
        finally
        {
            if (Directory.Exists(outDir))
            {
                Directory.Delete(outDir, recursive: true);
            }
        }
    }

    /// <summary>
    /// Force sync against a live engine (ADR-0043): the live table is V0001's shape, the
    /// model has the note column; the plan is exactly one additive statement and no
    /// <c>DDL-004</c> — which is only true when the dialect spells its storage types the
    /// way its introspection reports them back (ruling 8).
    /// </summary>
    internal static async Task ForceSyncPlansOnlyTheMissingColumnAsync(IDialect dialect, IShadowHost host, string admin, MigrationVersion first, string expectedAdd)
    {
        var connectionString = await host.ProvisionAsync(admin, CancellationToken.None);
        try
        {
            await using var db = await Db.OpenAsync(connectionString, new DbOptions { Dialect = dialect }, CancellationToken.None);
            await new MigrationRunner(db, [first]).MigrateAsync(CancellationToken.None);

            var plan = await SchemaSync.PlanAsync(db, [typeof(ShadowTree.ShadowWidget)], CancellationToken.None);
            Assert.Empty(plan.Unsupported);
            Assert.Empty(plan.Deletions);
            Assert.Equal(expectedAdd, Assert.Single(plan.Additive));

            await SchemaSync.ApplyAsync(db, plan.Additive, CancellationToken.None);
            var after = await SchemaSync.PlanAsync(db, [typeof(ShadowTree.ShadowWidget)], CancellationToken.None);
            Assert.True(after.IsEmpty, "the live schema should match the model after the sync");
        }
        finally
        {
            await host.DropAsync(CancellationToken.None);
        }
    }

    [Collection(PostgresCollection.Name)]
    public sealed class OnPostgres
    {
        [PostgresFact]
        public Task The_shadow_reproduces_the_metadata_snapshots_on_postgres()
            => RoundTripAsync(new PostgresDialect(), new PostgresShadowHost(), PostgresFixture.AdminConnectionString, new ShadowTree.Postgres.V0001(), new ShadowTree.Postgres.V0004());

        [PostgresFact]
        public Task Force_sync_plans_only_the_missing_column_on_postgres()
            => ForceSyncPlansOnlyTheMissingColumnAsync(
                new PostgresDialect(), new PostgresShadowHost(), PostgresFixture.AdminConnectionString, new ShadowTree.Postgres.V0001(),
                "alter table \"shadow_widgets\" add column \"note\" text");
    }

    [Collection(SqlServerCollection.Name)]
    public sealed class OnSqlServer
    {
        [SqlServerFact]
        public Task The_shadow_reproduces_the_metadata_snapshots_on_sql_server()
            => RoundTripAsync(new SqlServerDialect(), new SqlServerShadowHost(), SqlServerFixture.MasterConnectionString, new ShadowTree.SqlServer.V0001(), new ShadowTree.SqlServer.V0004());

        [SqlServerFact]
        public Task Force_sync_plans_only_the_missing_column_on_sql_server()
            => ForceSyncPlansOnlyTheMissingColumnAsync(
                new SqlServerDialect(), new SqlServerShadowHost(), SqlServerFixture.MasterConnectionString, new ShadowTree.SqlServer.V0001(),
                "alter table [shadow_widgets] add [note] nvarchar(max)");
    }
}

/// <summary>The live shadow tree: three neutral versions and one literal, dialect-specific addition per dialect.</summary>
public static class ShadowTree
{
    [Table("shadow_widgets")]
    [Index(nameof(Name), Unique = true)]
    [Trigger(typeof(ShadowWidgetTrigger))]
    public sealed class ShadowWidget
    {
        [Key]
        [Generated]
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Name { get; set; }

        /// <summary>Added by V0004, in the dialect's own type.</summary>
        [Column]
        public string? Note { get; set; }
    }

    [View("shadow_widget_names", "select id, name from shadow_widgets")]
    public sealed class ShadowWidgetName
    {
        [Column]
        public long Id { get; set; }

        [Column]
        public required string Name { get; set; }
    }

    [Trigger("trg_shadow_widgets", TriggerTiming.After, TriggerEvent.Update,
        "update shadow_widgets set name = name where id in (select n.id from {new: id} n);")]
    public sealed class ShadowWidgetTrigger
    {
    }

    public sealed class V0002_CreateNames : ViewMigration<ShadowWidgetName>
    {
        public override void Action(ViewActions actions) => actions.CreateView();
    }

    public sealed class V0003_CreateTrigger : TriggerMigration<ShadowWidget, ShadowWidgetTrigger>
    {
        public override void Action(TriggerActions actions) => actions.CreateTrigger();
    }

    public sealed class V0002 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<V0002_CreateNames>();
    }

    public sealed class V0003 : MigrationVersion
    {
        public override void Compose(VersionBuilder version) => version.Apply<V0003_CreateTrigger>();
    }

    // The table steps are literal, dialect-specific text (the freeze rule, ADR-0013): V0001 is the
    // shape *then* (no note), V0004 adds the column in the dialect's own type.
    public static class Postgres
    {
        public sealed class V0001_CreateShadowWidgets : TableMigration<ShadowWidget>
        {
            public override void Action(TableActions actions)
            {
                actions.Sql("create table \"shadow_widgets\" (\"id\" bigint generated by default as identity primary key, \"name\" text not null)");
                actions.Sql("create unique index \"ix_shadow_widgets_name\" on \"shadow_widgets\" (\"name\")");
            }
        }

        public sealed class V0004_AddNote : TableMigration<ShadowWidget>
        {
            public override void Action(TableActions actions) => actions.AddColumn("note", "text");
        }

        public sealed class V0001 : MigrationVersion
        {
            public override void Compose(VersionBuilder version) => version.Apply<V0001_CreateShadowWidgets>();
        }

        public sealed class V0004 : MigrationVersion
        {
            public override void Compose(VersionBuilder version) => version.Apply<V0004_AddNote>();
        }
    }

    public static class SqlServer
    {
        public sealed class V0001_CreateShadowWidgets : TableMigration<ShadowWidget>
        {
            public override void Action(TableActions actions)
            {
                actions.Sql("create table [shadow_widgets] ([id] bigint identity(1,1) primary key, [name] nvarchar(450) not null)");
                actions.Sql("create unique index [ix_shadow_widgets_name] on [shadow_widgets] ([name])");
            }
        }

        public sealed class V0004_AddNote : TableMigration<ShadowWidget>
        {
            public override void Action(TableActions actions) => actions.AddColumn("note", "nvarchar(max)");
        }

        public sealed class V0001 : MigrationVersion
        {
            public override void Compose(VersionBuilder version) => version.Apply<V0001_CreateShadowWidgets>();
        }

        public sealed class V0004 : MigrationVersion
        {
            public override void Compose(VersionBuilder version) => version.Apply<V0004_AddNote>();
        }
    }
}

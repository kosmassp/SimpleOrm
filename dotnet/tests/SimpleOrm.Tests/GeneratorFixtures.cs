// Fixture entities for GeneratorTests and SchemaSyncTests, in their own namespace
// so the sample models stay untouched. Each sync test owns a distinct table name:
// the SQLite test databases are shared per collection.
namespace GenModels
{
    [SimpleOrm.Table("gen_widgets")]
    [SimpleOrm.Index(nameof(Name))]
    public sealed class Widget
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string Name { get; set; } = string.Empty;

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }

    [SimpleOrm.Table("sync_new_widgets")]
    public sealed class SyncNew
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string? Label { get; set; }
    }

    [SimpleOrm.Table("sync_add_widgets")]
    public sealed class SyncAdd
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string Name { get; set; } = string.Empty;

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }

    [SimpleOrm.Table("sync_bad_widgets")]
    public sealed class SyncBad
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string? Note { get; set; }
    }
}

// The MIG-012 view guard (ADR-0017 add.1): V0002 changes guarded_totals and expects
// V0001's definition to still be live — the shape simpleorm diff generates.
namespace GuardFixture
{
    [SimpleOrm.View("guarded_totals", "select 2 as answer")]
    public sealed class GuardedTotals
    {
        [SimpleOrm.Column]
        public long Answer { get; set; }
    }

    public sealed class V0002 : SimpleOrm.MigrationVersion
    {
        public override void Compose(SimpleOrm.VersionBuilder version)
            => version.Apply<V0002_ChangeGuarded>();
    }

    public sealed class V0002_ChangeGuarded : SimpleOrm.ViewMigration<GuardedTotals>
    {
        public const string V1Ddl = "create view guarded_totals as select 1 as answer";
        public const string V2Ddl = "create view guarded_totals as select 2 as answer";

        public override void Action(SimpleOrm.ViewActions actions)
        {
            actions.ExpectDefinition(V1Ddl);
            actions.Sql("drop view if exists guarded_totals");
            actions.Sql(V2Ddl);
        }

        public override void Down(SimpleOrm.ViewActions actions)
        {
            actions.ExpectDefinition(V2Ddl);
            actions.Sql("drop view if exists guarded_totals");
            actions.Sql(V1Ddl);
        }
    }
}

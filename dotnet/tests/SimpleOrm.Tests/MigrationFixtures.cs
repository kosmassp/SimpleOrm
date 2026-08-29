// Fixture types for MigrationRunnerTests, in their own namespaces on purpose:
// Models2.Gadget is the entity the local migration steps bind to, and
// MigOrphanFixture is a scoped namespace exercising the MIG-004 orphan scan.
namespace Models2
{
    [SimpleOrm.Table("gadgets")]
    public sealed class Gadget
    {
        [SimpleOrm.Key]
        [SimpleOrm.Generated]
        [SimpleOrm.Column]
        public long Id { get; set; }

        [SimpleOrm.Column]
        public string? Title { get; set; }

        [SimpleOrm.Column]
        public string? Label { get; set; }
    }
}

namespace SimpleOrm.Tests.MigOrphanFixture
{
    public sealed class V0001 : SimpleOrm.MigrationVersion
    {
        public override void Compose(SimpleOrm.VersionBuilder version)
        {
        }
    }

    public sealed class V0001_Stray : SimpleOrm.TableMigration<Models2.Gadget>
    {
        public override void Action(SimpleOrm.TableActions actions) => actions.Sql("select 1");
    }
}

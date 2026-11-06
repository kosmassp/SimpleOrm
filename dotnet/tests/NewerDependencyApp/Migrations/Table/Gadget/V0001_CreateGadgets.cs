using SimpleOrm;

namespace NewerDependencyApp.Migrations.Table.Gadget;

public sealed class V0001_CreateGadgets : TableMigration<NewerDependencyApp.Gadget>
{
    public override void Action(TableActions actions) => actions.CreateTable();
}

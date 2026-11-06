using SimpleOrm;

namespace NewerDependencyApp.Migrations;

public sealed class V0001 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.Gadget.V0001_CreateGadgets>();
}

namespace SimpleOrm.Sample.Migrations;

public sealed class V0004 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.Role.V0004_RenameNameToRoleName>();
}

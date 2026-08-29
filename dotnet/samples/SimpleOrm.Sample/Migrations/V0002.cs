namespace SimpleOrm.Sample.Migrations;

public sealed class V0002 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.User.V0002_AddDisplayName>();
}

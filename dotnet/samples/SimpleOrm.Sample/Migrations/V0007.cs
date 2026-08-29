namespace SimpleOrm.Sample.Migrations;

public sealed class V0007 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.User.V0007_IndexDisplayName>();
}

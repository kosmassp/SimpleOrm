namespace SimpleOrm.Sample.Migrations;

public sealed class V0003 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.Transaction.V0003_AddNote>();
}

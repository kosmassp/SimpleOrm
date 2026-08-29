namespace SimpleOrm.Sample.Migrations;

public sealed class V0006 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<View.UserTransactionTotals.V0006_AddLastTransactionAt>();
}

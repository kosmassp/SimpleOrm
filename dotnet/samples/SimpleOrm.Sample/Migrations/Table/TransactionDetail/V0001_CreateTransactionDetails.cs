namespace SimpleOrm.Sample.Migrations.Table.TransactionDetail;

public sealed class V0001_CreateTransactionDetails : TableMigration<Models.TransactionDetail>
{
    public override void Action(TableActions actions) => actions.CreateTable();
}

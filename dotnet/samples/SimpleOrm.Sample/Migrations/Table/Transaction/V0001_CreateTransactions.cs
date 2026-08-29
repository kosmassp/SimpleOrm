namespace SimpleOrm.Sample.Migrations.Table.Transaction;

public sealed class V0001_CreateTransactions : TableMigration<Models.Transaction>
{
    public override void Action(TableActions actions) => actions.CreateTable();

    public override void Down(TableActions actions) => actions.DropTable();
}

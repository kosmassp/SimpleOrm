namespace SimpleOrm.Sample.Migrations.Table.User;

public sealed class V0001_CreateUsers : TableMigration<Models.User>
{
    public override void Action(TableActions actions) => actions.CreateTable();

    public override void Down(TableActions actions) => actions.DropTable();
}

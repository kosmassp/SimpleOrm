namespace SimpleOrm.Sample.Migrations.Table.UserRole;

public sealed class V0001_CreateUserRoles : TableMigration<Models.UserRole>
{
    public override void Action(TableActions actions) => actions.CreateTable();

    public override void Down(TableActions actions) => actions.DropTable();
}

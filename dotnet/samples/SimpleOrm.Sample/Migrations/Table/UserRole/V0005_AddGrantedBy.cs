namespace SimpleOrm.Sample.Migrations.Table.UserRole;

public sealed class V0005_AddGrantedBy : TableMigration<Models.UserRole>
{
    public override void Action(TableActions actions) => actions.AddColumn("granted_by", "TEXT");
}

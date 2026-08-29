namespace SimpleOrm.Sample.Migrations.Table.Transaction;

public sealed class V0003_AddNote : TableMigration<Models.Transaction>
{
    public override void Action(TableActions actions) => actions.AddColumn("note", "TEXT");
}

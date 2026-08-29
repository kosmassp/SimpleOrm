namespace SimpleOrm.Sample.Migrations.Table.User;

/// <summary>DDL plus a data step in one version: index the column, then backfill the gaps.</summary>
public sealed class V0007_IndexDisplayName : TableMigration<Models.User>
{
    public override void Action(TableActions actions)
    {
        actions.Sql("create index if not exists ix_users_display_name on users (display_name)");
        actions.Sql("update users set display_name = name where display_name is null");
    }
}

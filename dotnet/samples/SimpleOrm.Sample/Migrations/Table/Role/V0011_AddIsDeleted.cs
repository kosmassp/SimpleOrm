namespace SimpleOrm.Sample.Migrations.Table.Role;

/// <summary>The soft-delete flag (Level 3 M3, spec/hooks.md): NOT NULL, existing rows are live.</summary>
public sealed class V0011_AddIsDeleted : TableMigration<global::SimpleOrm.Sample.Models.Role>
{
    public override void Action(TableActions actions)
    {
        actions.AddColumn("is_deleted", "INTEGER", nullable: false, defaultSql: "0");
    }
}

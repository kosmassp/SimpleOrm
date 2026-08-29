namespace SimpleOrm.Sample.Migrations.Table.Role;

/// <summary>
/// A rename as a first-class action (never inferable by a differ): existing rows —
/// including the V0001 'admin' seed — keep their data.
/// </summary>
public sealed class V0004_RenameNameToRoleName : TableMigration<Models.Role>
{
    public override void Action(TableActions actions) => actions.RenameColumn("name", "role_name");
}

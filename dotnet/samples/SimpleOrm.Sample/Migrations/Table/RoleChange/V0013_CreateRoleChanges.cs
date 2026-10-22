namespace SimpleOrm.Sample.Migrations.Table.RoleChange;

/// <summary>The audit rows of roles (ADR-0042), written only by the trigger. Literal SQL, as the generator emits an initial create; no Down (ADR-0018).</summary>
public sealed class V0013_CreateRoleChanges : TableMigration<global::SimpleOrm.Sample.Models.RoleChange>
{
    public override void Action(TableActions actions)
    {
        actions.Sql(
            """
            create table if not exists role_changes (
                id INTEGER PRIMARY KEY,
                role_id INTEGER NOT NULL,
                role_name TEXT NOT NULL,
                changed_at TEXT NOT NULL
            ) STRICT
            """);
    }
}

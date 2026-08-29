namespace SimpleOrm.Sample.Migrations.Table.Role;

public sealed class V0001_CreateRoles : TableMigration<Models.Role>
{
    /// <summary>Post-create hook: seed data belongs to the create action, not a separate mechanism.</summary>
    public override void Action(TableActions actions) => actions
        .CreateTable()
        .Post("insert into roles (name, created_at) values ('admin', '2026-01-01T00:00:00.0000000Z')");
}

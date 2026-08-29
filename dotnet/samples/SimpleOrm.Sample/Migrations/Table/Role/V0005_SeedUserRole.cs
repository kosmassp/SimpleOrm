namespace SimpleOrm.Sample.Migrations.Table.Role;

/// <summary>A data-only step: no DDL, just rows — the same mechanism, the same atomicity.</summary>
public sealed class V0005_SeedUserRole : TableMigration<Models.Role>
{
    public override void Action(TableActions actions) => actions
        .Sql("insert into roles (role_name, created_at) values ('user', '2026-08-29T00:00:00.0000000Z')");
}

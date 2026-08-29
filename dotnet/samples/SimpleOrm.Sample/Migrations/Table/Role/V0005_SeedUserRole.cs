namespace SimpleOrm.Sample.Migrations.Table.Role;

/// <summary>
/// A data-only step: no DDL, just rows — the same mechanism, the same atomicity.
/// The derived rollback (ADR-0018) sees an unchanged schema and reverts nothing,
/// which is correct for structure — the seeded row is data, so the PreDown hook
/// carries its removal (data work is always hook territory, never derived).
/// </summary>
public sealed class V0005_SeedUserRole : TableMigration<Models.Role>
{
    public override void Action(TableActions actions) => actions
        .Sql("insert into roles (role_name, created_at) values ('user', '2026-08-29T00:00:00.0000000Z')");

    public override void PreDown(MigrationSql sql) => sql
        .Sql("delete from roles where role_name = 'user'");
}

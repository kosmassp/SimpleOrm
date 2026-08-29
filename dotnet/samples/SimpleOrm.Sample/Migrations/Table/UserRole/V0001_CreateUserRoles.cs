namespace SimpleOrm.Sample.Migrations.Table.UserRole;

/// <summary>Frozen to literal SQL when V0005 changed the table (ADR-0013/0016): the shape user_roles had at V0001.</summary>
public sealed class V0001_CreateUserRoles : TableMigration<Models.UserRole>
{
    public override void Action(TableActions actions)
    {
        actions.Sql(
            """
            create table if not exists user_roles (
                user_id     INTEGER NOT NULL,
                role_id     INTEGER NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT,
                primary key (user_id, role_id)
            ) STRICT
            """);
        actions.Sql("create index if not exists ix_user_roles_role_id on user_roles (role_id)");
    }
}

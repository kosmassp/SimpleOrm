namespace SimpleOrm.Sample.Migrations.Table.Role;

/// <summary>Frozen to literal SQL when V0004 renamed the column (ADR-0013/0016): the shape roles had at V0001.</summary>
public sealed class V0001_CreateRoles : TableMigration<Models.Role>
{
    /// <summary>Post-create hook: seed data belongs to the create action, not a separate mechanism.</summary>
    public override void Action(TableActions actions) => actions
        .Sql(
            """
            create table if not exists roles (
                id          INTEGER PRIMARY KEY,
                name        TEXT NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            """)
        .Post("insert into roles (name, created_at) values ('admin', '2026-01-01T00:00:00.0000000Z')");
}

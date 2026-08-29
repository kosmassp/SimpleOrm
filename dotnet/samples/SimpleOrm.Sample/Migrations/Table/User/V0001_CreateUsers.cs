namespace SimpleOrm.Sample.Migrations.Table.User;

/// <summary>
/// Frozen to literal SQL (ADR-0013): metadata-rendered creates are only safe while
/// the object never changes again — V0002 adds <c>display_name</c>, so V0001 must
/// stay the shape users had *then*, not whatever the entity looks like today.
/// This is the freeze the diff generator performs automatically when it emits a
/// follow-up migration for an object.
/// </summary>
public sealed class V0001_CreateUsers : TableMigration<Models.User>
{
    public override void Action(TableActions actions)
    {
        actions.Sql(
            """
            create table if not exists users (
                id          INTEGER PRIMARY KEY,
                name        TEXT NOT NULL,
                email       TEXT NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            """);
        actions.Sql("create unique index if not exists ix_users_email on users (email)");
    }
}

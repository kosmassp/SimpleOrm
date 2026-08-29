namespace SimpleOrm.Sample.Migrations.Table.Transaction;

/// <summary>Frozen to literal SQL when V0003 changed the table (ADR-0013/0016): the shape transactions had at V0001.</summary>
public sealed class V0001_CreateTransactions : TableMigration<Models.Transaction>
{
    public override void Action(TableActions actions)
    {
        actions.Sql(
            """
            create table if not exists transactions (
                id          INTEGER PRIMARY KEY,
                user_id     INTEGER NOT NULL,
                status      TEXT NOT NULL,
                amount      TEXT NOT NULL,
                version     INTEGER NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            """);
        actions.Sql("create index if not exists ix_transactions_user_id on transactions (user_id)");
        actions.Sql("create index if not exists ix_transactions_status_created on transactions (status, created_at desc)");
    }
}

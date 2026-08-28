namespace SimpleOrm.Sample;

/// <summary>
/// The sample schema as inline DDL commands (STRICT tables per ADR-0003). Interim:
/// milestone 5 moves this into versioned migration files; until then tests and
/// demos run these directly.
/// </summary>
public static class Schema
{
    public static readonly Command<EmptyArgs> CreateUsers = Query.Inline(
        """
        create table if not exists users (
            id          INTEGER PRIMARY KEY,
            name        TEXT NOT NULL,
            email       TEXT NOT NULL,
            created_at  TEXT NOT NULL,
            updated_at  TEXT
        ) STRICT
        """);

    public static readonly Command<EmptyArgs> CreateRoles = Query.Inline(
        """
        create table if not exists roles (
            id          INTEGER PRIMARY KEY,
            name        TEXT NOT NULL,
            created_at  TEXT NOT NULL,
            updated_at  TEXT
        ) STRICT
        """);

    public static readonly Command<EmptyArgs> CreateUserRoles = Query.Inline(
        """
        create table if not exists user_roles (
            user_id     INTEGER NOT NULL,
            role_id     INTEGER NOT NULL,
            created_at  TEXT NOT NULL,
            updated_at  TEXT,
            primary key (user_id, role_id)
        ) STRICT
        """);

    public static readonly Command<EmptyArgs> CreateTransactions = Query.Inline(
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

    public static readonly Command<EmptyArgs> CreateTransactionDetails = Query.Inline(
        """
        create table if not exists transaction_details (
            id              INTEGER PRIMARY KEY,
            transaction_id  INTEGER NOT NULL,
            description     TEXT NOT NULL,
            quantity        INTEGER NOT NULL,
            unit_price      TEXT NOT NULL,
            created_at      TEXT NOT NULL,
            updated_at      TEXT
        ) STRICT
        """);

    public static readonly Command<EmptyArgs> CreateUserTransactionTotalsView = Query.Inline(
        """
        create view if not exists user_transaction_totals as
        select u.id              as user_id,
               u.name            as user_name,
               count(t.id)       as transaction_count,
               coalesce(sum(t.amount), 0) as total_amount
        from users u
        left join transactions t on t.user_id = u.id
        group by u.id, u.name
        """);

    /// <summary>Everything, in dependency order.</summary>
    public static readonly IReadOnlyList<Command<EmptyArgs>> All =
    [
        CreateUsers,
        CreateRoles,
        CreateUserRoles,
        CreateTransactions,
        CreateTransactionDetails,
        CreateUserTransactionTotalsView,
    ];
}

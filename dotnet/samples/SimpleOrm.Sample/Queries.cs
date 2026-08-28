using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample;

/// <summary>
/// The sample query registry: SQL declared inline, next to its args and result
/// types (ADR-0009). This registry is what SchemaGuard enumerates at milestone 6.
/// </summary>
public static class Queries
{
    public static readonly Query<EmptyArgs, User> AllUsers = Query.Inline(
        """
        select id, name, email, created_at, updated_at
        from users
        order by id
        """);

    public static readonly Query<UserByIdArgs, User> UserById = Query.Inline(
        """
        select id, name, email, created_at, updated_at
        from users
        where id = @Id
        """);

    public static readonly Query<UserByEmailArgs, User> UserByEmail = Query.Inline(
        """
        select id, name, email, created_at, updated_at
        from users
        where email = @Email
        """);

    public static readonly Query<UsersByIdsArgs, User> UsersByIds = Query.Inline(
        """
        select id, name, email, created_at, updated_at
        from users
        where id in (@Ids)
        order by id
        """);

    public static readonly Query<TransactionsByUserArgs, Transaction> TransactionsByUser = Query.Inline(
        """
        select id, user_id, status, amount, version, created_at, updated_at
        from transactions
        where user_id = @UserId
        order by id
        """);

    public static readonly Query<TransactionsByStatusArgs, Transaction> TransactionsByStatus = Query.Inline(
        """
        select id, user_id, status, amount, version, created_at, updated_at
        from transactions
        where status = @Status
        order by id
        """);

    public static readonly Query<EmptyArgs, UserTransactionTotal> UserTransactionTotals = Query.Inline(
        """
        select user_id, user_name, transaction_count, total_amount
        from user_transaction_totals
        order by user_id
        """);
}

public sealed record UserByIdArgs(long Id);

public sealed record UserByEmailArgs(string Email);

public sealed record UsersByIdsArgs(IReadOnlyList<long> Ids);

public sealed record TransactionsByUserArgs(long UserId);

public sealed record TransactionsByStatusArgs(TransactionStatus Status);

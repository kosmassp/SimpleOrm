using SimpleOrm.Sample.Models;

namespace SimpleOrm.Sample;

/// <summary>The sample command registry: writes, SQL inline (ADR-0009).</summary>
public static class Commands
{
    public static readonly Command<InsertUserArgs> InsertUser = Query.Inline(
        """
        insert into users (name, email, created_at)
        values (@Name, @Email, @CreatedAt)
        """);

    public static readonly Command<InsertTransactionArgs> InsertTransaction = Query.Inline(
        """
        insert into transactions (user_id, status, amount, version, created_at)
        values (@UserId, @Status, @Amount, 0, @CreatedAt)
        """);

    public static readonly Command<SetTransactionStatusArgs> SetTransactionStatus = Query.Inline(
        """
        update transactions
        set status = @Status, updated_at = @UpdatedAt
        where id = @Id
        """);

    public static readonly Command<AssignRoleArgs> AssignRole = Query.Inline(
        """
        insert into user_roles (user_id, role_id, created_at)
        values (@UserId, @RoleId, @CreatedAt)
        """);
}

public sealed record InsertUserArgs(string Name, string Email, DateTime CreatedAt);

public sealed record InsertTransactionArgs(long UserId, TransactionStatus Status, decimal Amount, DateTime CreatedAt);

public sealed record SetTransactionStatusArgs(long Id, TransactionStatus Status, DateTime UpdatedAt);

public sealed record AssignRoleArgs(long UserId, long RoleId, DateTime CreatedAt);

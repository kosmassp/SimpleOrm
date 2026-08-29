namespace SimpleOrm.Sample.Migrations;

/// <summary>
/// The initial schema. Root versions are the recorded, checksummed unit; they
/// compose per-object steps in explicit order — tables in FK order, views last.
/// </summary>
public sealed class V0001 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.User.V0001_CreateUsers>()
        .Apply<Table.Role.V0001_CreateRoles>()
        .Apply<Table.UserRole.V0001_CreateUserRoles>()
        .Apply<Table.Transaction.V0001_CreateTransactions>()
        .Apply<Table.TransactionDetail.V0001_CreateTransactionDetails>()
        .Apply<View.UserTransactionTotal.V0001_CreateTotalsView>();
}

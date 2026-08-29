namespace SimpleOrm.Sample.Migrations;

/// <summary>A multi-object version: two steps, applied atomically in this order.</summary>
public sealed class V0005 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.UserRole.V0005_AddGrantedBy>()
        .Apply<Table.Role.V0005_SeedUserRole>();
}

namespace SimpleOrm.Sample.Migrations;

/// <summary>Level 3 M3: the soft-delete flag on roles.</summary>
public sealed class V0011 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.Role.V0011_AddIsDeleted>();
}

namespace SimpleOrm.Sample.Migrations;

/// <summary>Level 4, ADR-0042: the role audit table and the trigger that fills it — the table first, the trigger after.</summary>
public sealed class V0013 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.RoleChange.V0013_CreateRoleChanges>()
        .Apply<Trigger.RoleAuditTrigger.V0013_CreateRoleAudit>();
}

namespace SimpleOrm.Sample.Migrations.Trigger.RoleAuditTrigger;

/// <summary>
/// The initial create of the role audit trigger (ADR-0042), rendered from the
/// declaration the entity links — the trigger analog of <c>CreateView()</c>. The
/// step names the entity and the trigger class; the dialect renders the header,
/// the wrapper, and the token expansions. No Down — the runner derives the
/// rollback from the trigger snapshot (ADR-0018).
/// </summary>
public sealed class V0013_CreateRoleAudit : TriggerMigration<global::SimpleOrm.Sample.Models.Role, global::SimpleOrm.Sample.Models.RoleAuditTrigger>
{
    public override void Action(TriggerActions actions) => actions.CreateTrigger();
}

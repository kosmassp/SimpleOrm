namespace SimpleOrm.Sample.Models;

/// <summary>
/// The fixture trigger (Level 4, ADR-0042): after every update of a role, one
/// audit row per changed role into <c>role_changes</c>. Declared the way a
/// materialized view declares its SQL — the class carries the name, the
/// timing, the events, and <b>one</b> body — and linked from
/// <see cref="Role"/> with <c>[Trigger(typeof(RoleAuditTrigger))]</c>. The body
/// is set-based SQL over the changed rows; <c>{new: id, role_name}</c> is the
/// rows as a relation and <c>{now}</c> the UTC timestamp, each expanded by the
/// dialect (<c>inserted</c> and <c>todatetimeoffset(sysutcdatetime(), 0)</c> on
/// SQL Server, a one-row derived table over <c>new.</c> and <c>strftime</c> on
/// SQLite, the same over <c>new</c> and <c>now()</c> on PostgreSQL). Nothing here
/// names a dialect. Migration V0013 creates it (<c>Migrations/Trigger/RoleAuditTrigger/</c>).
/// </summary>
[Trigger("trg_roles_audit", TriggerTiming.After, TriggerEvent.Update, """
    insert into role_changes (role_id, role_name, changed_at)
    select n.id, n.role_name, {now}
    from {new: id, role_name} n;
    """)]
public sealed class RoleAuditTrigger
{
}

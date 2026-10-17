namespace SimpleOrm.Sample.Migrations.Table.UserProfile;

/// <summary>
/// The avatar image of UserProfile (ADR-0040 add.1) — the fixture's one bytes column, nullable,
/// so binary round trips are covered on every dialect. No Down — the runner derives the
/// rollback from the snapshots (ADR-0018).
/// </summary>
public sealed class V0012_AddAvatar : TableMigration<global::SimpleOrm.Sample.Models.UserProfile>
{
    public override void Action(TableActions actions)
    {
        actions.AddColumn("avatar", "BLOB");
    }
}

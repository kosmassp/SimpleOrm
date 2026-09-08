namespace SimpleOrm.Sample.Migrations.Table.UserProfile;

/// <summary>
/// The [Owned] Address of UserProfile (ADR-0030) as three nullable columns —
/// nullable because the navigation is: an all-NULL row reads back as no address.
/// No Down — the runner derives the rollback from the snapshots (ADR-0018).
/// </summary>
public sealed class V0010_AddAddress : TableMigration<global::SimpleOrm.Sample.Models.UserProfile>
{
    public override void Action(TableActions actions)
    {
        actions.AddColumn("address_street", "TEXT");
        actions.AddColumn("address_city", "TEXT");
        actions.AddColumn("address_postal_code", "TEXT");
    }
}

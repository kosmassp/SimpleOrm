namespace SimpleOrm.Sample.Migrations;

/// <summary>The owned-type columns (ADR-0030): user_profiles gains address_* for the [Owned] Address.</summary>
public sealed class V0010 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.UserProfile.V0010_AddAddress>();
}

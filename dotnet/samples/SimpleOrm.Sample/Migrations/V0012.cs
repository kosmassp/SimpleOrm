namespace SimpleOrm.Sample.Migrations;

/// <summary>ADR-0040 add.1: the fixture's bytes column on user profiles.</summary>
public sealed class V0012 : MigrationVersion
{
    public override void Compose(VersionBuilder version) => version
        .Apply<Table.UserProfile.V0012_AddAvatar>();
}

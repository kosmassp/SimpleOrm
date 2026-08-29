namespace SimpleOrm.Sample.Migrations.Table.User;

/// <summary>
/// A real change migration: literal column spec (frozen forever), with the
/// per-action Post hook backfilling existing rows — data work rides the same
/// version atomicity as the DDL.
/// </summary>
public sealed class V0002_AddDisplayName : TableMigration<Models.User>
{
    public override void Action(TableActions actions) => actions
        .AddColumn("display_name", "TEXT")
        .Post("update users set display_name = name");

    // No hand-written Down (owner decision, ADR-0016): rollbacks derive from the
    // versioned schema snapshots (diff V0002 vs V0001) once the generator exists;
    // until then, migrate down refuses honestly with MIG-020.
}

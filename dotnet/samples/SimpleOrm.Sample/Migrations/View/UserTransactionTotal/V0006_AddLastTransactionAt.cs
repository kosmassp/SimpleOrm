namespace SimpleOrm.Sample.Migrations.View.UserTransactionTotal;

/// <summary>
/// Views self-reflect (ADR-0008/0013): the defining SQL lives in the attribute, so a
/// view change is simply "recreate at this version" from the current definition.
/// </summary>
public sealed class V0006_AddLastTransactionAt : ViewMigration<Models.UserTransactionTotal>
{
    public override void Action(ViewActions actions) => actions.RecreateView();
}

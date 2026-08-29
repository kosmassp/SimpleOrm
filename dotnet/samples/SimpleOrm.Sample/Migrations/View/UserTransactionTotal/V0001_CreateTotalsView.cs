namespace SimpleOrm.Sample.Migrations.View.UserTransactionTotal;

public sealed class V0001_CreateTotalsView : ViewMigration<Models.UserTransactionTotal>
{
    public override void Action(ViewActions actions) => actions.CreateView();
}

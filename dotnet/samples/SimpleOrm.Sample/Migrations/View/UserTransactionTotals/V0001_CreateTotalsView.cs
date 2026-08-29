namespace SimpleOrm.Sample.Migrations.View.UserTransactionTotals;

public sealed class V0001_CreateTotalsView : ViewMigration<Models.UserTransactionTotal>
{
    public override void Action(ViewActions actions) => actions.CreateView();

    public override void Down(ViewActions actions) => actions.DropView();
}

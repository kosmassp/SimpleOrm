// Package usertransactiontotal holds the user_transaction_totals view's
// migration steps, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/View/UserTransactionTotal
// (CLAUDE.md §7.22).
package usertransactiontotal

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateTotalsView renders the view's initial creation from its
// defining SQL (the descriptor's Source).
type V0001_CreateTotalsView struct {
	orm.ViewMigration[sample.UserTransactionTotal]
}

func (V0001_CreateTotalsView) Action(actions *orm.ViewActions) {
	actions.CreateView()
}

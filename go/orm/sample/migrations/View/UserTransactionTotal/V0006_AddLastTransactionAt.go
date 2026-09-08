package usertransactiontotal

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0006_AddLastTransactionAt: views self-reflect (ADR-0008/0013), so a view
// change is simply "recreate at this version" from the current definition.
type V0006_AddLastTransactionAt struct {
	orm.ViewMigration[sample.UserTransactionTotal]
}

func (V0006_AddLastTransactionAt) Action(actions *orm.ViewActions) {
	actions.RecreateView()
}

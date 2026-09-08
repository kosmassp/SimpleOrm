// Package amendwidget holds the amend_widgets table's fixture migration steps.
package amendwidget

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/models"
)

// V0001_Create creates the fixture table.
type V0001_Create struct {
	orm.TableMigration[models.AmendWidget]
}

func (V0001_Create) Action(actions *orm.TableActions) {
	actions.SQL("create table if not exists amend_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT")
}

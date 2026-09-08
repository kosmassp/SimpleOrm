package amendwidget

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture/models"
)

// V0002_AddNote is the draft the model has since moved on from (see AmendWidget's doc comment).
type V0002_AddNote struct {
	orm.TableMigration[models.AmendWidget]
}

func (V0002_AddNote) Action(actions *orm.TableActions) {
	actions.AddColumn("note", "TEXT")
}

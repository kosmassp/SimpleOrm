package transaction

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0003_AddNote adds an optional free-text note.
type V0003_AddNote struct {
	orm.TableMigration[sample.Transaction]
}

func (V0003_AddNote) Action(actions *orm.TableActions) {
	actions.AddColumn("note", "TEXT")
}

package role

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0004_RenameNameToRoleName is a rename as a first-class action (never
// inferable by a differ): existing rows — including the V0001 'admin' seed —
// keep their data.
type V0004_RenameNameToRoleName struct {
	orm.TableMigration[sample.Role]
}

func (V0004_RenameNameToRoleName) Action(actions *orm.TableActions) {
	actions.RenameColumn("name", "role_name")
}

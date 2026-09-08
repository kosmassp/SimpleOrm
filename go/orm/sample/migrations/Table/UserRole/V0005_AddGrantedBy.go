package userrole

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0005_AddGrantedBy adds an optional column recording who granted the role.
type V0005_AddGrantedBy struct {
	orm.TableMigration[sample.UserRole]
}

func (V0005_AddGrantedBy) Action(actions *orm.TableActions) {
	actions.AddColumn("granted_by", "TEXT")
}

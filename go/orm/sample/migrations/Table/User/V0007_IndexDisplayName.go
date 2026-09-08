package user

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0007_IndexDisplayName is DDL plus a data step in one version: index the
// column, then backfill the gaps.
type V0007_IndexDisplayName struct {
	orm.TableMigration[sample.User]
}

func (V0007_IndexDisplayName) Action(actions *orm.TableActions) {
	actions.SQL("create index if not exists ix_users_display_name on users (display_name)")
	actions.SQL("update users set display_name = name where display_name is null")
}

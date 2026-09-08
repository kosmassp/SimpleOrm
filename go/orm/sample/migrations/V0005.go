package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Role"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/UserRole"
)

// V0005 is a multi-object version: two steps, applied atomically in this order.
type V0005 struct{}

func (V0005) Compose(version *orm.VersionBuilder) {
	version.
		Apply(userrole.V0005_AddGrantedBy{}).
		Apply(role.V0005_SeedUserRole{})
}

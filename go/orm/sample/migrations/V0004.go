package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Role"
)

type V0004 struct{}

func (V0004) Compose(version *orm.VersionBuilder) {
	version.Apply(role.V0004_RenameNameToRoleName{})
}

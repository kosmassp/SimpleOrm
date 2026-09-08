package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/User"
)

type V0002 struct{}

func (V0002) Compose(version *orm.VersionBuilder) {
	version.Apply(user.V0002_AddDisplayName{})
}

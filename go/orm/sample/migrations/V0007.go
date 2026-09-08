package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/User"
)

type V0007 struct{}

func (V0007) Compose(version *orm.VersionBuilder) {
	version.Apply(user.V0007_IndexDisplayName{})
}

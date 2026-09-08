package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Transaction"
)

type V0003 struct{}

func (V0003) Compose(version *orm.VersionBuilder) {
	version.Apply(transaction.V0003_AddNote{})
}

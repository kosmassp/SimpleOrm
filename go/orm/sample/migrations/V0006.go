package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/View/UserTransactionTotal"
)

type V0006 struct{}

func (V0006) Compose(version *orm.VersionBuilder) {
	version.Apply(usertransactiontotal.V0006_AddLastTransactionAt{})
}

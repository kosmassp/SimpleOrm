// Package migrations is the sample application's migrations tree, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations (CLAUDE.md §7.22-§7.24): root
// versions composing per-object steps, and the pinned schema snapshots. It
// imports only orm, never orm/migrations directly (CODING-STANDARD §10) —
// exactly what an application's own migrations tree looks like.
package migrations

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Role"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/Transaction"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/TransactionDetail"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/User"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/Table/UserRole"
	"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations/View/UserTransactionTotal"
)

// V0001 is the initial schema. Root versions are the recorded, checksummed
// unit; they compose per-object steps in explicit order — tables in FK order,
// views last.
type V0001 struct{}

func (V0001) Compose(version *orm.VersionBuilder) {
	version.
		Apply(user.V0001_CreateUsers{}).
		Apply(role.V0001_CreateRoles{}).
		Apply(userrole.V0001_CreateUserRoles{}).
		Apply(transaction.V0001_CreateTransactions{}).
		Apply(transactiondetail.V0001_CreateTransactionDetails{}).
		Apply(usertransactiontotal.V0001_CreateTotalsView{})
}

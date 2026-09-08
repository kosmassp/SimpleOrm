// Package transactiondetail holds the transaction_details table's migration
// step, mirroring
// dotnet/samples/SimpleOrm.Sample/Migrations/Table/TransactionDetail
// (CLAUDE.md §7.22).
package transactiondetail

import (
	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// V0001_CreateTransactionDetails renders its initial creation from metadata
// (CreateTable): the object's shape never changed after V0001, so there is no
// need to freeze it to literal SQL.
type V0001_CreateTransactionDetails struct {
	orm.TableMigration[sample.TransactionDetail]
}

func (V0001_CreateTransactionDetails) Action(actions *orm.TableActions) {
	actions.CreateTable()
}

package migrations

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// App is the sample application's registry (ADR-0027): the ten fixture
// entities (dotnet/samples/SimpleOrm.Sample/Models, one to one), a few
// registered queries/commands (sample/queries.go), this package's migration
// set, and its embedded snapshot tree — what SchemaGuard, the CLI, and their
// tests exercise in this port. It may import sample (the entity types) and
// orm; sample itself never imports this package (CODING-STANDARD §1 — a
// migrations tree reaches the library only through orm's aliases).
func App() (orm.Registry, error) {
	set, err := Set()
	if err != nil {
		return orm.Registry{}, err
	}

	return orm.Registry{
		Entities: []reflect.Type{
			orm.TypeOf[sample.User](),
			orm.TypeOf[sample.Role](),
			orm.TypeOf[sample.UserRole](),
			orm.TypeOf[sample.UserProfile](),
			orm.TypeOf[sample.Transaction](),
			orm.TypeOf[sample.TransactionDetail](),
			orm.TypeOf[sample.UserTransactionTotal](),
			orm.TypeOf[sample.MonthlySalesTotal](),
			orm.TypeOf[sample.DailySales](),
			orm.TypeOf[sample.UserActivityReport](),
		},
		Entries: []orm.Entry{
			sample.UsersByEmail,
			sample.UsersByIDs,
			sample.TransactionsByUser,
			sample.TransactionsByStatus,
			sample.MarkTransactionStatus,
		},
		Migrations: set,
		Snapshots:  Snapshots,
	}, nil
}

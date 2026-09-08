package migrations

import "github.com/kosmassp/SimpleOrm/go/orm"

// Set builds the sample application's validated migration set (§7.22): the
// ten root versions, in the order dotnet/samples/SimpleOrm.Sample/Migrations
// declares them.
func Set() (*orm.MigrationSet, error) {
	return orm.NewMigrationSet(
		V0001{}, V0002{}, V0003{}, V0004{}, V0005{}, V0006{}, V0007{}, V0008{}, V0009{}, V0010{},
	)
}

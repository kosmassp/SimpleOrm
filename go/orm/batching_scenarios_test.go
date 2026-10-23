package orm_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The shared batching scenarios (internal/testsupport/batching.go) run here on SQLite — the sequential realization,
// reusing prepared commands — so the bodies the PostgreSQL and SQL Server live legs run are proven against a database
// that is always there.

var sqliteBatchingLeg = testsupport.BatchingLeg{
	Open: func(t *testing.T, options orm.Options) (*orm.Db, *testsupport.ObservingDialect) {
		dialect := &testsupport.ObservingDialect{Dialect: sqlite.New()}
		options.Dialect = dialect
		return testsupport.OpenSampleWith(t, options), dialect
	},
}

func TestBatchingScenarios_OnSQLite(t *testing.T) {
	for name, scenario := range map[string]func(*testing.T, testsupport.BatchingLeg){
		"a batched flush":                 testsupport.BatchedFlush,
		"an explicit batch, every op":     testsupport.ExplicitBatchEveryOperation,
		"a stale version mid-message":     testsupport.StaleVersionMidMessage,
		"a failing statement":             testsupport.FailingStatementMidMessage,
		"the caller's transaction":        testsupport.InTheCallersTransaction,
		"a registry command among writes": testsupport.RegistryCommandAmongGeneratedWrites,
	} {
		t.Run(name, func(t *testing.T) { scenario(t, sqliteBatchingLeg) })
	}
}

package sqlserver_test

// The batching coverage of the live testing contract (Level 4 item 3, ADR-0044, spec/dialects.md "Statement
// batching" and "Testing contract"): a planned message goes out as one multi-statement command text — placeholders
// renamed apart per statement — and its results are read with NextResultSet. The scenarios are the ones every leg runs
// (internal/testsupport/batching.go).
//
// UNRUN against a server: the owner's SQL Server was off when this file was written, so the realization is built to the
// spec, its text and result reading are unit-tested without an engine (batch_test.go), and these tests compile, vet, and
// skip. The first green run happens where a server answers (SIMPLEORM_SQLSERVER).

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

var sqlServerBatchingLeg = testsupport.BatchingLeg{
	Open: func(t *testing.T, options orm.Options) (*orm.Db, *testsupport.ObservingDialect) {
		dialect := &testsupport.ObservingDialect{Dialect: sqlserver.New()}
		options.Dialect = dialect
		db, _ := newSampleSession(t, options)
		return db, dialect
	},
	BatchingInEffect:        true,
	IdentityIsNotRolledBack: true,
}

func TestLive_ABatchedFlushWritesGeneratedKeysAcrossMessages(t *testing.T) {
	testsupport.BatchedFlush(t, sqlServerBatchingLeg)
}

func TestLive_AnExplicitBatchCarriesEveryOperation(t *testing.T) {
	testsupport.ExplicitBatchEveryOperation(t, sqlServerBatchingLeg)
}

func TestLive_AStaleVersionInTheMiddleOfAMessageIsCRUD010WithNothingApplied(t *testing.T) {
	testsupport.StaleVersionMidMessage(t, sqlServerBatchingLeg)
}

func TestLive_AFailingStatementInTheMiddleOfAMessageIsTheProvidersErrorWithNothingApplied(t *testing.T) {
	testsupport.FailingStatementMidMessage(t, sqlServerBatchingLeg)
}

func TestLive_ABatchInsideTheCallersTransactionLeavesItToTheCaller(t *testing.T) {
	testsupport.InTheCallersTransaction(t, sqlServerBatchingLeg)
}

func TestLive_ARegistryCommandIsSentOnItsOwnBetweenTheMessagesOfTheGeneratedWrites(t *testing.T) {
	testsupport.RegistryCommandAmongGeneratedWrites(t, sqlServerBatchingLeg)
}

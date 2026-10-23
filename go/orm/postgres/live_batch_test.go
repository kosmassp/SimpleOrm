package postgres_test

// The batching coverage of the live testing contract (Level 4 item 3, ADR-0044, spec/dialects.md "Statement
// batching" and "Testing contract"): pgx's Batch carries each planned message as one pipeline on the session's
// connection. The scenarios are the ones every leg runs (internal/testsupport/batching.go); the tests after them are
// PostgreSQL's own — a cached plan that changes under a batch, and the pipeline's behaviour when the server rejects a
// statement.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

var postgresBatchingLeg = testsupport.BatchingLeg{
	Open: func(t *testing.T, options orm.Options) (*orm.Db, *testsupport.ObservingDialect) {
		dialect := &testsupport.ObservingDialect{Dialect: postgres.New()}
		options.Dialect = dialect
		return testsupport.OpenSampleAt(t, newDatabase(t), options), dialect
	},
	BatchingInEffect:        true,
	IdentityIsNotRolledBack: true,
}

func TestLive_ABatchedFlushWritesGeneratedKeysAcrossMessages(t *testing.T) {
	testsupport.BatchedFlush(t, postgresBatchingLeg)
}

func TestLive_AnExplicitBatchCarriesEveryOperation(t *testing.T) {
	testsupport.ExplicitBatchEveryOperation(t, postgresBatchingLeg)
}

func TestLive_AStaleVersionInTheMiddleOfAMessageIsCRUD010WithNothingApplied(t *testing.T) {
	testsupport.StaleVersionMidMessage(t, postgresBatchingLeg)
}

func TestLive_AFailingStatementInTheMiddleOfAMessageIsTheProvidersErrorWithNothingApplied(t *testing.T) {
	testsupport.FailingStatementMidMessage(t, postgresBatchingLeg)
}

func TestLive_ABatchInsideTheCallersTransactionLeavesItToTheCaller(t *testing.T) {
	testsupport.InTheCallersTransaction(t, postgresBatchingLeg)
}

func TestLive_AMultiStatementRegistryCommandGoesOnItsOwnBetweenPipelines(t *testing.T) {
	testsupport.RegistryCommandAmongGeneratedWrites(t, postgresBatchingLeg)
}

// TestLive_APipelineStopsAtTheStatementTheServerRejects pins what the spec's Failure paragraph says of a provider
// error in the middle of a message: the server skips what follows the rejected statement up to the Sync, so the
// error is the provider's own (the unique violation, SQLSTATE 23505), the rows before it are the transaction's to
// roll back, and the statements after it never ran — the identity sequence they would have consumed is untouched.
func TestLive_APipelineStopsAtTheStatementTheServerRejects(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresBatchingLeg.Open(t, orm.Options{Batching: orm.BatchingAuto})
	if !db.BatchingInEffect() {
		t.Fatal("the PostgreSQL dialect batches through pgx")
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	seed := &sample.Role{Name: "seed"}
	if err := orm.Insert(ctx, db, seed); err != nil {
		t.Fatal(err)
	}

	batch := db.Batch()
	_ = orm.BatchInsert(batch, &sample.User{Name: "Impostor", Email: "ada@example.com"})
	_ = orm.BatchInsert(batch, &sample.Role{Name: "never runs"})
	_, err := batch.Send(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected the provider's unique violation, got %v", err)
	}
	after := &sample.Role{Name: "after"}
	if err := orm.Insert(ctx, db, after); err != nil {
		t.Fatal(err)
	}
	if after.ID != seed.ID+1 {
		t.Errorf("the statement after the rejected one never ran (key %d, want %d)", after.ID, seed.ID+1)
	}
}

var touchNote = orm.InlineCommand[planArgs]("update plan_rows set note = note where id = @ID returning note")

// TestLive_ABatchedCallSurfacesACachedPlanErrorAsTX002AndTheRerunWorks pins Clarification 15 for the batch path: a
// batched call is always in a transaction (its own or the caller's), so PostgreSQL has aborted it when the cached
// plan of a statement in the pipeline changes under it — the error is TX-002 naming the statement, never retried,
// and rerunning the call (pgx dropped the stale entry on the error) re-prepares it.
func TestLive_ABatchedCallSurfacesACachedPlanErrorAsTX002AndTheRerunWorks(t *testing.T) {
	ctx := context.Background()
	connection := newDatabase(t)
	execRaw(t, connection, "create table plan_rows (id bigint primary key, note text not null)")
	execRaw(t, connection, "insert into plan_rows (id, note) values (1, 'kept')")
	db, err := orm.Open(ctx, connection, orm.Options{Dialect: postgres.New(), Clock: testsupport.FixedClock, Batching: orm.BatchingAuto})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	send := func() error {
		batch := db.Batch()
		if err := orm.BatchExecute(batch, touchNote, planArgs{ID: 1}); err != nil {
			t.Fatal(err)
		}
		_, err := batch.Send(ctx)
		return err
	}
	if err := send(); err != nil {
		t.Fatalf("first send (prepares and caches the statement): %v", err)
	}
	if _, err := orm.Execute(ctx, db, noteAsVarchar, orm.EmptyArgs{}); err != nil {
		t.Fatal(err)
	}

	err = send()
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != "TX-002" {
		t.Fatalf("expected TX-002, got %v", err)
	}
	if !strings.Contains(ormErr.Target, "plan_rows") || !strings.Contains(ormErr.Message, "roll it back and rerun the whole transaction") {
		t.Errorf("TX-002 names the statement and the transaction's remedy, got %q / %q", ormErr.Target, ormErr.Message)
	}
	if cause := errors.Unwrap(ormErr); cause == nil || !strings.Contains(cause.Error(), "cached plan must not change result type") {
		t.Errorf("TX-002 wraps pgx's 0A000 error, got %v", cause)
	}
	if err := send(); err != nil {
		t.Fatalf("the rerun re-prepares the statement: %v", err)
	}
}

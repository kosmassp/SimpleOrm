package orm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers resilience (Level 4, ADR-0047, spec/session.md "Resilience"): the open retry — only a transient
// open, a fixed delay the context can interrupt, the failure as it was raised — and the command timeout reaching every
// statement the session sends, through a SQLite dialect decorator that counts and fails the dials and records each
// statement's context deadline (testsupport.ResilienceDialect, real temp-file databases).

func openResilient(t *testing.T, dialect *testsupport.ResilienceDialect, options orm.Options) (*orm.Db, error) {
	t.Helper()
	options.Dialect = dialect
	db, err := orm.Open(context.Background(), testsupport.TempDatabase(t), options)
	if db != nil {
		t.Cleanup(func() { _ = db.Close() })
	}
	return db, err
}

// --- the open ---------------------------------------------------------------------

func TestOpenRefusesNegativeResilienceOptionsBeforeAnyAttempt(t *testing.T) {
	for name, options := range map[string]orm.Options{
		"OpenRetries":    {OpenRetries: -1},
		"OpenRetryDelay": {OpenRetryDelay: -time.Second},
		"CommandTimeout": {CommandTimeout: -time.Second},
	} {
		dialect := testsupport.NewResilienceDialect()
		if _, err := openResilient(t, dialect, options); err == nil {
			t.Errorf("%s: a negative value opened", name)
		}
		if dialect.Creates() != 0 {
			t.Errorf("%s: %d attempts before the refusal", name, dialect.Creates())
		}
	}
}

func TestOpenWithoutRetriesMakesOneAttempt(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 1
	_, err := openResilient(t, dialect, orm.Options{})
	if err != testsupport.ErrFlakyOpen {
		t.Fatalf("expected the failure as it was raised, got %v", err)
	}
	if dialect.Creates() != 1 || dialect.Dials() != 1 {
		t.Fatalf("attempts: %d pools, %d dials; want 1", dialect.Creates(), dialect.Dials())
	}
}

func TestOpenRetriesTransientFailuresThenSucceeds(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 2
	db, err := openResilient(t, dialect, orm.Options{OpenRetries: 3, OpenRetryDelay: orm.NoOpenRetryDelay})
	if err != nil {
		t.Fatal(err)
	}
	if dialect.Creates() != 3 {
		t.Fatalf("attempts: got %d, want 3 (two failures, then the success)", dialect.Creates())
	}
	one, err := orm.QuerySingle(context.Background(), db, orm.Inline[orm.EmptyArgs, int64]("select 1"), orm.EmptyArgs{})
	if err != nil || one != 1 {
		t.Fatalf("the opened session: %v, %v", one, err)
	}
}

func TestOpenSpendsItsRetriesAndReturnsTheLastFailure(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 10
	_, err := openResilient(t, dialect, orm.Options{OpenRetries: 2, OpenRetryDelay: orm.NoOpenRetryDelay})
	if err != testsupport.ErrFlakyOpen {
		t.Fatalf("expected the last attempt's failure, got %v", err)
	}
	if dialect.Creates() != 3 {
		t.Fatalf("attempts: got %d, want OpenRetries + 1 = 3", dialect.Creates())
	}
}

func TestOpenDoesNotRetryANonTransientFailure(t *testing.T) {
	wrongPassword := errors.New("password authentication failed")
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens, dialect.Failure = 5, wrongPassword
	_, err := openResilient(t, dialect, orm.Options{OpenRetries: 3, OpenRetryDelay: orm.NoOpenRetryDelay})
	if err != wrongPassword {
		t.Fatalf("expected the failure as it was raised, got %v", err)
	}
	if dialect.Creates() != 1 {
		t.Fatalf("attempts: got %d, want 1", dialect.Creates())
	}
}

func TestOpenWaitsTheDelayBetweenAttempts(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 2
	started := time.Now()
	if _, err := openResilient(t, dialect, orm.Options{OpenRetries: 2, OpenRetryDelay: 100 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
		t.Fatalf("two waits of 100 ms took %s", elapsed)
	}
}

func TestOpenRetryDelayDefaultsToOneSecond(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 1
	started := time.Now()
	if _, err := openResilient(t, dialect, orm.Options{OpenRetries: 1}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("the default wait took %s; want one second", elapsed)
	}
}

func TestOpenStopsWhenTheContextEndsDuringTheDelay(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	dialect.FailOpens = 5
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	started := time.Now()
	_, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: dialect, OpenRetries: 5, OpenRetryDelay: 10 * time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the context's error, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the cancellation did not interrupt the wait (%s)", elapsed)
	}
	if dialect.Creates() != 1 {
		t.Fatalf("attempts: got %d, want 1 (no attempt after the cancellation)", dialect.Creates())
	}
}

func TestOpenNeverRetriesACancellation(t *testing.T) {
	// A failure the dialect would call transient, but that is the platform's cancellation signal.
	dialect := testsupport.NewResilienceDialect()
	cancelled := errors.Join(testsupport.ErrFlakyOpen, context.Canceled)
	dialect.FailOpens, dialect.Failure = 5, cancelled
	if _, err := openResilient(t, dialect, orm.Options{OpenRetries: 3, OpenRetryDelay: orm.NoOpenRetryDelay}); err != cancelled {
		t.Fatalf("expected the cancellation as it was raised, got %v", err)
	}
	if dialect.Creates() != 1 {
		t.Fatalf("attempts: got %d, want 1", dialect.Creates())
	}

	// The caller's own context, already ended: one attempt, the context's error.
	dialect = testsupport.NewResilienceDialect()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: dialect, OpenRetries: 3}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the caller's cancellation, got %v", err)
	}
	if dialect.Creates() != 1 {
		t.Fatalf("attempts: got %d, want 1", dialect.Creates())
	}
}

func TestSQLiteCallsNoOpenFailureTransient(t *testing.T) {
	if sqlite.New().IsTransient(testsupport.ErrFlakyOpen) || sqlite.New().IsTransient(context.DeadlineExceeded) {
		t.Fatal("a SQLite open fails the same way the second time")
	}
}

// --- the command timeout ------------------------------------------------------------

var (
	usersNamed  = orm.Inline[struct{ Name string }, sample.User]("select id, name, email, display_name, created_at, updated_at from users where name = @Name")
	renameUsers = orm.InlineCommand[struct{ Name string }]("update users set display_name = @Name")
)

// sessionWorkload sends a statement down every path the session has: DDL, a generated insert, update, column-list
// update, and delete, a key read, a registry query and command, a stream, a criteria query, a select-all, and
// explicit loading.
func sessionWorkload(t *testing.T, db *orm.Db) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(orm.CreateTable[sample.User](ctx, db))
	must(orm.CreateTable[sample.Transaction](ctx, db))
	user := &sample.User{Name: "Ada", Email: "ada@example.com"}
	user.CreatedAtUtc = testsupport.SeedTime
	must(orm.Insert(ctx, db, user))
	_, err := orm.Get[sample.User](ctx, db, user.ID)
	must(err)
	_, err = orm.Query(ctx, db, usersNamed, struct{ Name string }{"Ada"})
	must(err)
	for _, err := range orm.Stream(ctx, db, usersNamed, struct{ Name string }{"Ada"}) {
		must(err)
	}
	_, err = orm.Execute(ctx, db, renameUsers, struct{ Name string }{"A."})
	must(err)
	_, err = orm.From[sample.User](db).Where(orm.Eq("Name", "Ada")).List(ctx)
	must(err)
	_, err = orm.QueryAll[sample.User](ctx, db)
	must(err)
	must(orm.Load(ctx, db, user, "Transactions"))
	user.DisplayName = ptr("Ada L.")
	must(orm.Update(ctx, db, user))
	must(orm.UpdateOnly(ctx, db, user, "Email"))
	must(orm.DeleteEntity(ctx, db, user))
}

func ptr[T any](value T) *T { return &value }

func TestCommandTimeoutReachesEveryStatementTheSessionSends(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	db, err := openResilient(t, dialect, orm.Options{CommandTimeout: 300 * time.Millisecond, Clock: testsupport.FixedClock})
	if err != nil {
		t.Fatal(err)
	}
	sessionWorkload(t, db)
	statements := dialect.Statements()
	if len(statements) < 14 {
		t.Fatalf("expected a statement per path, got %d: %v", len(statements), statements)
	}
	for _, statement := range statements {
		assertWholeSecondDeadline(t, statement)
	}
}

func TestCommandTimeoutReachesTheFlushAndTheExplicitBatch(t *testing.T) {
	for _, batching := range []orm.BatchingMode{orm.BatchingOff, orm.BatchingAuto} {
		dialect := testsupport.NewResilienceDialect()
		db, err := openResilient(t, dialect, orm.Options{
			CommandTimeout: time.Second, Clock: testsupport.FixedClock, Tracking: orm.TrackingTracked, Batching: batching,
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := orm.CreateTable[sample.User](ctx, db); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"Ada", "Grace"} {
			user := &sample.User{Name: name, Email: name + "@example.com"}
			user.CreatedAtUtc = testsupport.SeedTime
			if err := orm.Persist(db, user); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		batch := db.Batch()
		if err := orm.BatchExecute(batch, renameUsers, struct{ Name string }{"B."}); err != nil {
			t.Fatal(err)
		}
		if _, err := batch.Send(ctx); err != nil {
			t.Fatal(err)
		}
		for _, statement := range dialect.Statements() {
			assertWholeSecondDeadline(t, statement)
		}
	}
}

func TestCommandTimeoutUnsetLeavesTheCallersContextUntouched(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	db, err := openResilient(t, dialect, orm.Options{Clock: testsupport.FixedClock})
	if err != nil {
		t.Fatal(err)
	}
	sessionWorkload(t, db)
	for _, statement := range dialect.Statements() {
		if !statement.Deadline.IsZero() {
			t.Fatalf("without a timeout %q ran under a deadline", statement.SQL)
		}
	}
}

// On SQLite a statement outrunning the timeout is interrupted by the driver (modernc honours the context with
// sqlite3_interrupt): the error is the driver's, the caller's context is live, and the next statement runs.
func TestATimedOutStatementIsTheDriversErrorAndTheNextStatementRuns(t *testing.T) {
	db := testsupport.OpenSampleWith(t, orm.Options{CommandTimeout: time.Second})
	ctx := context.Background()
	endless := orm.Inline[orm.EmptyArgs, int64](
		"with recursive c(x) as (select 1 union all select x + 1 from c where x < 4000000000) select count(*) as n -- notnull: n\nfrom c")

	started := time.Now()
	_, err := orm.QuerySingle(ctx, db, endless, orm.EmptyArgs{})
	if err == nil {
		t.Fatal("a four-billion-row count finished inside one second")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("the timeout fired after %s", elapsed)
	}
	t.Logf("the timeout surfaced as %T: %v (errors.Is DeadlineExceeded: %v)", err, err, errors.Is(err, context.DeadlineExceeded))
	if orm.CodeOf(err) != "" {
		t.Fatalf("a timeout is the driver's error, never a SimpleOrm code: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the caller's context ended")
	}
	one, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, int64]("select 1"), orm.EmptyArgs{})
	if err != nil || one != 1 {
		t.Fatalf("the next statement after the timeout: %v, %v", one, err)
	}
}

// assertWholeSecondDeadline: a 300 ms or 1 s timeout is a fresh one-second deadline per statement.
func assertWholeSecondDeadline(t *testing.T, statement testsupport.ObservedStatement) {
	t.Helper()
	if statement.Deadline.IsZero() {
		t.Fatalf("%q ran without the command timeout", statement.SQL)
	}
	if statement.Remaining <= 500*time.Millisecond || statement.Remaining > time.Second {
		t.Fatalf("%q ran with %s left; expected a fresh one-second deadline", statement.SQL, statement.Remaining)
	}
}

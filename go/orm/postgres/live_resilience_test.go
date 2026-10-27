package postgres_test

// The resilience legs of the PostgreSQL dialect (Level 4, ADR-0047, spec/dialects.md "Testing contract"): a statement
// outrunning a one-second CommandTimeout surfaces the driver's own error and the session's next statement runs; an open
// against an unreachable port is attempted OpenRetries + 1 times and surfaces the driver's failure; an open of a missing
// database (3D000) is attempted once.

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
)

func TestLiveCommandTimeoutSurfacesTheDriversErrorAndTheNextStatementRuns(t *testing.T) {
	db, _ := openBare(t, orm.Options{CommandTimeout: time.Second})
	ctx := context.Background()

	started := time.Now()
	_, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, *string]("select pg_sleep(3)::text"), orm.EmptyArgs{})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("pg_sleep(3) under a one-second timeout succeeded")
	}
	if elapsed >= 2500*time.Millisecond {
		t.Fatalf("the timeout fired after %s; expected about one second", elapsed)
	}
	// The deadline cancels the statement on the server (a CancelRequest), so pgx hands back the server's own answer:
	// SQLSTATE 57014, unwrapped, while the caller's context is still live (CODING-STANDARD §10).
	if !isQueryCanceled(err) {
		t.Fatalf("expected the driver's error, SQLSTATE 57014, got %T: %v", err, err)
	}
	if ctx.Err() != nil {
		t.Fatal("the caller's context ended; the timeout must be the session's own")
	}
	assertNextStatementRuns(t, db)
}

func TestLiveCommandTimeoutBoundsABatchedMessage(t *testing.T) {
	db, _ := openBare(t, orm.Options{CommandTimeout: time.Second, Batching: orm.BatchingAuto})
	if !db.BatchingInEffect() {
		t.Fatal("PostgreSQL batches under Auto")
	}
	batch := db.Batch()
	sleep := orm.InlineCommand[orm.EmptyArgs]("select pg_sleep(3)")
	for range 2 {
		if err := orm.BatchExecute(batch, sleep, orm.EmptyArgs{}); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	_, err := batch.Send(context.Background())
	if elapsed := time.Since(started); err == nil || elapsed >= 2500*time.Millisecond {
		t.Fatalf("a two-statement message of pg_sleep(3) under a one-second timeout: %v after %s", err, elapsed)
	}
	assertNextStatementRuns(t, db)
}

// assertNextStatementRuns is the testing contract's "the session's next statement runs" on the same connection.
func assertNextStatementRuns(t *testing.T, db *orm.Db) {
	t.Helper()
	one, err := orm.QuerySingle(context.Background(), db, orm.Inline[orm.EmptyArgs, int64]("select 1::bigint"), orm.EmptyArgs{})
	if err != nil || one != 1 {
		t.Fatalf("the next statement after the timeout: %v, %v", one, err)
	}
}

// The connector cancels on the server (CancelRequestContextWatcherHandler) for the caller's own cancellation too: the
// running statement ends, the caller sees its cancellation, and the session's pinned connection survives it (the
// review of ADR-0047's pgx change).
func TestLiveCallersCancellationCancelsTheStatementAndTheConnectionSurvives(t *testing.T) {
	db, _ := openBare(t, orm.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(300*time.Millisecond, cancel)
	defer timer.Stop()

	started := time.Now()
	_, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, *string]("select pg_sleep(3)::text"), orm.EmptyArgs{})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("pg_sleep(3) under a cancelled context succeeded")
	}
	if elapsed >= 2500*time.Millisecond {
		t.Fatalf("the cancellation took effect after %s; expected about 300 ms", elapsed)
	}
	if !errors.Is(err, context.Canceled) && !isQueryCanceled(err) {
		t.Fatalf("expected the caller's cancellation or SQLSTATE 57014, got %T: %v", err, err)
	}
	assertNextStatementRuns(t, db)
}

func TestLiveCallersCancellationInsideATransactionLeavesARollbackAndTheNextStatement(t *testing.T) {
	db, _ := openBare(t, orm.Options{})
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(300*time.Millisecond, cancel)
	defer timer.Stop()

	if _, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, *string]("select pg_sleep(3)::text"), orm.EmptyArgs{}); err == nil {
		t.Fatal("pg_sleep(3) under a cancelled context succeeded")
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("the rollback after a cancelled statement: %v", err)
	}
	assertNextStatementRuns(t, db)
}

func TestLiveCommandTimeoutLeavesAFastStatementAlone(t *testing.T) {
	db, _ := openBare(t, orm.Options{CommandTimeout: time.Second})
	got, err := orm.QuerySingle(context.Background(), db, orm.Inline[orm.EmptyArgs, *string]("select pg_sleep(0.1)::text"), orm.EmptyArgs{})
	if err != nil || got == nil {
		t.Fatalf("a statement inside the timeout: %v, %v", got, err)
	}
}

func isQueryCanceled(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "57014"
}

// countingDialect is the PostgreSQL dialect with its connection creations counted: one per open attempt.
type countingDialect struct {
	*postgres.Dialect
	attempts int
}

func (d *countingDialect) CreateConnection(connectionString string) (*sql.DB, error) {
	d.attempts++
	return d.Dialect.CreateConnection(connectionString)
}

func TestLiveOpenOfAnUnreachablePortIsAttemptedOpenRetriesPlusOneTimes(t *testing.T) {
	server.SkipUnlessAvailable(t, noServer)
	port := closedPort(t)
	unreachable := "postgres://postgres:postgres@127.0.0.1:" + strconv.Itoa(port) + "/postgres?sslmode=disable&connect_timeout=2"

	dialect := &countingDialect{Dialect: postgres.New()}
	_, err := orm.Open(context.Background(), unreachable, orm.Options{Dialect: dialect, OpenRetries: 2, OpenRetryDelay: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("an open against a closed port succeeded")
	}
	if dialect.attempts != 3 {
		t.Fatalf("attempts: got %d, want OpenRetries + 1 = 3 (%v)", dialect.attempts, err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("expected the driver's dial failure, got %T: %v", err, err)
	}
	if !dialect.IsTransient(err) {
		t.Fatalf("the dialect does not call the refused connection transient: %v", err)
	}
}

func TestLiveOpenOfAMissingDatabaseIsAttemptedOnce(t *testing.T) {
	admin := server.AdminConnectionString()
	server.SkipUnlessAvailable(t, noServer)
	u, err := url.Parse(admin)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Skip("the admin connection string is not a URL")
	}
	u.Path = "/simpleorm_missing_database_" + strconv.FormatInt(time.Now().UnixNano(), 36)

	dialect := &countingDialect{Dialect: postgres.New()}
	_, err = orm.Open(context.Background(), u.String(), orm.Options{Dialect: dialect, OpenRetries: 3, OpenRetryDelay: orm.NoOpenRetryDelay})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "3D000" {
		t.Fatalf("expected SQLSTATE 3D000, got %T: %v", err, err)
	}
	if dialect.attempts != 1 {
		t.Fatalf("attempts: got %d, want 1 (3D000 is not transient)", dialect.attempts)
	}
}

// closedPort is a local TCP port nothing listens on: bound, read, and released.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

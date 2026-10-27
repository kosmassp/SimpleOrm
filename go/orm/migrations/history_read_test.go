package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The runner never mistakes a failure for an empty history (ADR-0047, spec/migrations.md "Recording and integrity"):
// status reads the history only when the version table exists by introspection; migrate, migrate down, and baseline
// create it first, so a failing read fails the run with the driver's error.

func widgetSet(t *testing.T) *migrations.Set {
	t.Helper()
	set, err := migrations.NewSet(rawTableVersion(
		1001, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestStatusOnAFreshDatabaseReportsEverythingPendingAndCreatesNothing(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), widgetSet(t), nil)

	entries, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].State != migrations.Pending {
		t.Fatalf("status on a fresh database: %v", entries)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	columns, err := migrations.ReadLiveColumns(ctx, conn, sqlite.New(), "schema_version")
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 0 {
		t.Fatalf("status created the version table: %v", columns)
	}
}

// A version table the history read cannot read (here: a table of that name with another shape) fails every command
// with the driver's error -- before ADR-0047 the read's failure was taken for an empty history and migrate re-applied
// every version.
func TestAFailingHistoryReadFailsMigrateStatusAndBaseline(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	if _, err := pool.ExecContext(ctx, "create table schema_version (unrelated TEXT)"); err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), widgetSet(t), nil)

	assertDriverError := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: a failing history read was taken for an empty history", what)
		}
		if !strings.Contains(err.Error(), "no such column") {
			t.Fatalf("%s: expected the driver's error, got %v", what, err)
		}
	}
	_, err := runner.Migrate(ctx, migrations.RunOptions{})
	assertDriverError("migrate", err)
	_, err = runner.Status(ctx)
	assertDriverError("status", err)
	assertDriverError("baseline", runner.Baseline(ctx, 1001))
	_, err = runner.MigrateDown(ctx, 0, migrations.RunOptions{})
	assertDriverError("migrate down", err)

	var widgets int
	if err := pool.QueryRowContext(ctx, "select count(*) from sqlite_master where name = 'widgets'").Scan(&widgets); err != nil {
		t.Fatal(err)
	}
	if widgets != 0 {
		t.Fatal("migrate applied a version over an unreadable history")
	}
}

// The migration tooling takes the command timeout through the context (WithCommandTimeout): every statement the runner
// sends -- the version table, the history, the steps, the recording -- runs under a deadline of whole seconds, and the
// run lock (the dialect's own BEGIN IMMEDIATE) does not.
func TestTheRunnerSendsEveryStatementUnderTheCommandTimeout(t *testing.T) {
	dialect := testsupport.NewResilienceDialect()
	pool, err := dialect.CreateConnection("Data Source=" + testsupport.TempDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	runner := migrations.NewRunner(pool, dialect, metadata.NewLoader(nil), widgetSet(t), nil)

	ctx := migrations.WithCommandTimeout(context.Background(), 300*time.Millisecond)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Status(ctx); err != nil {
		t.Fatal(err)
	}
	statements := dialect.Statements()
	if len(statements) < 5 {
		t.Fatalf("expected the version table, the history, the step, the record, and status's reads; got %v", statements)
	}
	for _, statement := range statements {
		assertWholeSecondDeadline(t, statement)
	}

	// Unset: the caller's context reaches the driver untouched.
	if _, err := runner.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range dialect.Statements() {
		if !statement.Deadline.IsZero() {
			t.Fatalf("without a timeout %q ran under a deadline", statement.SQL)
		}
	}
}

// assertWholeSecondDeadline: a 300 ms timeout is one second per statement (rounded up, at least 1).
func assertWholeSecondDeadline(t *testing.T, statement testsupport.ObservedStatement) {
	t.Helper()
	if statement.Deadline.IsZero() {
		t.Fatalf("%q ran without the command timeout", statement.SQL)
	}
	if statement.Remaining <= 500*time.Millisecond || statement.Remaining > time.Second {
		t.Fatalf("%q ran with %s left; expected a fresh one-second deadline", statement.SQL, statement.Remaining)
	}
}

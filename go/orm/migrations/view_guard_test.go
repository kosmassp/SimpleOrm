package migrations_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// ADR-0017 add.1 (owner): views get adjusted outside the code in urgencies,
// so a view step's ExpectDefinition guard compares the live definition
// against the expected previous one before applying — match applies, drift
// refuses with MIG-012, and only --force recreates over the drift (with
// notice). Mirrors ViewGuardTests.cs.

type GuardedTotals struct {
	Answer int64
}

func guardedTotalsMap() *core.EntityMap {
	t := reflect.TypeFor[GuardedTotals]()
	return core.NewEntityMap(
		t, core.RelationView, "guarded_totals", "", "select 1 as answer", nil,
		[]*core.PropertyMap{property(t, "Answer", "answer", core.TypeInt64)}, core.KeyNone, nil, nil)
}

func guardedTotalsLoader() *metadata.Loader {
	options := (&metadata.Options{}).Register(explicitMap{entityType: reflect.TypeFor[GuardedTotals](), m: guardedTotalsMap()})
	return metadata.NewLoader(options)
}

const guardV1DDL = "create view guarded_totals as select 1 as answer"

type V1301_CreateGuarded struct {
	migrations.ViewMigration[GuardedTotals]
}

func (V1301_CreateGuarded) Action(a *migrations.ViewActions) { a.SQL(guardV1DDL) }

type V1301 struct{}

func (V1301) Compose(v *migrations.VersionBuilder) { v.Apply(V1301_CreateGuarded{}) }

type V1302_ChangeGuarded struct {
	migrations.ViewMigration[GuardedTotals]
}

const guardV2DDL = "create view guarded_totals as select 2 as answer"

func (V1302_ChangeGuarded) Action(a *migrations.ViewActions) {
	a.ExpectDefinition(guardV1DDL)
	a.SQL("drop view if exists guarded_totals")
	a.SQL(guardV2DDL)
}

// Down is the manual override, mirroring the guard in reverse (ADR-0017 add.1):
// a rollback must not silently overwrite an outside hotfix either.
func (V1302_ChangeGuarded) Down(a *migrations.ViewActions) {
	a.ExpectDefinition(guardV2DDL)
	a.SQL("drop view if exists guarded_totals")
	a.SQL(guardV1DDL)
}

type V1302 struct{}

func (V1302) Compose(v *migrations.VersionBuilder) { v.Apply(V1302_ChangeGuarded{}) }

func guardedPlan(t *testing.T) *migrations.Set {
	t.Helper()
	set, err := migrations.NewSet(V1301{}, V1302{})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestViewGuard_MatchingPreviousDefinitionApplies(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	runner := migrations.NewRunner(pool, sqlite.New(), guardedTotalsLoader(), guardedPlan(t), nil)

	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("expected 2 applied versions, got %d", applied)
	}
	var answer int64
	if err := pool.QueryRowContext(ctx, "select answer from guarded_totals").Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != 2 {
		t.Fatalf("expected the V2 definition to apply, got answer=%d", answer)
	}
}

func TestViewGuard_OutsideDriftRefusesAndAppliesNothing(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	loader := guardedTotalsLoader()

	v1Only, err := migrations.NewSet(V1301{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), loader, v1Only, nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrations.ApplySync(ctx, conn,
		[]string{"drop view guarded_totals", "create view guarded_totals as select 99 as answer"}); err != nil {
		t.Fatal(err)
	}

	runner := migrations.NewRunner(pool, sqlite.New(), loader, guardedPlan(t), nil)
	_, err = runner.Migrate(ctx, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-012" {
		t.Fatalf("expected MIG-012, got %v", err)
	}

	// The whole run rolled back: the hotfixed definition is untouched.
	var answer int64
	if err := pool.QueryRowContext(ctx, "select answer from guarded_totals").Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != 99 {
		t.Fatalf("expected the hotfixed definition to survive the refused run, got answer=%d", answer)
	}
}

func TestViewGuard_ForceRecreatesOverDriftAndNotifies(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	loader := guardedTotalsLoader()

	v1Only, err := migrations.NewSet(V1301{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), loader, v1Only, nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrations.ApplySync(ctx, conn,
		[]string{"drop view guarded_totals", "create view guarded_totals as select 99 as answer"}); err != nil {
		t.Fatal(err)
	}

	var notices []string
	runner := migrations.NewRunner(pool, sqlite.New(), loader, guardedPlan(t), nil)
	applied, err := runner.Migrate(ctx, migrations.RunOptions{
		AllowViewDrift: true, Notify: func(n string) { notices = append(notices, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected 1 applied version (V1301 already recorded), got %d", applied)
	}
	if len(notices) != 1 {
		t.Fatalf("expected exactly one drift notice, got %v", notices)
	}

	var answer int64
	if err := pool.QueryRowContext(ctx, "select answer from guarded_totals").Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != 2 {
		t.Fatalf("expected the code's definition recreated, got answer=%d", answer)
	}
}

func TestViewGuard_DownIsGuardedTheSameWay(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	loader := guardedTotalsLoader()
	runner := migrations.NewRunner(pool, sqlite.New(), loader, guardedPlan(t), nil)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := migrations.ApplySync(ctx, conn,
		[]string{"drop view guarded_totals", "create view guarded_totals as select 99 as answer"}); err != nil {
		t.Fatal(err)
	}

	_, err = runner.MigrateDown(ctx, 1301, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-012" {
		t.Fatalf("expected MIG-012 on the way down too, got %v", err)
	}

	reverted, err := runner.MigrateDown(ctx, 1301, migrations.RunOptions{AllowViewDrift: true})
	if err != nil {
		t.Fatal(err)
	}
	if reverted != 1 {
		t.Fatalf("expected 1 reverted version, got %d", reverted)
	}
	var answer int64
	if err := pool.QueryRowContext(ctx, "select answer from guarded_totals").Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if answer != 1 {
		t.Fatalf("expected the V1 definition restored, got answer=%d", answer)
	}
}

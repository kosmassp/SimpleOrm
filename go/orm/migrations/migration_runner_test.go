package migrations_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	samplemigrations "github.com/kosmassp/SimpleOrm/go/orm/sample/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// Milestone-equivalent (§7.23): the versioned migration runner, per scenario
// and per code. Mirrors dotnet/tests/SimpleOrm.Tests/MigrationRunnerTests.cs.

func TestRunner_MigrateAppliesOnceAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	set, err := migrations.NewSet(rawTableVersion(
		1001, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), maps, set, nil)

	pending, err := runner.HasPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("expected a pending version before migrating")
	}

	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected 1 applied version, got %d", applied)
	}

	again, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("re-running should be idempotent, applied %d", again)
	}

	pending, err = runner.HasPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("expected no pending versions after migrating")
	}
}

// Gadget is a test-local fixture entity for the action-reordering test:
// TableActions must render rename -> add -> remove regardless of declaration
// order, with each action's Pre/Post hooks running in place.
type Gadget struct {
	ID    int64
	Title string
	Label string
}

func gadgetMap() *core.EntityMap {
	t := reflect.TypeFor[Gadget]()
	return core.NewEntityMap(t, core.RelationTable, "gadgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Title", "title", core.TypeString),
		property(t, "Label", "label", core.TypeString),
	}, core.KeyDatabaseGenerated, nil, nil)
}

func gadgetLoader() *metadata.Loader {
	options := (&metadata.Options{}).Register(explicitMap{entityType: reflect.TypeFor[Gadget](), m: gadgetMap()})
	return metadata.NewLoader(options)
}

// V1102_Restructure is deliberately declared out of execution order: the
// renderer must run rename -> add -> remove, and PreDown/Down/PostDown are
// the manual rollback override.
type V1102_Restructure struct {
	migrations.TableMigration[Gadget]
}

func (V1102_Restructure) Action(a *migrations.TableActions) {
	a.AddColumn("label", "TEXT").Post("update gadgets set label = 'L-' || title")
	a.RemoveColumn("legacy").Pre("update gadgets set title = title || '-' || legacy")
	a.RenameColumn("label", "title")
}

func (V1102_Restructure) Down(a *migrations.TableActions) { a.RemoveColumn("label") }

func (V1102_Restructure) PreDown(sql *migrations.MigrationSQL) {
	sql.SQL("update gadgets set title = label")
}

func (V1102_Restructure) PostDown(sql *migrations.MigrationSQL) {
	sql.SQL("update gadgets set title = title || '!'")
}

type V1102 struct{}

func (V1102) Compose(v *migrations.VersionBuilder) { v.Apply(V1102_Restructure{}) }

func TestRunner_ActionsReorderAndHooksRunInPlace(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)

	gadgetV1 := rawTableVersion(1101, "gadgets", "create", []string{
		"create table gadgets (id INTEGER PRIMARY KEY, label TEXT, legacy TEXT) STRICT",
		"insert into gadgets (label, legacy) values ('hello', 'old')",
	}, nil)

	set, err := migrations.NewSet(gadgetV1, V1102{})
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), gadgetLoader(), set, nil)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	var title, label string
	if err := pool.QueryRowContext(ctx, "select title, label from gadgets").Scan(&title, &label); err != nil {
		t.Fatal(err)
	}
	if title != "hello-old" {
		t.Errorf("expected the pre-remove hook to run after the add group: got title=%q", title)
	}
	if label != "L-hello" {
		t.Errorf("expected the post-add hook to backfill from the renamed column: got label=%q", label)
	}

	if _, err := runner.MigrateDown(ctx, 1101, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRowContext(ctx, "select title from gadgets").Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "L-hello!" {
		t.Errorf("expected PreDown -> DDL -> PostDown, got title=%q", title)
	}
}

func TestRunner_DownRevertsInReverseAndRequiresDownStatements(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	reversibleSet, err := migrations.NewSet(rawTableVersion(
		1110, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, []string{"drop table widgets"}))
	if err != nil {
		t.Fatal(err)
	}
	reversible := migrations.NewRunner(pool, sqlite.New(), maps, reversibleSet, nil)
	if _, err := reversible.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	reverted, err := reversible.MigrateDown(ctx, 0, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reverted != 1 {
		t.Fatalf("expected 1 reverted version, got %d", reverted)
	}

	irreversibleSet, err := migrations.NewSet(rawTableVersion(
		1111, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	irreversible := migrations.NewRunner(pool, sqlite.New(), maps, irreversibleSet, nil)
	if _, err := irreversible.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = irreversible.MigrateDown(ctx, 0, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-020" {
		t.Fatalf("expected MIG-020 without down statements or snapshots, got %v", err)
	}
}

func TestRunner_ChecksumDriftIsMIG010(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	original, err := migrations.NewSet(rawTableVersion(
		1120, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, original, nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	drifted, err := migrations.NewSet(rawTableVersion(
		1120, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY, extra TEXT) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrations.NewRunner(pool, sqlite.New(), maps, drifted, nil).Migrate(ctx, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-010" {
		t.Fatalf("expected MIG-010, got %v", err)
	}
}

func TestRunner_UnknownHistoryIsMIG011(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	set, err := migrations.NewSet(rawTableVersion(
		1130, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, set, nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	empty, err := migrations.NewSet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrations.NewRunner(pool, sqlite.New(), maps, empty, nil).Migrate(ctx, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-011" {
		t.Fatalf("expected MIG-011, got %v", err)
	}
}

func TestRunner_FailedRunRollsBackEveryVersion(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	set, err := migrations.NewSet(
		rawTableVersion(1140, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil),
		rawTableVersion(1141, "widgets", "boom", []string{"this is not sql"}, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrations.NewRunner(pool, sqlite.New(), maps, set, nil).Migrate(ctx, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-021" {
		t.Fatalf("expected MIG-021, got %v", err)
	}

	// BEGIN IMMEDIATE run: nothing survives -- not even V1140.
	cleanSet, err := migrations.NewSet(rawTableVersion(
		1140, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	status, err := migrations.NewRunner(pool, sqlite.New(), maps, cleanSet, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || status[0].State != migrations.Pending {
		t.Fatalf("expected a single Pending entry, got %+v", status)
	}
}

func TestRunner_BaselineRecordsWithoutRunning(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	set, err := migrations.NewSet(rawTableVersion(
		1150, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), maps, set, nil)
	if err := runner.Baseline(ctx, 1150); err != nil {
		t.Fatal(err)
	}

	pending, err := runner.HasPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("expected no pending versions after baseline")
	}
	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 0 {
		t.Fatalf("baseline should have recorded the version already, got %d applied", applied)
	}

	var count int64
	if err := pool.QueryRowContext(ctx,
		"select count(name) from sqlite_master where type = 'table' and name = 'widgets'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("baseline must not create the table, only record it")
	}
}

func TestRunner_StatusReportsEveryState(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	applied := rawTableVersion(1160, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil)
	set1, err := migrations.NewSet(applied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.NewRunner(pool, sqlite.New(), maps, set1, nil).Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	pendingVersion := rawTableVersion(1161, "widgets", "add_note", []string{"alter table widgets add column note TEXT"}, nil)
	set2, err := migrations.NewSet(applied, pendingVersion)
	if err != nil {
		t.Fatal(err)
	}
	status, err := migrations.NewRunner(pool, sqlite.New(), maps, set2, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[int64]migrations.MigrationState{}
	for _, e := range status {
		states[e.Version] = e.State
	}
	if states[1160] != migrations.Applied {
		t.Errorf("expected V1160 Applied, got %v", states[1160])
	}
	if states[1161] != migrations.Pending {
		t.Errorf("expected V1161 Pending, got %v", states[1161])
	}

	drifted := rawTableVersion(1160, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY, extra TEXT) STRICT"}, nil)
	set3, err := migrations.NewSet(drifted)
	if err != nil {
		t.Fatal(err)
	}
	status3, err := migrations.NewRunner(pool, sqlite.New(), maps, set3, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status3) != 1 || status3[0].State != migrations.Drifted {
		t.Fatalf("expected a single Drifted entry, got %+v", status3)
	}

	unknownSet, err := migrations.NewSet()
	if err != nil {
		t.Fatal(err)
	}
	status4, err := migrations.NewRunner(pool, sqlite.New(), maps, unknownSet, nil).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status4) != 1 || status4[0].State != migrations.Unknown || status4[0].ObjectName != "widgets" {
		t.Fatalf("expected a single Unknown entry for widgets, got %+v", status4)
	}
}

// TestRunner_SampleTreeRoundTripsThroughZero mirrors
// MigrationRunnerTests.Sample_migrations_apply_once_and_seed against the real
// sample tree, plus a full derived-rollback round trip (DerivedDownTests).
func TestRunner_SampleTreeRoundTripsThroughZero(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	set, err := samplemigrations.Set()
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err := migrations.SnapshotsFromFS(samplemigrations.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), maps, set, snapshots)

	pending, err := runner.HasPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("expected pending versions before migrating")
	}

	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 10 {
		t.Fatalf("expected 10 applied versions (V0001..V0010), got %d", applied)
	}
	if again, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil || again != 0 {
		t.Fatalf("expected idempotent re-migrate, got %d, %v", again, err)
	}

	roleNames, err := readRoleNames(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(roleNames) != 2 || roleNames[0] != "admin" || roleNames[1] != "user" {
		t.Fatalf("expected [admin user] after the V0004 rename and V0005 seed, got %v", roleNames)
	}

	status, err := runner.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 16 {
		t.Fatalf("expected one row per (version, object) = 16, got %d: %+v", len(status), status)
	}
	for _, e := range status {
		if e.State != migrations.Applied {
			t.Fatalf("expected every entry Applied, got %+v", e)
		}
	}

	// No sample step overrides Down(): every rollback below is derived.
	reverted, err := runner.MigrateDown(ctx, 0, migrations.RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if reverted != 10 {
		t.Fatalf("expected 10 reverted versions, got %d", reverted)
	}

	var objectCount int64
	if err := pool.QueryRowContext(ctx,
		"select count(*) from sqlite_master where type in ('table', 'view') "+
			"and name not like 'sqlite_%' and name <> 'schema_version'").Scan(&objectCount); err != nil {
		t.Fatal(err)
	}
	if objectCount != 0 {
		t.Fatalf("expected every sample object dropped, %d remain", objectCount)
	}

	// The same plan applies cleanly again: down really reached V0000.
	if applied, err = runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if applied != 10 {
		t.Fatalf("expected 10 re-applied versions, got %d", applied)
	}
	roleNames, err = readRoleNames(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(roleNames) != 2 || roleNames[0] != "admin" || roleNames[1] != "user" {
		t.Fatalf("expected [admin user] after re-applying, got %v", roleNames)
	}
}

func readRoleNames(ctx context.Context, pool *sql.DB) ([]string, error) {
	rows, err := pool.QueryContext(ctx, "select role_name from roles order by role_name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// ADR-0018 (owner): "Down could be deducted from the previous schema" —
// nobody writes rollback DDL. The runner derives it at migrate-down time from
// the versioned snapshots, inverting the step's typed renames
// data-preservingly and diffing the rest. Down() remains the manual override;
// missing snapshots still refuse (MIG-020). Mirrors DerivedDownTests.cs; the
// whole-sample-history and partial-rollback scenarios live in
// migration_runner_test.go's TestRunner_SampleTreeRoundTripsThroughZero.

func TestDerivedDown_UnderivableConstraintIsNoticedNotGuessed(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	// History: V1 creates derived_widgets(id, label NOT NULL); V2 drops label.
	v1Schema := &migrations.TableSchema{Name: "derived_widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
		{Name: "label", StorageType: "TEXT", Nullable: false},
	}}
	v2Schema := &migrations.TableSchema{Name: "derived_widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
	}}

	dir := t.TempDir()
	writeSnapshotFile(t, dir, "s1.schema.json", migrations.ExportSchema(v1Schema, 1, time.Now()))
	writeSnapshotFile(t, dir, "s2.schema.json", migrations.ExportSchema(v2Schema, 2, time.Now()))
	snapshots, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}

	set, err := migrations.NewSet(
		rawTableVersion(1, "derived_widgets", "create",
			[]string{"create table derived_widgets (id INTEGER PRIMARY KEY, label TEXT NOT NULL) STRICT"}, nil),
		rawTableVersion(2, "derived_widgets", "drop label",
			[]string{"alter table derived_widgets drop column label"}, nil),
	)
	if err != nil {
		t.Fatal(err)
	}

	runner := migrations.NewRunner(pool, sqlite.New(), maps, set, snapshots)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	// The structure returns nullable; the NOT NULL constraint (and data) can't derive.
	var notices []string
	reverted, err := runner.MigrateDown(ctx, 1, migrations.RunOptions{Notify: func(n string) { notices = append(notices, n) }})
	if err != nil {
		t.Fatal(err)
	}
	if reverted != 1 {
		t.Fatalf("expected 1 reverted version, got %d", reverted)
	}
	found := false
	for _, n := range notices {
		if strings.Contains(n, "label") && strings.Contains(n, "not derivable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a notice naming the underivable constraint, got %v", notices)
	}

	// Without snapshots the same plan still refuses honestly (MIG-020).
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	blind := migrations.NewRunner(pool, sqlite.New(), maps, set, nil)
	_, err = blind.MigrateDown(ctx, 1, migrations.RunOptions{})
	if core.CodeOf(err) != "MIG-020" {
		t.Fatalf("expected MIG-020 without snapshots, got %v", err)
	}
}

func TestDerivedDown_CreateRevertsToDrop(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	schema := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Nullable: false, Key: true, Generated: true},
	}}
	dir := t.TempDir()
	writeSnapshotFile(t, dir, "s1.schema.json", migrations.ExportSchema(schema, 1, time.Now()))
	snapshots, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}

	set, err := migrations.NewSet(rawTableVersion(
		1, "widgets", "create", []string{"create table widgets (id INTEGER PRIMARY KEY) STRICT"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlite.New(), maps, set, snapshots)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.MigrateDown(ctx, 0, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := pool.QueryRowContext(ctx,
		"select count(name) from sqlite_master where type = 'table' and name = 'widgets'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expected the derived rollback to drop the table created at V1")
	}
}

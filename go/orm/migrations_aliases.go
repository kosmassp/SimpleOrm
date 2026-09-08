package orm

import (
	"io/fs"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// Versioned code migrations (§7.22-§7.24) live in orm/migrations; application
// migration code (the sample tree included) reaches every type here, under
// the reference's names, so it never imports orm/migrations directly
// (CODING-STANDARD §1, mirrors aliases.go's pattern for internal/core).

type (
	// TableMigration is embedded by a table step to declare its entity type (ADR-0013).
	TableMigration[T any] = migrations.TableMigration[T]
	// ViewMigration is embedded by a view (or materialized view) step to declare its entity type.
	ViewMigration[T any] = migrations.ViewMigration[T]
	// TableActions are a table step's actions: rename -> add -> remove -> raw SQL, in that fixed order.
	TableActions = migrations.TableActions
	// ViewActions are a view step's actions, executed in declaration order.
	ViewActions = migrations.ViewActions
	// MigrationAction is one action with its optional per-action Pre/Post data hooks.
	MigrationAction = migrations.MigrationAction
	// MigrationSQL collects a step's PreDown/PostDown data-hook statements.
	MigrationSQL = migrations.MigrationSQL
	// VersionBuilder collects a version's object steps in explicit, declared order.
	VersionBuilder = migrations.VersionBuilder
	// Step is one object's migration step (TableMigration[T]/ViewMigration[T] embedders, or a data-driven SQLVersion step).
	Step = migrations.Step
	// Version is a root migration, composing its object steps via Compose.
	Version = migrations.Version
	// MigrationSet is a validated, ordered set of migration versions (see NewMigrationSet).
	MigrationSet = migrations.Set
	// SQLVersion is a data-driven version — raw SQL steps supplied as data instead of Go types.
	SQLVersion = migrations.SQLVersion
	// SQLVersionStep is one SQLVersion step's data.
	SQLVersionStep = migrations.SQLVersionStep
	// ColumnRename is a step's declared column rename, kept structurally for a future derived rollback.
	ColumnRename = migrations.ColumnRename
	// SnapshotSet is every committed schema snapshot of a migrations tree, indexed by (object, version).
	SnapshotSet = migrations.SnapshotSet
)

// NewMigrationSet builds a validated Set from explicit root versions, sorted
// by version number (MIG-001/002/003 checked eagerly; see migrations.NewSet).
func NewMigrationSet(versions ...Version) (*MigrationSet, error) {
	return migrations.NewSet(versions...)
}

// NewSQLVersion builds a data-driven version composing one raw step per declared SQLVersionStep.
func NewSQLVersion(version int64, steps ...SQLVersionStep) *SQLVersion {
	return migrations.NewSQLVersion(version, steps...)
}

// SnapshotsFromFS reads every *.schema.json under files — the Go analog of
// the reference's embedded resources: an application passes its own
// `//go:embed Table/*/*.schema.json View/*/*.schema.json` FS.
func SnapshotsFromFS(files fs.FS) (*SnapshotSet, error) {
	return migrations.SnapshotsFromFS(files)
}

// SnapshotsFromDirectory reads every *.schema.json recursively under dir; a missing directory is an empty set.
func SnapshotsFromDirectory(dir string) (*SnapshotSet, error) {
	return migrations.SnapshotsFromDirectory(dir)
}

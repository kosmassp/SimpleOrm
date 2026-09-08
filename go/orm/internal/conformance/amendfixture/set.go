// Package amendfixture is the fixed fixture conformance/amend-cases/ runs
// against (CLAUDE.md §9, ADR-0017 add.3): a two-version migration set for the
// AmendWidget table (V0001 creates it, V0002 adds a note column), used by
// orm/internal/conformance's amend-cases runner to test `diff --amend`
// against a migrations tree given as data. The entity type lives in the
// sibling models package and the step type in Table/AmendWidget, split the
// same way orm/sample is split from orm/sample/migrations.
package amendfixture

import "github.com/kosmassp/SimpleOrm/go/orm"

// Set is the fixture's two-version migration set (V0001 creates the table, V0002 adds note).
func Set() (*orm.MigrationSet, error) {
	return orm.NewMigrationSet(V0001{}, V0002{})
}

// None is the "fixture": "none" case: a namespace with no versions at all.
func None() (*orm.MigrationSet, error) {
	return orm.NewMigrationSet()
}

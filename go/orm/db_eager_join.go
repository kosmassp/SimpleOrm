package orm

import (
	"context"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// listWithJoins is join-mode eager loading (spec/loading.md "Join", ADR-0022
// add.1): one SELECT with a LEFT JOIN per included navigation (two for
// many-to-many — the unprojected link, then the projected target), built as
// core.SelectJoin entries on the root AST (spec/query-ast.md "Level 2
// extensions": root alias t, projected joins j0…, link joins l<n>, columns
// re-aliased <alias>_<column>), rendered by the dialect, and read back by
// partitioning each row into segments — the root, then one per projected
// join — through the one mapping pipeline. Roots deduplicate and children
// share instances by key identity (§7.4); collections order by target key
// value-wise; a one-to-one matched twice is REL-002.
//
// Refusals, before any SQL: a collection include with limit/offset is
// REL-005; more than one collection include is REL-006; a keyless root or
// target is REL-003; an unknown navigation is REL-001.
//
// FOUNDATION STUB — the join agent replaces this file.
func listWithJoins[T any](ctx context.Context, q *CriteriaQuery[T], m *core.EntityMap, ast *core.SelectAst, queryName string) ([]T, error) {
	_ = ctx
	_ = q
	_ = m
	_ = ast
	return nil, core.NewError("REL-001", queryName, "join-mode eager loading is not implemented yet (foundation stub)")
}

package orm

import (
	"context"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// loadEach is the one loading engine (spec/loading.md): resolves the
// navigation (REL-001, REL-003), collects the owners' key/FK tuples
// (structural value equality, null parts excluded), issues the per-kind
// queries through the criteria pipeline ordered by target key value-wise, and
// attaches the results — overwriting the navigation with fresh state.
//
// ownerSubquery is nil for explicit/batch loading and for MultiQuery eager
// loading (key lists, chunked at core.LoadChunkSize); SubSelect eager loading
// passes the root query's AST, and the target filter becomes membership in
// (select <owner keys or FKs> from that query) — never chunked. The
// many-to-many link→target hop still key-lists.
//
// FOUNDATION STUB — the loading agent replaces this file.
func loadEach[T any](ctx context.Context, db *Db, entities []*T, navigation string, ownerSubquery *core.SelectAst) error {
	_ = ctx
	_ = db
	_ = entities
	_ = ownerSubquery
	return core.NewError("REL-001", navigation, "loading is not implemented yet (foundation stub)")
}

// eagerLoad fills the included navigations of the rows a criteria query just
// materialized (MultiQuery and SubSelect modes): one loadEach per navigation,
// over pointers into the caller's slice so the loaded state lands on the
// returned values.
func eagerLoad[T any](ctx context.Context, db *Db, m *core.EntityMap, rows []T, includes []string, ownerSubquery *core.SelectAst, queryName string) error {
	_ = m
	_ = queryName
	pointers := make([]*T, len(rows))
	for i := range rows {
		pointers[i] = &rows[i]
	}
	for _, navigation := range includes {
		if err := loadEach(ctx, db, pointers, navigation, ownerSubquery); err != nil {
			return err
		}
	}
	return nil
}

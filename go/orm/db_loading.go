package orm

import "context"

// Explicit and batch loading (spec/loading.md, ADR-0021): nothing loads
// implicitly; these calls name the entity (or entities), the navigation, and
// a context, and issue one criteria query per navigation per call (two for
// many-to-many), chunked at core.LoadChunkSize owners.
//
// Go shape (CODING-STANDARD §10): a collection navigation that was never
// loaded is a nil slice, a loaded-but-empty one is an empty non-nil slice —
// the language's way of telling "unloaded" from "empty", since a slice read
// cannot be intercepted (REL-004 is unreachable here). A singular navigation
// is a nil pointer until loaded; after loading, nil means a null foreign key
// or a dead link.

// Load fills one navigation of one entity (REL-001 for an unknown name).
func Load[T any](ctx context.Context, db *Db, entity *T, navigation string) error {
	return LoadEach(ctx, db, []*T{entity}, navigation)
}

// LoadEach fills one navigation of every entity in one batched pass: one query
// per navigation per call, never one per entity. Entities are pointers so the
// loaded state lands on the caller's instances; owners sharing a many-to-one
// target share the same instance.
func LoadEach[T any](ctx context.Context, db *Db, entities []*T, navigation string) error {
	return loadEach(ctx, db, entities, navigation, nil)
}

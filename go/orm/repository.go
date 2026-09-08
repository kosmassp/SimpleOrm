package orm

import "context"

// Repository is the per-entity accessor (ADR-0016): generic CRUD, key reads,
// and criteria over one injected session — the base every app-side data layer
// was rewriting by hand. Instance-based and session-first (§7.17): no
// statics, no ambient session. Entity-specific methods come from embedding
// this in an application type (CODING-STANDARD §10); Level 2 loading methods
// are out of scope for this port.
type Repository[T any] struct {
	db *Db
}

// NewRepository wraps db with the generic surface for T.
func NewRepository[T any](db *Db) *Repository[T] {
	return &Repository[T]{db: db}
}

// Db is the session this repository operates on.
func (r *Repository[T]) Db() *Db { return r.db }

func (r *Repository[T]) Insert(ctx context.Context, entity *T) error {
	return Insert(ctx, r.db, entity)
}

func (r *Repository[T]) Update(ctx context.Context, entity *T) error {
	return Update(ctx, r.db, entity)
}

// Delete deletes by key; DeleteEntity gives the version-checked form (§7.16).
func (r *Repository[T]) Delete(ctx context.Context, key any) error {
	return Delete[T](ctx, r.db, key)
}

func (r *Repository[T]) DeleteEntity(ctx context.Context, entity *T) error {
	return DeleteEntity(ctx, r.db, entity)
}

func (r *Repository[T]) Get(ctx context.Context, key any) (T, error) {
	return Get[T](ctx, r.db, key)
}

func (r *Repository[T]) GetOrDefault(ctx context.Context, key any) (*T, error) {
	return GetOrDefault[T](ctx, r.db, key)
}

func (r *Repository[T]) GetAll(ctx context.Context) ([]T, error) {
	return QueryAll[T](ctx, r.db)
}

// Find is a criteria shortcut; compose with And/Or for more.
func (r *Repository[T]) Find(ctx context.Context, criteria Criteria) ([]T, error) {
	return r.Query().Where(criteria).List(ctx)
}

// Query is the full criteria chain for ordering and paging.
func (r *Repository[T]) Query() *CriteriaQuery[T] { return From[T](r.db) }

package orm

import (
	"context"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// CriteriaQuery is the session-first criteria chain (ADR-0012):
// orm.From[User](db).Where(...).OrderBy(...).Limit(n).List(ctx). Where
// arguments (and repeated calls) are implicitly ANDed. The rendered SELECT
// lists explicit columns (never *), resolves property names through the
// metadata (QRY-006 when unknown), and binds every value as a parameter.
// Include/Fetch are eager loading (spec/loading.md, ADR-0022): the named
// navigations load with the query, in the chosen fetch mode.
type CriteriaQuery[T any] struct {
	db        *Db
	where     []Criteria
	orderings []Ordering
	limit     *int64
	offset    *int64
	includes  []string
	fetch     FetchMode
}

// Include names navigations to load with the query (property names, exactly;
// an unknown one is REL-001 even when the query matches no rows). Includes are
// single-level; deeper graphs load explicitly from the loaded entities.
func (q *CriteriaQuery[T]) Include(navigations ...string) *CriteriaQuery[T] {
	q.includes = append(q.includes, navigations...)
	return q
}

// Fetch chooses the eager-loading mode (FetchMultiQuery by default).
func (q *CriteriaQuery[T]) Fetch(mode FetchMode) *CriteriaQuery[T] {
	q.fetch = mode
	return q
}

// Where appends criteria; multiple arguments and repeated calls are implicitly ANDed.
func (q *CriteriaQuery[T]) Where(criteria ...Criteria) *CriteriaQuery[T] {
	q.where = append(q.where, criteria...)
	return q
}

// OrderBy adds one ORDER BY term. order stands in for C#'s optional parameter
// (SortOrder.Asc by default) — passing more than one is a programmer error and
// only the first is used.
func (q *CriteriaQuery[T]) OrderBy(property string, order ...SortOrder) *CriteriaQuery[T] {
	direction := Asc
	if len(order) > 0 {
		direction = order[0]
	}
	q.orderings = append(q.orderings, Ordering{Property: property, Order: direction})
	return q
}

// Limit sets the row limit; a negative value is QRY-008 at render time.
func (q *CriteriaQuery[T]) Limit(limit int64) *CriteriaQuery[T] {
	q.limit = &limit
	return q
}

// Offset sets the row offset; a negative value is QRY-008 at render time.
func (q *CriteriaQuery[T]) Offset(offset int64) *CriteriaQuery[T] {
	q.offset = &offset
	return q
}

// queryName is "<Entity> criteria" — the name every error and the AST's WHERE naming uses.
func (q *CriteriaQuery[T]) queryName() string { return reflect.TypeFor[T]().Name() + " criteria" }

// List runs the query and materializes every row.
func (q *CriteriaQuery[T]) List(ctx context.Context) ([]T, error) {
	entityType := reflect.TypeFor[T]()
	m, err := q.db.maps.Load(entityType)
	if err != nil {
		return nil, err
	}
	// The QRY-005 gate belongs at the session in the reference (Db.Query<T>());
	// Go's chain cannot return an error from From, so it fires here instead,
	// still in the session and before any rendering (CODING-STANDARD §10
	// clarification, ADR-0027).
	if err := requireNamedRelation(m, entityType,
		"criteria queries need a named relation (statements execute via the statement API)"); err != nil {
		return nil, err
	}

	queryName := q.queryName()
	ast := &core.SelectAst{Map: m, Where: q.where, Orderings: q.orderings, Limit: q.limit, Offset: q.offset}

	if len(q.includes) > 0 && q.fetch == FetchSubSelect && (q.limit != nil || q.offset != nil) {
		// A paged SubSelect root re-evaluates inside every subquery, so the page
		// must be deterministic: every key property not already ordered on
		// breaks ties, ascending, in key order — on the root and, through the
		// shared AST, on each subquery (spec/loading.md "Key-tiebroken ordering").
		ast.Orderings = keyTiebrokenOrderings(m, q.orderings)
	}

	if len(q.includes) > 0 && q.fetch == FetchJoin {
		// Join mode is one SELECT with LEFT JOINs: a different statement, not a
		// post-pass (db_eager_join.go).
		return listWithJoins[T](ctx, q, m, ast, queryName)
	}

	rows, err := q.execute(ctx, m, ast, queryName)
	if err != nil {
		return nil, err
	}
	if len(q.includes) > 0 {
		// MultiQuery and SubSelect load after the root query (db_eager.go);
		// SubSelect filters each navigation by membership in the root query.
		var ownerSubquery *core.SelectAst
		if q.fetch == FetchSubSelect {
			ownerSubquery = ast
		}
		if err := eagerLoad[T](ctx, q.db, m, rows, q.includes, ownerSubquery, queryName); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// keyTiebrokenOrderings appends every key property the orderings do not
// already name (case-insensitively), ascending, in key order.
func keyTiebrokenOrderings(m *core.EntityMap, orderings []Ordering) []Ordering {
	result := append([]Ordering(nil), orderings...)
	for _, key := range m.KeyProperties {
		present := false
		for _, o := range result {
			if strings.EqualFold(o.Property, key.PropertyName) {
				present = true
				break
			}
		}
		if !present {
			result = append(result, Ordering{Property: key.PropertyName, Order: Asc})
		}
	}
	return result
}

// execute renders the AST through the dialect, binds in render order, and
// materializes the root rows.
func (q *CriteriaQuery[T]) execute(ctx context.Context, m *core.EntityMap, ast *core.SelectAst, queryName string) ([]T, error) {
	rows, err := renderAndRunSelect(ctx, q.db, ast, queryName)
	if err != nil {
		return nil, err
	}
	return materializeRows[T](ctx, q.db, rows, queryName)
}

// Single expects exactly one row: zero is QRY-001, more than one QRY-002.
func (q *CriteriaQuery[T]) Single(ctx context.Context) (T, error) {
	rows, err := q.List(ctx)
	var zero T
	if err != nil {
		return zero, err
	}
	switch len(rows) {
	case 1:
		return rows[0], nil
	case 0:
		return zero, core.NewError("QRY-001", q.queryName(), "expected exactly one row, found none")
	default:
		return zero, core.Errorf("QRY-002", q.queryName(), "expected exactly one row, found %d", len(rows))
	}
}

// SingleOrDefault expects at most one row: zero returns nil, more than one is QRY-002.
func (q *CriteriaQuery[T]) SingleOrDefault(ctx context.Context) (*T, error) {
	rows, err := q.List(ctx)
	if err != nil {
		return nil, err
	}
	switch len(rows) {
	case 0:
		return nil, nil
	case 1:
		return &rows[0], nil
	default:
		return nil, core.Errorf("QRY-002", q.queryName(), "expected at most one row, found %d", len(rows))
	}
}

package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// CriteriaQuery is the session-first criteria chain (ADR-0012):
// orm.From[User](db).Where(...).OrderBy(...).Limit(n).List(ctx). Where
// arguments (and repeated calls) are implicitly ANDed. The rendered SELECT
// lists explicit columns (never *), resolves property names through the
// metadata (QRY-006 when unknown), and binds every value as a parameter.
// Level 2's Include/Fetch are out of scope for this port (CLAUDE.md §7b M3/M4).
type CriteriaQuery[T any] struct {
	db        *Db
	where     []Criteria
	orderings []Ordering
	limit     *int64
	offset    *int64
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

	var bound []any
	bind := func(value any, property *core.PropertyMap) (string, error) {
		name := fmt.Sprintf("c%d", len(bound))
		var columnType core.ColumnType
		if property != nil {
			columnType = property.ColumnType
		}
		converted, err := q.db.converter.ToDatabase(value, columnType, fmt.Sprintf("%s @%s", queryName, name))
		if err != nil {
			return "", err
		}
		bound = append(bound, sql.Named(name, converted))
		return "@" + name, nil
	}

	sqlText, err := q.db.options.Dialect.SelectSQL(ast, bind)
	if err != nil {
		return nil, err
	}

	rows, err := q.db.exec().QueryContext(ctx, sqlText, bound...)
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

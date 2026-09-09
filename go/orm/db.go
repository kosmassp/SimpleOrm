package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/params"
)

// Db is the session (§7.17, mirrors dotnet/src/SimpleOrm/Db.cs): it owns one
// pinned connection and, at most, one active transaction. Every command runs
// on that connection and inside the current transaction, if any — no ambient
// state, no hidden queries. The session owns its own metadata cache, so an
// entity's map loads once per session.
type Db struct {
	pool       *sql.DB
	connection *sql.Conn
	tx         *sql.Tx
	options    Options
	maps       *metadata.Loader
	converter  *mapping.TypeConverter
	mapper     *mapping.ResultMapper
}

// executor is the surface *sql.Conn and *sql.Tx share — every statement runs
// on it, so it automatically runs inside the current transaction when one is
// active (§7.17).
type executor interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Open opens the session against connectionString (§7.17): the dialect
// creates the connection, one *sql.Conn is pinned from its pool, and a failed
// open leaves nothing behind. Options.Dialect is required.
func Open(ctx context.Context, connectionString string, options Options) (*Db, error) {
	if options.Dialect == nil {
		return nil, fmt.Errorf("orm.Open: Options.Dialect is required")
	}

	pool, err := options.Dialect.CreateConnection(connectionString)
	if err != nil {
		return nil, err
	}

	connection, err := pool.Conn(ctx)
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	if err := connection.PingContext(ctx); err != nil {
		_ = connection.Close()
		_ = pool.Close()
		return nil, err
	}

	loader := metadata.NewLoader(options.Mapping)
	converter := mapping.NewTypeConverter(options.TypeHandlers, options.Dialect.BindsTemporalsNatively())
	return &Db{
		pool:       pool,
		connection: connection,
		options:    options,
		maps:       loader,
		converter:  converter,
		mapper:     mapping.NewResultMapper(loader, converter),
	}, nil
}

// Close rolls back an active transaction, then releases the connection and
// the pool. Disposal is the only teardown; there is no close-and-reopen.
func (db *Db) Close() error {
	if db.tx != nil {
		_ = db.tx.Rollback()
		db.tx = nil
	}
	connErr := db.connection.Close()
	poolErr := db.pool.Close()
	if connErr != nil {
		return connErr
	}
	return poolErr
}

// Maps is the session's metadata loader (a shared cache for this session).
func (db *Db) Maps() *metadata.Loader { return db.maps }

// Dialect is the session's dialect.
func (db *Db) Dialect() Dialect { return db.options.Dialect }

// Options is the session's configuration.
func (db *Db) Options() Options { return db.options }

// conn is the pinned connection (§7.17), for SchemaGuard's describe-statement path.
func (db *Db) conn() *sql.Conn { return db.connection }

// exec is the current statement target: the active transaction, or the pinned connection.
func (db *Db) exec() executor {
	if db.tx != nil {
		return db.tx
	}
	return db.connection
}

// Tx is a transaction scope on one session (§7.17). Commit explicitly;
// `defer tx.Rollback(ctx)` is the idiom — Rollback after Commit (or a second
// Rollback) is a no-op.
type Tx struct {
	db   *Db
	done bool
}

// Begin starts the session's transaction scope; a second concurrent scope is TX-001.
func (db *Db) Begin(ctx context.Context) (*Tx, error) {
	if db.tx != nil {
		return nil, core.NewError("TX-001", "session", "a transaction is already active on this session")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tx, err := db.connection.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	db.tx = tx
	return &Tx{db: db}, nil
}

// Commit commits the transaction; a second call (or a call after Rollback) is a no-op.
func (t *Tx) Commit(context.Context) error {
	if t.done {
		return nil
	}
	t.done = true
	err := t.db.tx.Commit()
	t.db.tx = nil
	return err
}

// Rollback rolls back the transaction; a second call (or a call after Commit) is a no-op.
func (t *Tx) Rollback(context.Context) error {
	if t.done {
		return nil
	}
	t.done = true
	err := t.db.tx.Rollback()
	t.db.tx = nil
	return err
}

// --- shared plumbing (used by db_query.go, db_crud.go, criteria_query.go) ---

// bindAndQuery binds sqlText's placeholders from args and runs it on the
// session's current executor.
func bindAndQuery(ctx context.Context, db *Db, sqlText string, args any, queryName string) (*sql.Rows, error) {
	bound, err := params.Bind(sqlText, args, db.converter, db.options.Dialect, queryName)
	if err != nil {
		return nil, err
	}
	return db.exec().QueryContext(ctx, bound.SQL, bound.Args...)
}

// renderAndRunSelect renders ast through the dialect (spec/query-ast.md) and
// runs it on the session's current executor. One bind closure, shared by the
// criteria chain (criteria_query.go's execute), join-mode eager loading
// (db_eager_join.go's listWithJoins), and every per-kind explicit/batch/
// MultiQuery/SubSelect loader query (db_eager.go's selectRows) — the AST
// analog of bindAndQuery above, for callers that render through
// Dialect.SelectSQL instead of scanning `@name` placeholders out of literal
// SQL.
func renderAndRunSelect(ctx context.Context, db *Db, ast *core.SelectAst, queryName string) (*sql.Rows, error) {
	var bound []any
	bind := func(value any, property *core.PropertyMap) (string, error) {
		name := fmt.Sprintf("c%d", len(bound))
		var columnType core.ColumnType
		if property != nil {
			columnType = property.ColumnType
		}
		converted, err := db.converter.ToDatabase(value, columnType, fmt.Sprintf("%s @%s", queryName, name))
		if err != nil {
			return "", err
		}
		bound = append(bound, sql.Named(name, converted))
		return "@" + name, nil
	}

	sqlText, err := db.options.Dialect.SelectSQL(ast, bind)
	if err != nil {
		return nil, err
	}
	return db.exec().QueryContext(ctx, sqlText, bound...)
}

// materializeRows is the one list-materialization loop (§7.11/ADR-0015's Go
// analog, minus the C#-only compiled fast paths): the plan is built from the
// column schema before the first row, so strictness (MAP-001/002) fires even
// for an empty result; cancellation is checked every 64 rows.
func materializeRows[T any](ctx context.Context, db *Db, rows *sql.Rows, queryName string) ([]T, error) {
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	plan, err := db.mapper.Plan(reflect.TypeFor[T](), columns, queryName)
	if err != nil {
		return nil, err
	}

	var results []T
	count := 0
	for rows.Next() {
		value, err := plan.Read(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, value.(T))
		count++
		if count%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// streamRows is materializeRows's lazy counterpart (§10 Stream): the plan is
// still built before the first row is yielded; an error is yielded once and
// the sequence ends; rows close whenever the consumer stops (the caller's defer).
func streamRows[T any](ctx context.Context, db *Db, rows *sql.Rows, queryName string, yield func(T, error) bool) {
	defer rows.Close()
	var zero T

	columns, err := rows.Columns()
	if err != nil {
		yield(zero, err)
		return
	}
	plan, err := db.mapper.Plan(reflect.TypeFor[T](), columns, queryName)
	if err != nil {
		yield(zero, err)
		return
	}

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			yield(zero, err)
			return
		}
		value, err := plan.Read(rows)
		if err != nil {
			yield(zero, err)
			return
		}
		if !yield(value.(T), nil) {
			return
		}
	}
	if err := rows.Err(); err != nil {
		yield(zero, err)
	}
}

// requireNamedRelation is the QRY-005 gate: statements/procedures have no
// named relation, so select-all, key reads, and criteria queries refuse
// (ADR-0011 add., spec/session.md).
func requireNamedRelation(m *core.EntityMap, entityType reflect.Type, reason string) error {
	if m.Kind == core.RelationStatement || m.Kind == core.RelationProcedure {
		return core.Errorf("QRY-005", entityType.Name(), "is %s-backed; %s", m.Kind, reason)
	}
	return nil
}

func stripPointer(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

func formatKey(key any) string {
	if values, ok := key.([]any); ok {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprint(v)
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(key)
}

func formatKeyOf(m *core.EntityMap, entity any) string {
	values, err := m.KeyValues(entity)
	if err != nil {
		return err.Error()
	}
	return formatKey(values)
}

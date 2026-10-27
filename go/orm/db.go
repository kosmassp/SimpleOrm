package orm

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

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
	// tracker is the identity map and snapshots (Level 3 M1); nil in the default untracked session.
	tracker *tracker
	// uow is the unit of work's schedule (Level 3 M2), created on first Persist/Remove.
	uow *unitOfWork
	// batcher is the dialect's one-round-trip realization of a planned message (Level 4 item 3, ADR-0044), set only
	// when the option is Auto and the dialect both reports SupportsStatementBatching and implements the capability;
	// nil means the plan's messages run one statement at a time.
	batcher core.StatementBatcher
	// maxStatements is the cap on statements per message, resolved at Open.
	maxStatements int
	// log is the statement log (Level 4 item 4, ADR-0045): nil when Options.Log is unset.
	log *core.StatementLog
	// timeout is Options.CommandTimeout (Level 4, ADR-0047): zero when unset. Every statement the session sends runs
	// under core.CommandContext(ctx, timeout) — through exec(), the statement executor, or around a dialect's
	// one-round-trip message (db_statements.go).
	timeout time.Duration
	// renders is the generated CRUD text per entity map (ADR-0048 item 1, generated_sql.go).
	renders renderCache
	// renderObserver is the step-script runners' test hook (core.RenderObserver): the dialect, when it implements it.
	renderObserver core.RenderObserver
}

// Open opens the session against connectionString (§7.17): the dialect
// creates the connection, one *sql.Conn is pinned from its pool, and a failed
// open leaves nothing behind. Options.Dialect is required. Under Options.OpenRetries a
// failure the dialect calls transient is attempted again (db_open.go, ADR-0047).
func Open(ctx context.Context, connectionString string, options Options) (*Db, error) {
	if options.Dialect == nil {
		return nil, fmt.Errorf("orm.Open: Options.Dialect is required")
	}
	if options.MaxStatementsPerMessage < 0 {
		return nil, fmt.Errorf("orm.Open: Options.MaxStatementsPerMessage must be at least 1 (0 means the default, %d), got %d",
			DefaultMaxStatementsPerMessage, options.MaxStatementsPerMessage)
	}
	if options.Batching != BatchingOff && options.Batching != BatchingAuto {
		return nil, fmt.Errorf("orm.Open: Options.Batching is not a known mode: %d", options.Batching)
	}
	if options.OpenRetries < 0 {
		return nil, fmt.Errorf("orm.Open: Options.OpenRetries must be at least 0, got %d", options.OpenRetries)
	}
	if options.OpenRetryDelay < 0 {
		return nil, fmt.Errorf("orm.Open: Options.OpenRetryDelay must not be negative (0 means the default, %s; NoOpenRetryDelay means no wait), got %s",
			DefaultOpenRetryDelay, options.OpenRetryDelay)
	}
	if options.CommandTimeout < 0 {
		return nil, fmt.Errorf("orm.Open: Options.CommandTimeout must not be negative (0 means the driver's default), got %s", options.CommandTimeout)
	}

	pool, connection, err := openConnection(ctx, connectionString, options)
	if err != nil {
		return nil, err
	}

	loader := metadata.NewLoader(options.Mapping)
	converter := mapping.NewTypeConverter(options.TypeHandlers, options.Dialect.BindsTemporalsNatively())
	var identityMap *tracker
	if options.Tracking == TrackingTracked {
		identityMap = newTracker(converter)
	}
	mapper := mapping.NewResultMapper(loader, converter)
	if judge, ok := options.Dialect.(core.TemporalMarkers); ok {
		mapper.UseTemporalMarkers(judge) // the VAL-020 read rule by column type (spec/dialects.md "Temporals")
	}
	db := &Db{
		pool:          pool,
		connection:    connection,
		options:       options,
		maps:          loader,
		converter:     converter,
		mapper:        mapper,
		tracker:       identityMap,
		maxStatements: options.MaxStatementsPerMessage,
		log:           core.NewStatementLog(options.Log, options.LogParameterValues),
		timeout:       options.CommandTimeout,
	}
	db.renderObserver, _ = options.Dialect.(core.RenderObserver)
	if db.maxStatements == 0 {
		db.maxStatements = DefaultMaxStatementsPerMessage
	}
	// The one-round-trip realization needs the dialect to say so and to be able to (a decorator that embeds the Dialect
	// interface promotes the flag but not the capability, so it runs the plan sequentially).
	if batcher, ok := options.Dialect.(core.StatementBatcher); ok && options.Batching == BatchingAuto && options.Dialect.SupportsStatementBatching() {
		db.batcher = batcher
	}
	return db, nil
}

// BatchingInEffect reports whether a flush or an explicit batch sends each planned message as one round trip with a
// result per statement (spec/session.md "Batching"): the option is Auto and this implementation's driver for the
// dialect can (SupportsStatementBatching). False under Auto means the plan is formed all the same and each message's
// statements run one at a time, reusing one prepared command per distinct statement text within the call (SQLite);
// nothing refuses — one configuration serves every database, and this says which realization is in force.
func (db *Db) BatchingInEffect() bool { return db.batcher != nil }

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

// exec is the current statement target — the active transaction, or the pinned connection — behind the session's one
// statement funnel, which applies the command timeout (db_statements.go). Every statement runs inside the current
// transaction when one is active (§7.17).
func (db *Db) exec() statementExecutor {
	return statementExecutor{inner: db.rawExecutor(), timeout: db.timeout}
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
//
// A rollback reverts the database, not the identity map, the snapshots, or in-memory version
// bumps; Refresh is the way back (spec/session.md Clarifications, ADR-0035: "transactions do not
// touch the map").
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
func bindAndQuery(ctx context.Context, db *Db, sqlText string, args any, queryName, logName string) (*resultRows, error) {
	bound, err := params.Bind(sqlText, args, db.converter, db.options.Dialect, queryName)
	if err != nil {
		return nil, err
	}
	db.logStatement(logName, bound.SQL, bound.Args)
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
func renderAndRunSelect(ctx context.Context, db *Db, ast *core.SelectAst, queryName string) (*resultRows, error) {
	var bound []any
	bind := func(value any, property *core.PropertyMap) (string, error) {
		name := fmt.Sprintf("c%d", len(bound))
		var columnType core.ColumnType
		if property != nil {
			columnType = property.ColumnType
		}
		converted, err := db.converter.ToParameter(value, columnType, fmt.Sprintf("%s @%s", queryName, name))
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
	db.logStatement(selectLogName(ast), sqlText, bound)
	return db.exec().QueryContext(ctx, sqlText, bound...)
}

// selectLogName is the statement-log name of a rendered SELECT (spec/session.md "Statement log"): the criteria query of the
// entity it reads -- the queries explicit and batch loading issue included, a many-to-many load being the link entity's
// criteria then the target's -- or `include` when it carries the joins of a join-mode eager query.
func selectLogName(ast *core.SelectAst) string {
	if len(ast.Joins) > 0 {
		return ast.Map.Type.Name() + ".include"
	}
	return ast.Map.Type.Name() + ".criteria"
}

// materializeRows is the one list-materialization loop (§7.11/ADR-0015's Go
// analog, minus the C#-only compiled fast paths): the plan is built from the
// column schema before the first row, so strictness (MAP-001/002) fires even
// for an empty result; cancellation is checked every 64 rows.
func materializeRows[T any](ctx context.Context, db *Db, rows *resultRows, queryName string) ([]T, error) {
	return readRows[T](ctx, db, rows, db.resultMapFor(reflect.TypeFor[T]()), queryName)
}

// readRows is the loop behind materializeRows. tracked is the entity map the
// rows resolve through the identity map (Level 3 M1: a known key yields the
// tracked instance, an unknown one is registered), or nil for results that
// are never tracked — plain result structs, keyless entities, untracked
// sessions. Callers that know their result is an entity pass db.tracking(m).
//
// A read that fails midway (a mapping error, cancellation, REL-002 in join mode) leaves the
// rows it had already resolved registered: the map is a cache of what the session
// materialized, not a transaction (spec/session.md Clarifications, ADR-0035).
func readRows[T any](ctx context.Context, db *Db, rows *resultRows, tracked *core.EntityMap, queryName string) ([]T, error) {
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
		value, err := plan.Read(rows.Rows)
		if err != nil {
			return nil, err
		}
		resolved, err := db.resolveAny(tracked, value)
		if err != nil {
			return nil, err
		}
		results = append(results, resolved.(T))
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
func streamRows[T any](ctx context.Context, db *Db, rows *resultRows, queryName string, yield func(T, error) bool) {
	defer rows.Close()
	var zero T
	tracked := db.resultMapFor(reflect.TypeFor[T]())

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
		value, err := plan.Read(rows.Rows)
		if err != nil {
			yield(zero, err)
			return
		}
		resolved, err := db.resolveAny(tracked, value)
		if err != nil {
			yield(zero, err)
			return
		}
		if !yield(resolved.(T), nil) {
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

package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The write machinery shared by the immediate operations, the unit of work's flush, and the explicit batch (Level 4
// item 3, ADR-0044, spec/session.md "Batching"). Every generated write is split into three steps:
//
//   - prepare — the checks, the gates, the hooks, the SQL and its bound values, producing a preparedWrite (or nothing,
//     when the operation issues no statement: a clean tracked update);
//   - execute — the statement runs, alone or as one statement of a planned message, and its result is read;
//   - complete — the result is judged (CRUD-010 / CRUD-001) and applied to the instance and the session: the generated
//     key, the bumped version, the identity map and the snapshots.
//
// One path, so batching changes how many round trips carry the statements and never what a statement is; the immediate
// calls run the three steps back to back, and Off leaves them exactly as they were.

// writeResult is what one statement returned: the generated key (ResultKey) or the count the CRUD rules judge
// (ResultReportedCount, ResultDriverCount).
type writeResult struct {
	key      any
	affected int64
}

// preparedWrite is one statement ready to run, with how to read its result and how to finish the operation that
// issued it.
type preparedWrite struct {
	sql  string
	args []any
	kind core.StatementResult
	// name is the statement-log name (`User.update`); empty when no log is set.
	name string
	// instance is the *E the statement writes, or nil (a registry command, a delete by key): the plan's rule 1a keeps
	// a second statement on the same instance out of the message that holds the first.
	instance any
	// generatesKey is true for the insert of an instance whose key the database generates: rule 1b starts a message
	// after it for whatever reads the key.
	generatesKey bool
	// command is true for a registry command: rule 2 keeps a statement that reads a result behind it out of its message.
	command bool
	// noRow is the error text when a ResultKey statement returns no row.
	noRow string
	// finish judges the result and applies the statement's effects on the instance and the session. The journal (nil
	// for an immediate call) takes the undo of every effect that is not captured by recordWrite.
	finish func(result writeResult, journal *undoJournal) error
	// undoPrepare puts back what the prepare step changed on the instance (the hooks' fills and stamps) when the
	// statement then fails — an immediate call's own restore; a flush or batch rolls its journal back instead.
	undoPrepare func()
	// affected is the operation's count in an explicit batch's result: 1 for an insert, the judged count of a
	// generated update or delete, the driver's count of a registry command.
	affected int64
}

// readsResult is rule 2's "returns a result the session reads": a generated key, or a count reported by select.
func (w *preparedWrite) readsResult() bool {
	return w.kind == core.ResultKey || w.kind == core.ResultReportedCount
}

// countKind is how the session reads a generated UPDATE or DELETE's count on this dialect (ADR-0040 add.3,
// spec/dialects.md Clarification 23): the dialect's reported scalar, or the driver's count.
func (db *Db) countKind() core.StatementResult {
	if db.options.Dialect.ReportsAffectedRowsBySelect() {
		return core.ResultReportedCount
	}
	return core.ResultDriverCount
}

// --- running one statement ----------------------------------------------------------------

// statementRunner runs one statement on its own. The direct runner is the Level 0–3 path (the session's executor);
// the prepared runner reuses one prepared command per distinct statement text within a call.
type statementRunner interface {
	exec(ctx context.Context, sqlText string, args []any) (sql.Result, error)
	query(ctx context.Context, sqlText string, args []any) (*sql.Rows, error)
}

type directRunner struct{ executor executor }

func (r directRunner) exec(ctx context.Context, sqlText string, args []any) (sql.Result, error) {
	return r.executor.ExecContext(ctx, sqlText, args...)
}

func (r directRunner) query(ctx context.Context, sqlText string, args []any) (*sql.Rows, error) {
	return r.executor.QueryContext(ctx, sqlText, args...)
}

// preparedRunner prepares each distinct statement text once and reuses it for the rest of the call (spec/session.md
// "Batching": on SQLite, where no network round trip exists, the reuse is the gain). close releases the commands.
type preparedRunner struct {
	executor executor
	prepared map[string]*sql.Stmt
}

func newPreparedRunner(executor executor) *preparedRunner {
	return &preparedRunner{executor: executor, prepared: map[string]*sql.Stmt{}}
}

func (r *preparedRunner) statement(ctx context.Context, sqlText string) (*sql.Stmt, error) {
	if stmt, ok := r.prepared[sqlText]; ok {
		return stmt, nil
	}
	stmt, err := r.executor.PrepareContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	r.prepared[sqlText] = stmt
	return stmt, nil
}

func (r *preparedRunner) exec(ctx context.Context, sqlText string, args []any) (sql.Result, error) {
	stmt, err := r.statement(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	return stmt.ExecContext(ctx, args...)
}

func (r *preparedRunner) query(ctx context.Context, sqlText string, args []any) (*sql.Rows, error) {
	stmt, err := r.statement(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	return stmt.QueryContext(ctx, args...)
}

func (r *preparedRunner) close() {
	for _, stmt := range r.prepared {
		_ = stmt.Close() // a statement of a finished call; the transaction or the connection owns what is left
	}
	r.prepared = nil
}

// errNoRow is a scalar read that found no row.
var errNoRow = errors.New("the statement returned no row")

// runWrite runs one prepared statement and reads its result.
func runWrite(ctx context.Context, runner statementRunner, write *preparedWrite) (writeResult, error) {
	switch write.kind {
	case core.ResultNone:
		_, err := runner.exec(ctx, write.sql, write.args)
		return writeResult{}, err

	case core.ResultKey:
		value, err := readScalar(ctx, runner, write)
		if errors.Is(err, errNoRow) {
			return writeResult{}, errors.New(write.noRow)
		}
		return writeResult{key: value}, err

	case core.ResultReportedCount:
		value, err := readScalar(ctx, runner, write)
		if errors.Is(err, errNoRow) {
			return writeResult{}, sql.ErrNoRows
		}
		if err != nil {
			return writeResult{}, err
		}
		count, err := scalarCount(value)
		return writeResult{affected: count}, err

	default: // core.ResultDriverCount
		result, err := runner.exec(ctx, write.sql, write.args)
		if err != nil {
			return writeResult{}, err
		}
		affected, err := result.RowsAffected()
		return writeResult{affected: affected}, err
	}
}

// readScalar is the first column of the first row of a statement's result.
func readScalar(ctx context.Context, runner statementRunner, write *preparedWrite) (any, error) {
	rows, err := runner.query(ctx, write.sql, write.args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, errNoRow
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		return nil, err
	}
	return value, rows.Err()
}

// scalarCount reads a reported count as an integer whatever the driver's shape (spec/dialects.md Clarification 24:
// go-mssqldb returns @@rowcount as a 32-bit integer). A non-numeric value is a dialect bug — never zero, so never
// CRUD-010 or CRUD-001.
func scalarCount(value any) (int64, error) {
	switch v := value.(type) {
	case []byte:
		return parseCount(string(v))
	case string:
		return parseCount(v)
	case nil:
		return 0, fmt.Errorf("the statement's reported affected-row count is NULL")
	}
	count, err := toInt64(value)
	if err != nil {
		return 0, fmt.Errorf("the statement's reported affected-row count is not an integer: %T", value)
	}
	return count, nil
}

func parseCount(text string) (int64, error) {
	count, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the statement's reported affected-row count is not an integer: %q", text)
	}
	return count, nil
}

// runImmediate is the Level 0–3 path of one generated write: prepare has run, the statement runs on the session's
// executor, the result is judged and applied; a failure puts the instance back as prepare found it. A nil write is an
// operation that issued no statement.
func (db *Db) runImmediate(ctx context.Context, write *preparedWrite) error {
	if write == nil {
		return nil
	}
	db.logStatement(write.name, write.sql, write.args)
	result, err := runWrite(ctx, directRunner{db.exec()}, write)
	if err == nil && write.finish != nil {
		err = write.finish(result, nil)
	}
	if err != nil {
		if write.undoPrepare != nil {
			write.undoPrepare()
		}
		return err
	}
	return nil
}

// --- the undo journal ----------------------------------------------------------------------

// undoJournal remembers how to put the session's in-memory state back when a call that issued several statements —
// a flush or an explicit batch send — fails: the database side is the transaction's rollback; this is the instances'
// keys, versions, stamps, and flags, and the identity map and the snapshots (spec/unit-of-work.md Clarifications,
// ADR-0036; spec/session.md "Batching", Failure). Closures run last-in first-out. Every method is nil-safe, so the
// immediate calls pass no journal.
type undoJournal struct {
	undo []func()
}

func (j *undoJournal) rollback() {
	if j == nil {
		return
	}
	for i := len(j.undo) - 1; i >= 0; i-- {
		j.undo[i]()
	}
	j.undo = nil
}

func (j *undoJournal) add(undo func()) {
	if j != nil {
		j.undo = append(j.undo, undo)
	}
}

// recordWrite is registered before an operation on ptr (a *E) prepares, so a partial failure is undone too: it
// captures the instance's key, version, created-at, updated-at, and soft-delete values (an insert writes the key
// back, an update stamps and bumps, a soft delete sets the flag) and the identity map's view of it — its entry and
// that entry's snapshot, and whichever instance holds its key — and puts them all back.
func (j *undoJournal) recordWrite(db *Db, m *core.EntityMap, ptr any) {
	if j == nil {
		return
	}
	type value struct {
		property *core.PropertyMap
		prior    any
	}
	var values []value
	capture := func(p *core.PropertyMap) {
		if p != nil {
			values = append(values, value{p, p.Get(ptr)})
		}
	}
	for _, key := range m.KeyProperties {
		capture(key)
	}
	capture(m.VersionProperty)
	capture(m.CreatedAtProperty)
	capture(m.UpdatedAtProperty)
	capture(m.SoftDeleteProperty)

	var entry, holder *trackedEntry
	var snapshot []any
	if db.tracker != nil {
		entry = db.entryOf(ptr)
		if entry != nil {
			snapshot = append([]any(nil), entry.snapshot...)
		}
		if key, ok := keyOf(m, ptr); ok {
			holder = db.tracker.find(m.Type, key)
		}
	}

	j.undo = append(j.undo, func() {
		for _, v := range values {
			_ = v.property.Set(ptr, v.prior) // the value came from Get, so it always assigns back
		}
		if db.tracker == nil {
			return
		}
		// An entry registered since (an insert, an update of an unknown instance) is forgotten; the entry there was is
		// put back as it was, and so is the instance that held the key before a write displaced it.
		if current := db.entryOf(ptr); current != nil && current != entry {
			db.tracker.forget(current)
		}
		if entry != nil {
			copy(entry.snapshot, snapshot)
			db.tracker.restore(entry)
		}
		if holder != nil && holder != entry {
			db.tracker.restore(holder)
		}
	})
}

// trackJournaled registers entity after a write stored it (Insert, a full-row Update of an unknown instance). The
// instance that held the new key — only known now, for a generated key — comes back if the call is undone.
func (db *Db) trackJournaled(j *undoJournal, m *core.EntityMap, entity any) error {
	if db.tracking(m) == nil {
		return nil
	}
	if j != nil {
		if key, ok := keyOf(m, entity); ok {
			if holder := db.tracker.find(m.Type, key); holder != nil && holder.ptr.Interface() != entity {
				j.add(func() { db.tracker.restore(holder) })
			}
		}
	}
	return db.track(m, entity)
}

// forgetKeyJournaled drops whatever instance is tracked for (m's type, key); the call's undo tracks it again.
func (db *Db) forgetKeyJournaled(j *undoJournal, m *core.EntityMap, key core.KeyTuple) {
	if db.tracker == nil {
		return
	}
	if entry := db.tracker.find(m.Type, key); entry != nil {
		j.add(func() { db.tracker.restore(entry) })
		db.tracker.forget(entry)
	}
}

// forgetEntryJournaled forgets a tracked instance after its row was deleted; the call's undo tracks it again.
func (db *Db) forgetEntryJournaled(j *undoJournal, entry *trackedEntry) {
	if entry == nil {
		return
	}
	j.add(func() { db.tracker.restore(entry) })
	db.tracker.forget(entry)
}

// m2oTargets lists the instances ptr's non-null many-to-one navigations point at: the plan's rule 1b asks whether any
// of them is inserted, with a database-generated key, in the message in progress.
func m2oTargets(m *core.EntityMap, ptr any) []any {
	var targets []any
	for _, relationship := range m.Relationships {
		if relationship.Kind != core.RelationshipManyToOne {
			continue
		}
		found, _ := navigationTargets(ptr, relationship)
		targets = append(targets, found...)
	}
	return targets
}

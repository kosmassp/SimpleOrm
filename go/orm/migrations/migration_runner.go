package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// MigrationState is a (version, object)'s status against the recorded history (§7.23).
type MigrationState int

const (
	// Pending: the plan has this step but nothing is recorded for its version.
	Pending MigrationState = iota
	// Applied: the recorded checksum matches the rendered Up SQL.
	Applied
	// Drifted: the version is recorded but this object's row is missing or its checksum disagrees.
	Drifted
	// Unknown: a recorded row whose (version, object) the plan does not compose.
	Unknown
)

// String is the reference's state name.
func (s MigrationState) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Applied:
		return "Applied"
	case Drifted:
		return "Drifted"
	case Unknown:
		return "Unknown"
	}
	return fmt.Sprintf("MigrationState(%d)", int(s))
}

// MigrationEntry is one (version, object) row of the migration status (§7.23).
type MigrationEntry struct {
	Version     int64
	ObjectName  string
	Description string
	State       MigrationState
}

// String mirrors the C# MigrationEntry.ToString format.
func (e MigrationEntry) String() string {
	return fmt.Sprintf("V%04d %-28s %-30s %s", e.Version, e.ObjectName, e.Description, e.State)
}

// RunOptions configures one Migrate/MigrateDown run: the view-drift override
// (ADR-0017 add.1) and a sink for notices (drift reports, underivable-constraint warnings).
type RunOptions struct {
	// AllowViewDrift lets a view step's ExpectDefinition guard recreate over an
	// outside hotfix instead of refusing with MIG-012; the drift is reported through Notify.
	AllowViewDrift bool
	// Notify receives drift/notice text as the run discovers it; nil discards it.
	Notify func(string)
}

// Runner applies versioned code migrations (§7.22-§7.24, ADR-0013/0018): the
// whole run executes inside the dialect's run lock — on SQLite one BEGIN
// IMMEDIATE transaction, which also makes a failed run fully atomic. Every
// plan is validated (checksums, unknown history) before any statement
// executes. The application never calls this at startup; migrating is an
// explicit act. The runner pins one *sql.Conn per run from the pool — never a
// shared session connection (CODING-STANDARD §10) — so it composes with a
// session that owns its own connection.
type Runner struct {
	pool      *sql.DB
	dialect   core.Dialect
	maps      *metadata.Loader
	set       *Set
	snapshots *SnapshotSet
}

// NewRunner builds a runner; a nil snapshots set means empty (every derived
// rollback then refuses with MIG-020 unless a step overrides Down()).
func NewRunner(pool *sql.DB, dialect core.Dialect, maps *metadata.Loader, set *Set, snapshots *SnapshotSet) *Runner {
	if snapshots == nil {
		snapshots = newSnapshotSet()
	}
	return &Runner{pool: pool, dialect: dialect, maps: maps, set: set, snapshots: snapshots}
}

// recordedKey identifies one schema_version row.
type recordedKey struct {
	Version int64
	Object  string
}

type recordedRow struct {
	Description string
	Checksum    string
}

// recordedHistory is what's already in schema_version, read inside the run lock.
type recordedHistory struct {
	rows     map[recordedKey]recordedRow
	versions map[int64]bool
}

// queryer is the common surface of *sql.Tx and *sql.Conn that readRecorded needs.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Migrate applies pending versions in order; returns how many versions were applied.
func (r *Runner) Migrate(ctx context.Context, options RunOptions) (int, error) {
	plan, err := r.set.Render(r.maps, r.dialect)
	if err != nil {
		return 0, err
	}

	conn, err := r.pool.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	tx, err := r.dialect.BeginMigrationRunLock(ctx, conn)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := r.ensureVersionTable(ctx, tx); err != nil {
		return 0, err
	}
	recorded, err := readRecorded(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := validateHistory(plan, recorded); err != nil {
		return 0, err
	}

	applied := 0
	for _, version := range plan {
		if recorded.versions[version.Version] {
			continue
		}
		for _, step := range version.Steps {
			start := time.Now()
			for _, statement := range step.Up {
				if err := r.applyStatement(ctx, tx, version.Version, statement, options); err != nil {
					return 0, err
				}
			}
			if err := r.record(ctx, tx, version.Version, step, time.Since(start).Milliseconds()); err != nil {
				return 0, err
			}
		}
		applied++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return applied, nil
}

// MigrateDown reverts versions above targetVersion, newest first: every
// step's rollback is resolved before anything executes — a hand-written Down
// core wins, otherwise DeriveDown from the snapshots; a step with statements
// to undo but no derivable rollback is MIG-020 (ADR-0018).
func (r *Runner) MigrateDown(ctx context.Context, targetVersion int64, options RunOptions) (int, error) {
	plan, err := r.set.Render(r.maps, r.dialect)
	if err != nil {
		return 0, err
	}

	conn, err := r.pool.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	tx, err := r.dialect.BeginMigrationRunLock(ctx, conn)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := r.ensureVersionTable(ctx, tx); err != nil {
		return 0, err
	}
	recorded, err := readRecorded(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err := validateHistory(plan, recorded); err != nil {
		return 0, err
	}

	var reverting []RenderedVersion
	for _, version := range plan {
		if version.Version > targetVersion && recorded.versions[version.Version] {
			reverting = append(reverting, version)
		}
	}
	sort.Slice(reverting, func(i, j int) bool { return reverting[i].Version > reverting[j].Version })

	// Resolve every step's rollback before anything executes (§7.23).
	var notices []string
	derived := map[recordedKey][]MigrationStatement{}
	for _, version := range reverting {
		for _, step := range version.Steps {
			if len(step.Up) == 0 || len(step.Down.Core) > 0 {
				continue
			}
			derivedCore, err := DeriveDown(step.ObjectName, step.Version, r.snapshots, step.Renames, &notices, r.dialect)
			if err != nil {
				return 0, err
			}
			if derivedCore == nil {
				return 0, core.Errorf("MIG-020", fmt.Sprintf("V%04d %s", step.Version, step.ObjectName),
					"no snapshot to derive the rollback from (run simpleorm snapshot/shadow and embed the .schema.json files), or override Down()")
			}
			derived[recordedKey{step.Version, step.ObjectName}] = derivedCore
		}
	}
	if options.Notify != nil {
		for _, notice := range notices {
			options.Notify(notice)
		}
	}

	for _, version := range reverting {
		for i := len(version.Steps) - 1; i >= 0; i-- {
			step := version.Steps[i]
			coreStatements := step.Down.Core
			if len(coreStatements) == 0 {
				coreStatements = derived[recordedKey{step.Version, step.ObjectName}]
			}

			var statements []MigrationStatement
			statements = append(statements, step.Down.Pre...)
			statements = append(statements, coreStatements...)
			statements = append(statements, step.Down.Post...)
			for _, statement := range statements {
				if err := r.applyStatement(ctx, tx, version.Version, statement, options); err != nil {
					return 0, err
				}
			}
		}

		if err := r.deleteVersionRecord(ctx, tx, version.Version); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true
	return len(reverting), nil
}

// Baseline records every step of versions <= version not yet recorded, without running them (§7.23).
func (r *Runner) Baseline(ctx context.Context, version int64) error {
	plan, err := r.set.Render(r.maps, r.dialect)
	if err != nil {
		return err
	}

	conn, err := r.pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	tx, err := r.dialect.BeginMigrationRunLock(ctx, conn)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := r.ensureVersionTable(ctx, tx); err != nil {
		return err
	}
	recorded, err := readRecorded(ctx, tx)
	if err != nil {
		return err
	}

	for _, entry := range plan {
		if entry.Version > version || recorded.versions[entry.Version] {
			continue
		}
		for _, step := range entry.Steps {
			if err := r.record(ctx, tx, entry.Version, step, 0); err != nil {
				return err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// Status reports every planned step's state plus any recorded row the plan
// does not compose (Unknown); no lock is taken (§7.23).
func (r *Runner) Status(ctx context.Context) ([]MigrationEntry, error) {
	plan, err := r.set.Render(r.maps, r.dialect)
	if err != nil {
		return nil, err
	}

	conn, err := r.pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	recorded, err := readRecorded(ctx, conn)
	if err != nil {
		return nil, err
	}

	var entries []MigrationEntry
	for _, version := range plan {
		for _, step := range version.Steps {
			var state MigrationState
			if row, ok := recorded.rows[recordedKey{version.Version, step.ObjectName}]; ok {
				if row.Checksum == computeChecksum(step.Up) {
					state = Applied
				} else {
					state = Drifted
				}
			} else if recorded.versions[version.Version] {
				state = Drifted
			} else {
				state = Pending
			}
			entries = append(entries, MigrationEntry{
				Version: version.Version, ObjectName: step.ObjectName, Description: step.Description, State: state,
			})
		}
	}

	for key, row := range recorded.rows {
		found := false
		for _, version := range plan {
			if version.Version != key.Version {
				continue
			}
			for _, step := range version.Steps {
				if step.ObjectName == key.Object {
					found = true
					break
				}
			}
			break
		}
		if !found {
			entries = append(entries, MigrationEntry{
				Version: key.Version, ObjectName: key.Object, Description: row.Description, State: Unknown,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Version != entries[j].Version {
			return entries[i].Version < entries[j].Version
		}
		return entries[i].ObjectName < entries[j].ObjectName
	})
	return entries, nil
}

// HasPending is true when any planned version is unapplied — the MIG-030 check SchemaGuard runs.
func (r *Runner) HasPending(ctx context.Context) (bool, error) {
	entries, err := r.Status(ctx)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.State == Pending {
			return true, nil
		}
	}
	return false, nil
}

// --- validation ----------------------------------------------------------------

// validateHistory checks the whole plan against what's recorded, before any
// statement executes: drift on an applied step is MIG-010, history unknown to
// the code is MIG-011.
func validateHistory(plan []RenderedVersion, recorded *recordedHistory) error {
	for _, version := range plan {
		if !recorded.versions[version.Version] {
			continue
		}
		for _, step := range version.Steps {
			target := fmt.Sprintf("V%04d %s", version.Version, step.ObjectName)
			row, ok := recorded.rows[recordedKey{version.Version, step.ObjectName}]
			if !ok {
				return core.Errorf("MIG-010", target, "the applied version has no record for this object; history and code disagree")
			}
			if row.Checksum != computeChecksum(step.Up) {
				return core.Errorf("MIG-010", target, "checksum changed since it was applied; applied migrations must not change")
			}
		}
	}

	keys := make([]recordedKey, 0, len(recorded.rows))
	for key := range recorded.rows {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Version != keys[j].Version {
			return keys[i].Version < keys[j].Version
		}
		return keys[i].Object < keys[j].Object
	})
	for _, key := range keys {
		known := false
		for _, version := range plan {
			if version.Version == key.Version {
				known = true
				break
			}
		}
		if !known {
			return core.Errorf("MIG-011", fmt.Sprintf("V%04d %s", key.Version, key.Object), "applied in the database but unknown to the code")
		}
	}
	return nil
}

// computeChecksum is SHA-256 of the joined rendered Up SQL (guards included),
// byte-identical to the C# reference so a C# run on the same migration records
// the same checksum.
func computeChecksum(statements []MigrationStatement) string {
	parts := make([]string, len(statements))
	for i, s := range statements {
		parts[i] = s.SQL
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n;\n")))
	return hex.EncodeToString(sum[:])
}

// --- storage ---------------------------------------------------------------------

func readRecorded(ctx context.Context, q queryer) (*recordedHistory, error) {
	history := &recordedHistory{rows: map[recordedKey]recordedRow{}, versions: map[int64]bool{}}
	rows, err := q.QueryContext(ctx, "select version, object, description, checksum from schema_version")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// No schema_version table yet: nothing recorded.
		return history, nil
	}
	defer rows.Close()

	for rows.Next() {
		var version int64
		var object, description, checksum string
		if err := rows.Scan(&version, &object, &description, &checksum); err != nil {
			return nil, err
		}
		history.rows[recordedKey{version, object}] = recordedRow{Description: description, Checksum: checksum}
		history.versions[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return history, nil
}

func (r *Runner) ensureVersionTable(ctx context.Context, tx *sql.Tx) error {
	return r.execute(ctx, tx, r.dialect.VersionTableSQL())
}

func (r *Runner) record(ctx context.Context, tx *sql.Tx, version int64, step RenderedStep, executionMs int64) error {
	_, err := tx.ExecContext(ctx,
		"insert into schema_version (version, object, description, checksum, applied_at, execution_ms) "+
			"values (@version, @object, @description, @checksum, @applied_at, @execution_ms)",
		sql.Named("version", version),
		sql.Named("object", step.ObjectName),
		sql.Named("description", step.Description),
		sql.Named("checksum", computeChecksum(step.Up)),
		sql.Named("applied_at", core.FormatUTC(time.Now())),
		sql.Named("execution_ms", executionMs))
	return err
}

func (r *Runner) deleteVersionRecord(ctx context.Context, tx *sql.Tx, version int64) error {
	if _, err := tx.ExecContext(ctx, "delete from schema_version where version = @version", sql.Named("version", version)); err != nil {
		return core.Errorf("MIG-021", "migration statement", "%s -- while executing: delete from schema_version where version = %d", err.Error(), version)
	}
	return nil
}

// applyStatement executes one rendered statement — or, for a guard, checks the view's live definition instead (MIG-012).
func (r *Runner) applyStatement(
	ctx context.Context, tx *sql.Tx, version int64, statement MigrationStatement, options RunOptions,
) error {
	if statement.GuardView == "" {
		return r.execute(ctx, tx, statement.SQL)
	}

	live, err := r.readViewDefinition(ctx, tx, statement.GuardView)
	if err != nil {
		return err
	}
	if live != nil && NormalizeDDL(*live) == statement.SQL {
		return nil
	}

	target := fmt.Sprintf("V%04d %s", version, statement.GuardView)
	var drift, reason string
	if live == nil {
		drift = target + ": the view is absent; expected the previous definition"
		reason = "the view is absent but a previous definition was expected"
	} else {
		drift = target + ": the live definition does not match the expected one — it was changed outside migrations"
		reason = "the live definition was changed outside migrations"
	}

	if !options.AllowViewDrift {
		return core.NewError("MIG-012", target, reason+"; review the drift, then rerun with --force to recreate it from the code")
	}
	if options.Notify != nil {
		options.Notify(drift + "; recreating (--force)")
	}
	return nil
}

func (r *Runner) readViewDefinition(ctx context.Context, tx *sql.Tx, view string) (*string, error) {
	row := tx.QueryRowContext(ctx, r.dialect.ViewDefinitionSQL(), sql.Named("relation", view))
	var definition string
	if err := row.Scan(&definition); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &definition, nil
}

func (r *Runner) execute(ctx context.Context, tx *sql.Tx, statement string) error {
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return core.Errorf("MIG-021", "migration statement", "%s -- while executing: %s", err.Error(), statement)
	}
	return nil
}

package migrations

// Shadow rebuilds the versioned schema snapshots (V000N.schema.json) by replaying the migration history into
// a throwaway database and recording, after each version, what the touched objects look like (ADR-0017,
// ADR-0043): the model-vs-history integrity check of spec/migrations.md — a shadow rebuild must reproduce every
// committed snapshot byte-for-byte modulo generatedAt. It runs on every dialect; the throwaway database comes
// from the dialect's ShadowHost.
//
// Go adaptation (CODING-STANDARD §10): the reference resolves each touched relation back to its mapped entity
// by scanning the whole assembly. Go has no assembly to scan, so this loads every entity in Entities up front
// and indexes it by relation name — the direction the PHP port took too (there, from the step's own class).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// ShadowHost is the dialect's tooling contract beside the seam (ADR-0043, spec/dialects.md "Migration
// tooling"), never consulted at runtime: it provisions the throwaway database the replay runs in. A dialect
// whose catalog hands a trigger back losslessly also implements TriggerReadBack.
type ShadowHost interface {
	// Provision creates a throwaway database from the admin connection (a file for SQLite, which ignores it; a
	// `simpleorm_shadow_<hex>` database on SQL Server and PostgreSQL) and returns the connection string that
	// opens it and the function that drops it. The caller closes every pool on the database before dropping.
	Provision(ctx context.Context, adminConnectionString string) (connectionString string, drop func(context.Context) error, err error)
}

// TriggerReadBack is the shadow host's optional trigger read-back: the inverse of Dialect.CreateTriggerSQL
// over the rows of Dialect.TriggerDefinitionSQL, present where the catalog is lossless (SQLite, PostgreSQL;
// not SQL Server, whose `inserted` has lost a token's column list). It returns the declaration the rows spell
// (RelationKind unset — the text does not say) or a note when they do not match the dialect's grammar (a
// hand-written trigger); nil with an empty note when there are no rows.
type TriggerReadBack interface {
	ReadBackTrigger(name string, rows []string) (declaration *TriggerDeclaration, note string)
}

// ShadowOptions configures [Shadow]: the dialect and its host, the migration set to replay, every entity
// Shadow may need to resolve a touched relation to its mapped type, the migrations tree's directory (both the
// trusted baseline source when From > 0 and the destination for regenerated snapshots), and the version range
// to regenerate. To == 0 means unbounded (regenerate through the newest version in Set).
type ShadowOptions struct {
	Dialect core.Dialect
	Host    ShadowHost
	// Admin is the admin connection string handed to Host.Provision: the database the throwaway one is created
	// beside (SQL Server, PostgreSQL); ignored by SQLite.
	Admin    string
	Set      *Set
	Entities []reflect.Type
	// Triggers are the registry's trigger classes (ADR-0042): with the classes the entities link, they name the
	// Trigger/<Class>/ folder a touched trigger's snapshot is written to.
	Triggers []reflect.Type
	OutDir   string
	From     int64
	To       int64
}

// ShadowResult is [Shadow]'s outcome: files written and progress notes, in the order the replay produced them.
type ShadowResult struct {
	WrittenFiles []string
	Notes        []string
}

// Shadow replays Set's versions into a throwaway database from Host, dropped when it returns (failure
// included). With From > 0 the trusted baseline is restored from the committed snapshots under OutDir at <=
// From — through the dialect, each refused with MIG-014 when stamped for another dialect — and those versions
// are baselined (never verified); every version in (From, To] then migrates in turn, and each object it
// touches (per Status at that version) is snapshotted: a table by introspection, a view by the step's last
// create statement, a trigger by the declaration the step's actions leave behind (spec/migrations.md
// "Dialects"). Where the catalog is lossless the shadow reads it too and notes a disagreement.
func Shadow(ctx context.Context, options ShadowOptions) (result *ShadowResult, err error) {
	result = &ShadowResult{}
	if options.Set == nil || len(options.Set.Versions()) == 0 {
		result.Notes = append(result.Notes, "no migration versions found")
		return result, nil
	}
	dialect := options.Dialect

	entityByRelation, err := mapEntitiesByRelation(options.Entities)
	if err != nil {
		return nil, err
	}
	triggerByName, err := mapTriggersByName(options.Entities, options.Triggers)
	if err != nil {
		return nil, err
	}

	connectionString, drop, err := options.Host.Provision(ctx, options.Admin)
	if err != nil {
		return nil, err
	}
	defer func() {
		// Registered first, so it runs last: the pools below are closed by then. A failed drop is reported, not
		// fatal — the replay's own outcome stands.
		if dropErr := drop(context.Background()); dropErr != nil && result != nil {
			result.Notes = append(result.Notes, fmt.Sprintf("the throwaway database was not dropped: %v", dropErr))
		}
	}()

	pool, err := dialect.CreateConnection(connectionString)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	maps := metadata.NewLoader(nil)
	generatedAt := time.Now().UTC()

	// A draft replays like any version (Level 3 M5, migrations.md "Draft migrations"): the throwaway database
	// is no one's history, so the roots are replayed through a wrapper without the marker -- MIG-013 guards
	// real databases only.
	allVersions := replayable(options.Set)
	allNumbers := options.Set.VersionNumbers()
	replaySet, err := NewSet(allVersions...)
	if err != nil {
		return nil, err
	}

	if options.From > 0 {
		if err := restoreBaseline(ctx, conn, dialect, options.OutDir, options.From, entityByRelation, result); err != nil {
			return nil, err
		}
		baselineRunner := NewRunner(pool, dialect, maps, replaySet, nil)
		if err := baselineRunner.Baseline(ctx, options.From); err != nil {
			return nil, err
		}
	}

	replay := &shadowReplay{
		options: options, conn: conn, entities: entityByRelation, triggers: triggerByName,
		generatedAt: generatedAt, result: result,
	}
	for i, number := range allNumbers {
		if number <= options.From {
			continue
		}
		if options.To != 0 && number > options.To {
			continue
		}

		// The runner for this step knows only versions <= number (this
		// version's steps must be exactly what shows Pending): a runner
		// holding the whole set would apply every later version too.
		subset, err := NewSet(allVersions[:i+1]...)
		if err != nil {
			return nil, err
		}
		runner := NewRunner(pool, dialect, maps, subset, nil)

		statuses, err := runner.Status(ctx)
		if err != nil {
			return nil, err
		}
		touched := touchedObjects(statuses, number)

		if _, err := runner.Migrate(ctx, RunOptions{}); err != nil {
			return nil, err
		}

		steps, err := renderedStepsAt(subset, maps, dialect, number)
		if err != nil {
			return nil, err
		}
		for _, relation := range touched {
			if err := replay.snapshot(ctx, relation, number, steps[relation]); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

// shadowReplay carries what the per-object snapshotting of one Shadow run shares.
type shadowReplay struct {
	options     ShadowOptions
	conn        *sql.Conn
	entities    map[string]mappedEntity
	triggers    map[string]mappedTrigger
	generatedAt time.Time
	result      *ShadowResult
}

// snapshot records one touched object at one version: a trigger by its declaration, a mapped table or view by
// its shape; an object with no mapped entity is skipped with a note.
func (r *shadowReplay) snapshot(ctx context.Context, relation string, number int64, step RenderedStep) error {
	if trigger, isTrigger := r.triggers[strings.ToLower(relation)]; isTrigger {
		return r.snapshotTrigger(ctx, trigger, relation, number, step)
	}
	entity, ok := r.entities[strings.ToLower(relation)]
	if !ok {
		r.result.Notes = append(r.result.Notes, fmt.Sprintf("V%04d: '%s' has no mapped entity; snapshot skipped", number, relation))
		return nil
	}

	dialect := r.options.Dialect
	var content string
	if entity.kind == core.RelationTable {
		schema, err := introspectTable(ctx, r.conn, dialect, relation)
		if err != nil {
			return err
		}
		if schema == nil {
			return nil // dropped at this version -- its snapshot history ends here
		}
		content = ExportSchema(schema, number, r.generatedAt)
	} else {
		ddl, err := r.viewSnapshot(ctx, relation, number, step)
		if err != nil || ddl == "" {
			return err
		}
		content = ExportDDL(relation, dialect.Name(), entity.kindToken, ddl, number, r.generatedAt)
	}
	return r.write(entity.kindFolder, entity.typeName, number, content)
}

// viewSnapshot is a view's neutral definition after the step: the step's last create statement, normalized (a
// later drop ends the history, "" back). Where the catalog stores the text verbatim it is read too and a
// disagreement noted; a step with no create falls back to that catalog text, and is skipped with a note where
// the catalog reformats (PostgreSQL) — spec/migrations.md "Dialects".
func (r *shadowReplay) viewSnapshot(ctx context.Context, relation string, number int64, step RenderedStep) (string, error) {
	dialect := r.options.Dialect
	created, state := viewHistory(step.Up)
	if state == viewDropped {
		return "", nil
	}

	verbatim := dialect.ViewDefinitionIsVerbatim()
	var catalog *string
	if verbatim {
		stored, err := ReadViewDefinition(ctx, r.conn, dialect, relation)
		if err != nil {
			return "", err
		}
		if stored != nil {
			normalized := NormalizeDDL(*stored)
			catalog = &normalized
		}
	}

	switch {
	case state == viewCreated:
		if verbatim && (catalog == nil || *catalog != created) {
			r.result.Notes = append(r.result.Notes, fmt.Sprintf(
				"V%04d: view %s: the catalog disagrees with the step's create statement; the snapshot follows the step", number, relation))
		}
		return created, nil
	case verbatim && catalog != nil:
		return *catalog, nil
	case verbatim:
		return "", nil // not a view right now: dropped, or never one
	}
	r.result.Notes = append(r.result.Notes, fmt.Sprintf(
		"V%04d: view %s: the step has no create statement and the %s catalog reformats a view; snapshot skipped",
		number, relation, dialect.Name()))
	return "", nil
}

// snapshotTrigger records a trigger by the declaration the step's actions leave behind (create or recreate →
// declared, drop → gone: the history ends). Where the host reads the catalog back, it does so too and notes a
// disagreement; a step with no declaration of its own (raw SQL) falls back to the read-back, and is skipped
// with a note where there is none.
func (r *shadowReplay) snapshotTrigger(ctx context.Context, trigger mappedTrigger, name string, number int64, step RenderedStep) error {
	dialect := r.options.Dialect
	if step.TriggerDropped && step.Trigger == nil {
		return nil // dropped at this version -- its snapshot history ends here
	}

	var parsed *TriggerDeclaration
	readBack, hasReadBack := r.options.Host.(TriggerReadBack)
	if hasReadBack {
		rows, err := ReadTriggerDefinitions(ctx, r.conn, dialect, name)
		if err != nil {
			return err
		}
		var note string
		parsed, note = readBack.ReadBackTrigger(name, rows)
		if parsed == nil && note != "" {
			r.result.Notes = append(r.result.Notes, fmt.Sprintf("V%04d: %s", number, note))
		}
	}

	declaration := step.Trigger
	switch {
	case declaration == nil && parsed == nil:
		if !hasReadBack {
			r.result.Notes = append(r.result.Notes, fmt.Sprintf(
				"V%04d: trigger %s: the step declares none and %s has no catalog read-back; snapshot skipped", number, name, dialect.Name()))
		}
		return nil
	case declaration == nil:
		declaration = parsed
		declaration.RelationKind = r.relationKindOf(parsed.Relation)
	case parsed != nil:
		parsed.RelationKind = declaration.RelationKind
		if !parsed.SameAs(declaration) {
			r.result.Notes = append(r.result.Notes, fmt.Sprintf(
				"V%04d: trigger %s: the catalog disagrees with the step's declaration; the snapshot follows the step", number, name))
		}
	}

	exported := *declaration
	exported.Dialect = dialect.Name()
	return r.write("Trigger", trigger.typeName, number, ExportTrigger(&exported, number, r.generatedAt))
}

// relationKindOf is whether a relation is a mapped view; anything else is taken as a table.
func (r *shadowReplay) relationKindOf(relation string) string {
	if entity, ok := r.entities[strings.ToLower(relation)]; ok && entity.kind == core.RelationView {
		return RelationKindView
	}
	return RelationKindTable
}

func (r *shadowReplay) write(folder, typeName string, number int64, content string) error {
	directory := filepath.Join(r.options.OutDir, folder, typeName)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return err
	}
	file := filepath.Join(directory, fmt.Sprintf("V%04d.schema.json", number))
	if err := os.WriteFile(file, []byte(content+"\n"), 0o666); err != nil {
		return err
	}
	r.result.WrittenFiles = append(r.result.WrittenFiles, file)
	return nil
}

// renderedStepsAt is the rendered steps of one version, by object name — what the replay just applied.
func renderedStepsAt(set *Set, maps *metadata.Loader, dialect core.Dialect, version int64) (map[string]RenderedStep, error) {
	plan, err := set.Render(maps, dialect)
	if err != nil {
		return nil, err
	}
	steps := map[string]RenderedStep{}
	for _, rendered := range plan {
		if rendered.Version != version {
			continue
		}
		for _, step := range rendered.Steps {
			steps[step.ObjectName] = step
		}
	}
	return steps, nil
}

type viewHistoryState int

const (
	viewNoStatement viewHistoryState = iota
	viewCreated
	viewDropped
)

var dropViewPattern = regexp.MustCompile(`(?is)^drop\s+(?:materialized\s+)?view\b`)

// viewHistory reads a view step's statements in order: the last create (any dialect's prefix, normalized to the
// neutral snapshot form) and whether a later drop ended it. Guards are preconditions, not statements.
func viewHistory(up []MigrationStatement) (ddl string, state viewHistoryState) {
	for _, statement := range up {
		if statement.GuardView != "" || statement.GuardTrigger != "" {
			continue
		}
		collapsed := strings.Join(strings.Fields(statement.SQL), " ")
		switch {
		case viewDDLPattern.MatchString(collapsed):
			ddl, state = NormalizeDDL(collapsed), viewCreated
		case dropViewPattern.MatchString(collapsed):
			ddl, state = "", viewDropped
		}
	}
	return ddl, state
}

// replayVersion is a root seen through the shadow replay: the same steps and number, never a draft.
type replayVersion struct {
	inner  Version
	number int64
}

func (r replayVersion) Version() int64            { return r.number }
func (r replayVersion) Compose(b *VersionBuilder) { r.inner.Compose(b) }

// replayable wraps every root of set as a replayVersion, in the set's order.
func replayable(set *Set) []Version {
	roots := set.Versions()
	numbers := set.VersionNumbers()
	out := make([]Version, len(roots))
	for i, root := range roots {
		out[i] = replayVersion{inner: root, number: numbers[i]}
	}
	return out
}

// touchedObjects is the distinct object names Status reports at exactly version.
func touchedObjects(entries []MigrationEntry, version int64) []string {
	seen := map[string]bool{}
	var names []string
	for _, entry := range entries {
		if entry.Version == version && !seen[entry.ObjectName] {
			seen[entry.ObjectName] = true
			names = append(names, entry.ObjectName)
		}
	}
	return names
}

// --- trusted baseline (From > 0) --------------------------------------------

// restoreBaseline creates every table/view/trigger whose latest snapshot at or below fromVersion exists under
// migrationsDir, rendered through the dialect from that snapshot — trusted, never verified against the actual
// migration history below it. A snapshot stamped for another dialect is MIG-014 (a tree belongs to one dialect).
func restoreBaseline(
	ctx context.Context, conn *sql.Conn, dialect core.Dialect, migrationsDir string, fromVersion int64,
	entities map[string]mappedEntity, result *ShadowResult,
) error {
	tableRoot := filepath.Join(migrationsDir, "Table")
	if info, statErr := os.Stat(tableRoot); statErr == nil && info.IsDir() {
		objectDirs, err := os.ReadDir(tableRoot)
		if err != nil {
			return err
		}
		for _, entry := range objectDirs {
			if !entry.IsDir() {
				continue
			}
			schema, atVersion, ok := LatestTableSnapshotAtOrBefore(filepath.Join(tableRoot, entry.Name()), fromVersion)
			if !ok {
				continue // table born after the trusted version
			}
			if err := CheckSnapshotDialect(schema.Dialect, dialect, schema.Name); err != nil {
				return err
			}
			if err := execStatement(ctx, conn, dialect.CreateTableFromSchemaSQL(schema)); err != nil {
				return err
			}
			for _, index := range schema.Indexes {
				if err := execStatement(ctx, conn, dialect.CreateIndexFromSchemaSQL(schema.Name, index)); err != nil {
					return err
				}
			}
			result.Notes = append(result.Notes,
				fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", schema.Name, atVersion))
		}
	} else {
		result.Notes = append(result.Notes,
			fmt.Sprintf("--from V%04d: no committed snapshots under %s; starting empty", fromVersion, tableRoot))
	}

	// Views restore after tables -- their neutral definition renders again through the dialect.
	for _, kindFolder := range []string{"View", "MaterializedView"} {
		kindRoot := filepath.Join(migrationsDir, kindFolder)
		info, statErr := os.Stat(kindRoot)
		if statErr != nil || !info.IsDir() {
			continue
		}
		objectDirs, err := os.ReadDir(kindRoot)
		if err != nil {
			return err
		}
		for _, entry := range objectDirs {
			if !entry.IsDir() {
				continue
			}
			snapshot, ok := LatestDDLSnapshotAtOrBefore(filepath.Join(kindRoot, entry.Name()), fromVersion)
			if !ok {
				continue
			}
			if err := CheckSnapshotDialect(snapshot.Dialect, dialect, snapshot.Object); err != nil {
				return err
			}
			statement := snapshot.DDL
			if name, body, materialized, parsed := ViewParts(snapshot.DDL); parsed {
				statement = dialect.CreateViewFromBodySQL(name, body, materialized)
			}
			if err := execStatement(ctx, conn, statement); err != nil {
				return err
			}
			result.Notes = append(result.Notes,
				fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", snapshot.Object, snapshot.AsOfVersion))
		}
	}

	// Triggers restore last: their relation must exist (spec/migrations.md "Structure").
	return restoreTriggerBaseline(ctx, conn, dialect, migrationsDir, fromVersion, result)
}

// restoreTriggerBaseline recreates every trigger whose latest snapshot at or below fromVersion exists under
// migrationsDir, rendered through the dialect from the snapshot's declaration — trusted, like the tables and
// views before it, so a later trigger step's guard finds what it expects.
func restoreTriggerBaseline(
	ctx context.Context, conn *sql.Conn, dialect core.Dialect, migrationsDir string, fromVersion int64, result *ShadowResult,
) error {
	root := filepath.Join(migrationsDir, "Trigger")
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return nil
	}
	objectDirs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range objectDirs {
		if !entry.IsDir() {
			continue
		}
		declaration, atVersion, ok := LatestTriggerSnapshotAtOrBefore(filepath.Join(root, entry.Name()), fromVersion)
		if !ok {
			continue
		}
		if err := CheckSnapshotDialect(declaration.Dialect, dialect, declaration.Name); err != nil {
			return err
		}
		creates, err := dialect.CreateTriggerSQL(declaration.Relation, declaration.OnView(), declaration.Definition())
		if err != nil {
			return err
		}
		for _, statement := range append(dialect.DropTriggerSQL(declaration.Relation, declaration.Name), creates...) {
			if err := execStatement(ctx, conn, statement); err != nil {
				return err
			}
		}
		result.Notes = append(result.Notes,
			fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", declaration.Name, atVersion))
	}
	return nil
}

// --- introspection -----------------------------------------------------------

// introspectTable is the live shape of relation, or nil when it does not exist right now (dropped at this
// version). Column and index reads go through ReadLiveColumns/ReadLiveIndexes — the one introspection helper
// also used by force-sync and SchemaGuard (CODING-STANDARD §8) — so this is only the shape conversion into a
// TableSchema: nullable is "not notnull and not key", generated is the dialect's fifth column (ADR-0043).
func introspectTable(ctx context.Context, conn queryer, dialect core.Dialect, relation string) (*TableSchema, error) {
	live, err := ReadLiveColumns(ctx, conn, dialect, relation)
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return nil, nil
	}

	columns := make([]TableColumn, 0, len(live))
	for _, c := range live {
		columns = append(columns, TableColumn{
			Name: c.Name, StorageType: c.DeclaredType, Nullable: !c.EffectivelyNotNull(), Key: c.PrimaryKey, Generated: c.Generated,
		})
	}

	indexes, err := ReadLiveIndexes(ctx, conn, dialect, relation)
	if err != nil {
		return nil, err
	}

	return &TableSchema{Name: relation, Dialect: dialect.Name(), Columns: columns, Indexes: indexes}, nil
}

// ReadViewDefinition is the stored definition of a view through the dialect's ViewDefinitionSQL, as stored
// (callers normalize), or nil when it is absent.
func ReadViewDefinition(ctx context.Context, q rowQueryer, dialect core.Dialect, view string) (*string, error) {
	row := q.QueryRowContext(ctx, dialect.ViewDefinitionSQL(), sql.Named("relation", view))
	var definition sql.NullString
	if err := row.Scan(&definition); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !definition.Valid {
		return nil, nil
	}
	return &definition.String, nil
}

// rowQueryer is the single-row read surface of *sql.Conn, *sql.Tx, and *sql.DB.
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func execStatement(ctx context.Context, conn *sql.Conn, statement string) error {
	_, err := conn.ExecContext(ctx, statement)
	return err
}

// --- entity resolution ---------------------------------------------------------

// mappedEntity is one entity's snapshot destination: its type name (the folder under kindFolder) and the kind
// token a DDL-shaped snapshot records.
type mappedEntity struct {
	typeName   string
	kind       core.RelationKind
	kindFolder string
	kindToken  string
}

// mapEntitiesByRelation loads every entity in entities (trigger classes are no entities and are skipped) and
// indexes the table/view/materialized-view ones by relation name (case-insensitive): Shadow resolves a touched
// relation back to its entity this way, since Go has no assembly to scan the other direction (CODING-STANDARD
// §10).
func mapEntitiesByRelation(entities []reflect.Type) (map[string]mappedEntity, error) {
	loader := metadata.NewLoader(nil)
	byRelation := map[string]mappedEntity{}
	for _, t := range entities {
		if core.IsTriggerType(t) {
			continue
		}
		m, err := loader.Load(t)
		if err != nil {
			return nil, err
		}
		if m.RelationName == "" {
			continue
		}

		entry := mappedEntity{typeName: t.Name(), kind: m.Kind}
		switch m.Kind {
		case core.RelationTable:
			entry.kindFolder = "Table"
		case core.RelationView:
			entry.kindFolder, entry.kindToken = "View", "view"
		case core.RelationMaterializedView:
			entry.kindFolder, entry.kindToken = "MaterializedView", "materialized_view"
		default:
			continue // statements and (dormant) procedures are not schema objects to snapshot
		}
		byRelation[strings.ToLower(m.RelationName)] = entry
	}
	return byRelation, nil
}

// mappedTrigger is a trigger's snapshot destination: its class's name (the folder under Trigger/).
type mappedTrigger struct {
	typeName string
}

// mapTriggersByName indexes every trigger class the entities link, and every class the registry lists, by the
// name it declares (case-insensitive): Shadow resolves a touched object back to its class this way, since Go
// has no assembly to scan the other direction (CODING-STANDARD §10).
func mapTriggersByName(entities, triggerTypes []reflect.Type) (map[string]mappedTrigger, error) {
	loader := metadata.NewLoader(nil)
	byName := map[string]mappedTrigger{}
	note := func(t reflect.Type) {
		t = metadata.EntityType(t)
		if declaration, ok := core.TriggerDeclarationOf(t); ok {
			byName[strings.ToLower(strings.TrimSpace(declaration.Name))] = mappedTrigger{typeName: t.Name()}
		}
	}

	for _, t := range entities {
		if core.IsTriggerType(t) {
			continue
		}
		m, err := loader.Load(t)
		if err != nil {
			return nil, err
		}
		for _, trigger := range m.Triggers {
			note(trigger.Type)
		}
	}
	for _, t := range triggerTypes {
		note(t)
	}
	return byName, nil
}

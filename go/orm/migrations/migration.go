package migrations

import (
	"reflect"
	"regexp"
	"strconv"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Version is a root migration (§7.22): the recorded, checksummed unit that
// composes its object steps in explicit, reviewable order. Its number parses
// from the concrete type's name (V<version>, e.g. V0001, MIG-001 on a
// mismatch) unless the type implements Versioned (the data-driven SQLVersion).
type Version interface {
	Compose(*VersionBuilder)
}

// Versioned overrides a root version's type-name-parsed number.
type Versioned interface {
	Version() int64
}

// Described overrides a step's type-name-parsed version and description (the
// data-driven raw step implements it).
type Described interface {
	Version() int64
	Description() string
}

// VersionBuilder collects a version's object steps in explicit,
// caller-declared order (FK order across tables is the author's/generator's
// job).
type VersionBuilder struct {
	steps []Step
}

// Apply appends step and returns the builder for chaining.
func (b *VersionBuilder) Apply(step Step) *VersionBuilder {
	b.steps = append(b.steps, step)
	return b
}

// Steps returns the steps applied so far, in declaration order.
func (b *VersionBuilder) Steps() []Step { return b.steps }

// stepKind is a step's rendering shape.
type stepKind int

const (
	stepKindTable stepKind = iota
	stepKindView
	stepKindRaw
)

// stepDescriptor is what Set needs to render a step, regardless of its
// concrete kind: the entity type for table/view steps, or the raw SQL data
// for a data-driven step.
type stepDescriptor struct {
	kind       stepKind
	entityType reflect.Type

	// Raw-step-only fields (SQLVersion).
	objectName       string
	up               []string
	down             []string
	renames          []ColumnRename
	expectDefinition string
}

// Step is one object's migration step (§7.22): a class named
// V<version>_<Description> in the object's folder (Table/User/…). Only the
// library's markers — TableMigration[T], ViewMigration[T], and the
// data-driven raw step — satisfy it: describe is unexported and declared in
// this package, so embedding one of those markers is the only way to
// implement Step (Go's "sealed interface" idiom) — which is what makes
// VersionBuilder.Apply type-safe.
type Step interface {
	describe() stepDescriptor
}

// TableMigration is embedded by a table step to declare its entity type
// (ADR-0013). The step itself must additionally implement
// Action(*TableActions); it may implement Down(*TableActions) (TableDowner),
// PreDown(*MigrationSQL) (PreDowner), and PostDown(*MigrationSQL) (PostDowner)
// for the manual-override rollback (ADR-0016/0018):
//
//	type V0002_AddDisplayName struct{ orm.TableMigration[models.User] }
//
//	func (V0002_AddDisplayName) Action(a *orm.TableActions) {
//		a.AddColumn("display_name", "TEXT").Post("update users set display_name = name")
//	}
type TableMigration[T any] struct{}

func (TableMigration[T]) describe() stepDescriptor {
	return stepDescriptor{kind: stepKindTable, entityType: reflect.TypeFor[T]()}
}

// ViewMigration is embedded by a view (or materialized view) step to declare
// its entity type; the step implements Action(*ViewActions) and may implement
// Down(*ViewActions) (ViewDowner), PreDown/PostDown (PreDowner/PostDowner).
type ViewMigration[T any] struct{}

func (ViewMigration[T]) describe() stepDescriptor {
	return stepDescriptor{kind: stepKindView, entityType: reflect.TypeFor[T]()}
}

// TableDowner is the manual rollback override for a table step (ADR-0018);
// the default (no override) derives the rollback from the versioned snapshots
// — a later phase.
type TableDowner interface {
	Down(*TableActions)
}

// ViewDowner is the manual rollback override for a view step.
type ViewDowner interface {
	Down(*ViewActions)
}

// PreDowner runs data work before the rollback DDL (e.g. stash values a
// destructive revert would lose).
type PreDowner interface {
	PreDown(*MigrationSQL)
}

// PostDowner runs data work after the rollback DDL (e.g. restore or transform).
type PostDowner interface {
	PostDown(*MigrationSQL)
}

var (
	rootNamePattern = regexp.MustCompile(`^V(\d+)$`)
	stepNamePattern = regexp.MustCompile(`^V(\d+)_(\w+)$`)
)

// concreteTypeName is the type name MIG-001 parses and MIG-003 names: a
// pointer resolves to its element (steps are ordinarily plain structs, but a
// pointer receiver works the same way).
func concreteTypeName(v any) string {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return "<nil>"
	}
	return t.Name()
}

// rootVersionNumber is a root's version: Versioned.Version() when the type
// implements it, else its type name parsed as V<version> (MIG-001).
func rootVersionNumber(v Version) (int64, error) {
	if versioned, ok := v.(Versioned); ok {
		return versioned.Version(), nil
	}
	name := concreteTypeName(v)
	match := rootNamePattern.FindStringSubmatch(name)
	if match == nil {
		return 0, core.Errorf("MIG-001", name, "root migration type names are V<version>, e.g. V0001")
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, core.Errorf("MIG-001", name, "version number out of range: %s", err)
	}
	return number, nil
}

// stepIdentity is a step's (version, description): Described's values when
// the type implements it (the data-driven raw step), else its type name
// parsed as V<version>_<Description> (MIG-001).
func stepIdentity(step Step) (version int64, description string, err error) {
	if described, ok := step.(Described); ok {
		return described.Version(), described.Description(), nil
	}
	name := concreteTypeName(step)
	match := stepNamePattern.FindStringSubmatch(name)
	if match == nil {
		return 0, "", core.Errorf(
			"MIG-001", name, "object migration type names are V<version>_<Description>, e.g. V0002_AddDisplayName")
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, "", core.Errorf("MIG-001", name, "version number out of range: %s", err)
	}
	return number, match[2], nil
}

// ColumnRename is a step's declared rename, kept structurally (not just as
// SQL) so a future derived rollback can invert it: snapshots alone cannot
// tell a rename from a drop+add (ADR-0018).
type ColumnRename struct {
	From string
	To   string
}

// MigrationStatement is one rendered statement with the action it came from,
// for error reporting. A statement carrying GuardView is a precondition, not
// SQL to run: the runner compares the named view's live definition (already
// normalized) against SQL before continuing (MIG-012) — views get patched
// outside code in urgencies, and a migration must not silently overwrite that.
// Guards still count into the step checksum.
type MigrationStatement struct {
	SQL       string
	Origin    string
	GuardView string
}

// DownPlan is a step's rendered rollback: data hooks around the DDL core.
// Reversibility is judged on Core alone — hooks without a core do not make a
// step reversible (ADR-0018).
type DownPlan struct {
	Pre  []MigrationStatement
	Core []MigrationStatement
	Post []MigrationStatement
}

// MigrationSQL collects a step's PreDown/PostDown data-hook statements.
type MigrationSQL struct {
	statements []string
}

// SQL appends one statement and returns the collector for chaining.
func (m *MigrationSQL) SQL(sql string) *MigrationSQL {
	m.statements = append(m.statements, sql)
	return m
}

func (m *MigrationSQL) render(origin string) []MigrationStatement {
	if m == nil || len(m.statements) == 0 {
		return nil
	}
	out := make([]MigrationStatement, len(m.statements))
	for i, sql := range m.statements {
		out[i] = MigrationStatement{SQL: sql, Origin: origin}
	}
	return out
}

// MigrationAction is one action with its optional per-action data hooks: Pre
// runs immediately before it, Post immediately after (e.g. preserve values
// before a remove, backfill after an add).
type MigrationAction struct {
	origin     string
	statements []string
	guard      *MigrationStatement
	preSQL     []string
	postSQL    []string
}

func newAction(origin string, statements []string) *MigrationAction {
	return &MigrationAction{origin: origin, statements: statements}
}

// Pre runs sql immediately before the action's own statements. Optional; chainable.
func (a *MigrationAction) Pre(sql string) *MigrationAction {
	a.preSQL = append(a.preSQL, sql)
	return a
}

// Post runs sql immediately after the action's own statements. Optional; chainable.
func (a *MigrationAction) Post(sql string) *MigrationAction {
	a.postSQL = append(a.postSQL, sql)
	return a
}

func (a *MigrationAction) render() []MigrationStatement {
	var out []MigrationStatement
	if a.guard != nil {
		out = append(out, *a.guard)
	}
	for _, sql := range a.preSQL {
		out = append(out, MigrationStatement{SQL: sql, Origin: a.origin + " pre"})
	}
	for _, sql := range a.statements {
		out = append(out, MigrationStatement{SQL: sql, Origin: a.origin})
	}
	for _, sql := range a.postSQL {
		out = append(out, MigrationStatement{SQL: sql, Origin: a.origin + " post"})
	}
	return out
}

// ColumnOption configures AddColumn (Go has no optional parameters).
type ColumnOption func(*columnOptions)

type columnOptions struct {
	nullable   bool
	defaultSQL string
}

// NotNull marks an added column NOT NULL; defaultSQL ("" for none) renders as
// the column's DEFAULT clause — usually required when the table already has
// rows.
func NotNull(defaultSQL string) ColumnOption {
	return func(o *columnOptions) {
		o.nullable = false
		o.defaultSQL = defaultSQL
	}
}

// TableActions are a table step's actions (§7.22, ADR-0013): whatever the
// declaration order, execution is rename -> add -> remove -> raw SQL
// (declaration order inside each group) — renames running first is what makes
// a rename expressible without data loss. Column specs are literal (name,
// type, nullability, default) so an applied migration's rendered SQL never
// changes when the model evolves; CreateTable (metadata-rendered) is legal
// only for an object's initial creation, frozen thereafter by the checksum.
// Built only by Set.Render (DDL-001 when the entity is not table-backed).
type TableActions struct {
	table     string
	entityMap *core.EntityMap
	dialect   core.Dialect

	renames []*MigrationAction
	adds    []*MigrationAction
	removes []*MigrationAction
	custom  []*MigrationAction

	columnRenames []ColumnRename
}

func newTableActions(m *core.EntityMap, dialect core.Dialect) *TableActions {
	return &TableActions{table: m.RelationName, entityMap: m, dialect: dialect}
}

// CreateTable renders the object's initial creation from metadata: the table
// plus its declared indexes.
func (a *TableActions) CreateTable() *MigrationAction {
	statements := append([]string{a.dialect.CreateTableSQL(a.entityMap)}, a.dialect.CreateIndexSQL(a.entityMap)...)
	return a.track(&a.adds, newAction("create "+a.table, statements))
}

// DropTable drops the table.
func (a *TableActions) DropTable() *MigrationAction {
	return a.track(&a.removes, newAction("drop "+a.table, []string{a.dialect.DropTableSQL(a.table)}))
}

// RenameTable renames the table from fromName to its current mapped name.
func (a *TableActions) RenameTable(fromName string) *MigrationAction {
	return a.track(&a.renames, newAction(
		"rename table "+fromName, []string{a.dialect.RenameTableSQL(fromName, a.table)}))
}

// RenameColumn also records the rename structurally (ColumnRenames) for a
// future derived rollback: snapshots alone cannot tell a rename from drop+add.
func (a *TableActions) RenameColumn(fromName, toName string) *MigrationAction {
	a.columnRenames = append(a.columnRenames, ColumnRename{From: fromName, To: toName})
	return a.track(&a.renames, newAction(
		"rename "+a.table+"."+fromName, []string{a.dialect.RenameColumnSQL(a.table, fromName, toName)}))
}

// AddColumn adds a literal column spec: nullable with no default unless opts
// says otherwise (NotNull).
func (a *TableActions) AddColumn(name, storageType string, opts ...ColumnOption) *MigrationAction {
	options := columnOptions{nullable: true}
	for _, opt := range opts {
		opt(&options)
	}
	return a.track(&a.adds, newAction(
		"add "+a.table+"."+name,
		[]string{a.dialect.AddColumnSQL(a.table, name, storageType, options.nullable, options.defaultSQL)}))
}

// RemoveColumn drops a column.
func (a *TableActions) RemoveColumn(name string) *MigrationAction {
	return a.track(&a.removes, newAction("remove "+a.table+"."+name, []string{a.dialect.DropColumnSQL(a.table, name)}))
}

// CreateIndexes renders every declared index from metadata.
func (a *TableActions) CreateIndexes() *MigrationAction {
	return a.track(&a.adds, newAction("indexes "+a.table, a.dialect.CreateIndexSQL(a.entityMap)))
}

// DropIndex drops an index by name.
func (a *TableActions) DropIndex(name string) *MigrationAction {
	return a.track(&a.removes, newAction("drop index "+name, []string{"drop index " + name}))
}

// SQL is the raw-SQL escape hatch; it runs after the ordered groups.
func (a *TableActions) SQL(sql string) *MigrationAction {
	return a.track(&a.custom, newAction("sql "+a.table, []string{sql}))
}

// ColumnRenames returns the step's declared renames, for a future derived rollback.
func (a *TableActions) ColumnRenames() []ColumnRename {
	return a.columnRenames
}

func (a *TableActions) build() []MigrationStatement {
	var out []MigrationStatement
	for _, group := range [][]*MigrationAction{a.renames, a.adds, a.removes, a.custom} {
		for _, action := range group {
			out = append(out, action.render()...)
		}
	}
	return out
}

func (a *TableActions) track(group *[]*MigrationAction, action *MigrationAction) *MigrationAction {
	*group = append(*group, action)
	return action
}

// ViewActions are a view (or materialized view) step's actions; they execute
// in declaration order. Built only by Set.Render (DDL-001 when the entity is
// not view-backed, DDL-002 for a materialized view on a dialect without them).
type ViewActions struct {
	view      string
	entityMap *core.EntityMap
	dialect   core.Dialect
	actions   []*MigrationAction
}

func newViewActions(m *core.EntityMap, dialect core.Dialect) *ViewActions {
	return &ViewActions{view: m.RelationName, entityMap: m, dialect: dialect}
}

// CreateView renders the object's initial creation from metadata.
func (a *ViewActions) CreateView() *MigrationAction {
	statements := append([]string{a.dialect.CreateViewSQL(a.entityMap)}, a.dialect.CreateIndexSQL(a.entityMap)...)
	return a.track(newAction("create "+a.view, statements))
}

// DropView drops the view.
func (a *ViewActions) DropView() *MigrationAction {
	return a.track(newAction("drop "+a.view, []string{"drop view " + a.view}))
}

// RecreateView drops (if present) and re-creates the view from its current defining SQL.
func (a *ViewActions) RecreateView() *MigrationAction {
	statements := append([]string{"drop view if exists " + a.view}, a.dialect.CreateViewSQL(a.entityMap))
	statements = append(statements, a.dialect.CreateIndexSQL(a.entityMap)...)
	return a.track(newAction("recreate "+a.view, statements))
}

// SQL is the raw-SQL escape hatch.
func (a *ViewActions) SQL(sql string) *MigrationAction {
	return a.track(newAction("sql "+a.view, []string{sql}))
}

// ExpectDefinition declares the MIG-012 apply guard: the view's live
// definition must match ddl (whitespace-normalized) when this step applies.
// On mismatch — the view was adjusted outside the code — the run refuses
// unless forced. Generated change steps carry this automatically from the
// previous snapshot.
func (a *ViewActions) ExpectDefinition(ddl string) *MigrationAction {
	action := a.track(newAction("expect "+a.view, nil))
	action.guard = &MigrationStatement{SQL: NormalizeDDL(ddl), Origin: "expect " + a.view, GuardView: a.view}
	return action
}

func (a *ViewActions) build() []MigrationStatement {
	var out []MigrationStatement
	for _, action := range a.actions {
		out = append(out, action.render()...)
	}
	return out
}

func (a *ViewActions) track(action *MigrationAction) *MigrationAction {
	a.actions = append(a.actions, action)
	return action
}

// SQLVersion is a data-driven version — raw SQL steps supplied as data
// instead of Go types (used by the conformance suite; also the shape a future
// generator target could produce). It implements Versioned, so its number
// never depends on the type's name.
type SQLVersion struct {
	version int64
	steps   []SQLVersionStep
}

// SQLVersionStep is one raw-SQL step's data: the object it changes, its
// description, the Up/Down statements, its declared renames, and the expected
// previous definition for a view guard ("" for none).
type SQLVersionStep struct {
	ObjectName       string
	Description      string
	Up               []string
	Down             []string
	Renames          []ColumnRename
	ExpectDefinition string
}

// NewSQLVersion builds a data-driven version composing one raw step per entry in steps.
func NewSQLVersion(version int64, steps ...SQLVersionStep) *SQLVersion {
	return &SQLVersion{version: version, steps: steps}
}

// Version is version's number (Versioned) — never parsed from a type name.
func (v *SQLVersion) Version() int64 { return v.version }

// Compose applies one raw step per declared SQLVersionStep, in order.
func (v *SQLVersion) Compose(builder *VersionBuilder) {
	for _, step := range v.steps {
		builder.Apply(rawStep{version: v.version, data: step})
	}
}

// rawStep is the Step implementation backing one SQLVersionStep entry.
type rawStep struct {
	version int64
	data    SQLVersionStep
}

func (r rawStep) describe() stepDescriptor {
	return stepDescriptor{
		kind:             stepKindRaw,
		objectName:       r.data.ObjectName,
		up:               r.data.Up,
		down:             r.data.Down,
		renames:          r.data.Renames,
		expectDefinition: r.data.ExpectDefinition,
	}
}

// Version and Description implement Described: a raw step's identity is its
// data, never a type name (every rawStep shares the same Go type).
func (r rawStep) Version() int64      { return r.version }
func (r rawStep) Description() string { return r.data.Description }

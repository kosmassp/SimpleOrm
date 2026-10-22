package migrations_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// Triggers in migrations (Level 4, ADR-0042): a trigger step's actions, the MIG-012 guard reused for triggers,
// the derived rollback, the snapshot document, and the generator — on SQLite, with real databases.

// trigWidget is the table the trigger fixtures sit on; trigAudit logs every update of it.
type trigWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (trigWidget) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.Table("trig_widgets"),
		Triggers: []orm.TriggerLink{orm.Trigger[trigAudit]()},
	}
}

type trigAudit struct{}

func (trigAudit) Trigger() orm.TriggerDef {
	return orm.TriggerDef{
		Name: "trg_trig_widgets", Timing: orm.TimingAfter, Events: orm.EventUpdate,
		SQL: "insert into trig_log (widget_id, note) select n.id, 'v1' from {new: id} n;",
	}
}

// trigWidgetPlain is the same relation with no trigger linked: the model after the declaration was removed.
type trigWidgetPlain struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (trigWidgetPlain) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("trig_widgets")}
}

const (
	trigV1Body = "insert into trig_log (widget_id, note) select n.id, 'v1' from {new: id} n;"
	trigV2Body = "insert into trig_log (widget_id, note) select n.id, 'v2' from {new: id} n;"
)

// V1401 creates the two tables as data-driven steps; the typed trigger steps follow (a step's version is the
// digits of its type name).
type V1401 struct{}

func (V1401) Compose(v *migrations.VersionBuilder) {
	migrations.NewSQLVersion(1401,
		migrations.SQLVersionStep{ObjectName: "trig_widgets", Description: "create",
			Up: []string{"create table trig_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT"}},
		migrations.SQLVersionStep{ObjectName: "trig_log", Description: "create",
			Up: []string{"create table trig_log (id INTEGER PRIMARY KEY, widget_id INTEGER NOT NULL, note TEXT NOT NULL) STRICT"}},
	).Compose(v)
}

func (V1401) Version() int64 { return 1401 }

type V1402_CreateAudit struct {
	migrations.TriggerMigration[trigWidget, trigAudit]
}

func (V1402_CreateAudit) Action(a *migrations.TriggerActions) { a.CreateTrigger() }

type V1402 struct{}

func (V1402) Compose(v *migrations.VersionBuilder) { v.Apply(V1402_CreateAudit{}) }

type V1403_ChangeAudit struct {
	migrations.TriggerMigration[trigWidget, trigAudit]
}

func (V1403_ChangeAudit) Action(a *migrations.TriggerActions) {
	a.ExpectDefinition(core.TimingAfter, core.EventUpdate, trigV1Body)
	a.Recreate(core.TimingAfter, core.EventUpdate, trigV2Body)
}

type V1403 struct{}

func (V1403) Compose(v *migrations.VersionBuilder) { v.Apply(V1403_ChangeAudit{}) }

type V1404_DropAudit struct {
	migrations.TriggerMigration[trigWidget, trigAudit]
}

func (V1404_DropAudit) Action(a *migrations.TriggerActions) { a.DropTrigger() }

type V1404 struct{}

func (V1404) Compose(v *migrations.VersionBuilder) { v.Apply(V1404_DropAudit{}) }

// V1405 asks CreateTrigger of an entity that does not link the class.
type V1405_CreateUnlinked struct {
	migrations.TriggerMigration[trigWidgetPlain, trigAudit]
}

func (V1405_CreateUnlinked) Action(a *migrations.TriggerActions) { a.CreateTrigger() }

type V1405 struct{}

func (V1405) Compose(v *migrations.VersionBuilder) { v.Apply(V1405_CreateUnlinked{}) }

func triggerPlan(t *testing.T, versions ...migrations.Version) *migrations.Set {
	t.Helper()
	set, err := migrations.NewSet(versions...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// storedTrigger is the trigger's live statements, as stored.
func storedTrigger(t *testing.T, ctx context.Context, pool *sql.DB) []string {
	t.Helper()
	rows, err := migrations.ReadTriggerDefinitions(ctx, pool, sqlite.New(), "trg_trig_widgets")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func logNotes(t *testing.T, ctx context.Context, pool *sql.DB) []string {
	t.Helper()
	rows, err := pool.QueryContext(ctx, "select note from trig_log order by id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatal(err)
		}
		notes = append(notes, note)
	}
	return notes
}

func execStatements(t *testing.T, ctx context.Context, pool *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// triggerSnapshots writes the trigger's snapshot at a version into a temp snapshot directory.
func triggerSnapshots(t *testing.T, documents map[int64]*migrations.TriggerDeclaration) *migrations.SnapshotSet {
	t.Helper()
	dir := t.TempDir()
	for version, declaration := range documents {
		writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), fmt.Sprintf("V%04d.schema.json", version),
			migrations.ExportTrigger(declaration, version, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)))
	}
	set, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func declarationOf(t *testing.T, body string) *migrations.TriggerDeclaration {
	t.Helper()
	return &migrations.TriggerDeclaration{
		Name: "trg_trig_widgets", Relation: "trig_widgets", RelationKind: migrations.RelationKindTable,
		Timing: core.TimingAfter, Events: core.EventUpdate, SQL: body,
	}
}

func TestTriggerStep_CreateFiresAndTheDerivedRollbackDropsIt(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	snapshots := triggerSnapshots(t, map[int64]*migrations.TriggerDeclaration{1402: declarationOf(t, trigV1Body)})
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), triggerPlan(t, V1401{}, V1402{}), snapshots)

	if applied, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil || applied != 2 {
		t.Fatalf("migrate: %d, %v", applied, err)
	}
	// Replace semantics: the step is drop-then-create, and the created trigger fires.
	execStatements(t, ctx, pool, "insert into trig_widgets (id, name) values (1, 'a')", "update trig_widgets set name = 'b'")
	if got := logNotes(t, ctx, pool); !reflect.DeepEqual(got, []string{"v1"}) {
		t.Fatalf("the trigger fires once per update: %v", got)
	}

	if _, err := runner.MigrateDown(ctx, 1401, migrations.RunOptions{}); err != nil {
		t.Fatalf("down: %v", err)
	}
	if rows := storedTrigger(t, ctx, pool); len(rows) != 0 {
		t.Errorf("the creating version's derived rollback is the drop, but %v is stored", rows)
	}
}

func TestTriggerStep_ADriftedTriggerRefusesWithMIG012AndForceRecreatesWithNotice(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	first := migrations.NewRunner(pool, sqlite.New(), maps, triggerPlan(t, V1401{}, V1402{}), nil)
	if _, err := first.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	// The outside hotfix: someone rewrote the trigger by hand.
	execStatements(t, ctx, pool,
		"drop trigger trg_trig_widgets",
		"create trigger trg_trig_widgets after update on trig_widgets for each row begin insert into trig_log (widget_id, note) values (new.id, 'hotfix'); end")

	both := migrations.NewRunner(pool, sqlite.New(), maps, triggerPlan(t, V1401{}, V1402{}, V1403{}), nil)
	if _, err := both.Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-012" {
		t.Fatalf("expected MIG-012, got %v", err)
	}
	execStatements(t, ctx, pool, "insert into trig_widgets (id, name) values (1, 'a')", "update trig_widgets set name = 'b'")
	if got := logNotes(t, ctx, pool); !reflect.DeepEqual(got, []string{"hotfix"}) {
		t.Fatalf("the refused run leaves the hand-written trigger untouched: %v", got)
	}

	var notices []string
	if _, err := both.Migrate(ctx, migrations.RunOptions{AllowViewDrift: true, Notify: func(n string) { notices = append(notices, n) }}); err != nil {
		t.Fatalf("force: %v", err)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "recreating") {
		t.Errorf("force reports the drift: %v", notices)
	}
	execStatements(t, ctx, pool, "update trig_widgets set name = 'c'")
	if got := logNotes(t, ctx, pool); !reflect.DeepEqual(got, []string{"hotfix", "v2"}) {
		t.Errorf("force recreated the trigger from the code: %v", got)
	}
}

func TestTriggerStep_AGuardOverAMissingTriggerRefusesAndAMatchingOneApplies(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	maps := metadata.NewLoader(nil)

	// V1403's guard over a database that never had V1402's trigger: the trigger is absent.
	execStatements(t, ctx, pool,
		"create table trig_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT",
		"create table trig_log (id INTEGER PRIMARY KEY, widget_id INTEGER NOT NULL, note TEXT NOT NULL) STRICT")
	runner := migrations.NewRunner(pool, sqlite.New(), maps, triggerPlan(t, V1403{}), nil)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); core.CodeOf(err) != "MIG-012" {
		t.Fatalf("an absent trigger fails the guard: %v", err)
	}

	whole := migrations.NewRunner(pool, sqlite.New(), maps, triggerPlan(t, V1401{}, V1402{}, V1403{}), nil)
	execStatements(t, ctx, pool, "drop table trig_widgets", "drop table trig_log")
	if applied, err := whole.Migrate(ctx, migrations.RunOptions{}); err != nil || applied != 3 {
		t.Fatalf("the whole history applies, each guard finding what it expects: %d, %v", applied, err)
	}
}

func TestTriggerStep_TheDerivedRollbackOfAChangeRestoresThePreviousDeclaration(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	snapshots := triggerSnapshots(t, map[int64]*migrations.TriggerDeclaration{
		1402: declarationOf(t, trigV1Body), 1403: declarationOf(t, trigV2Body),
	})
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), triggerPlan(t, V1401{}, V1402{}, V1403{}), snapshots)

	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	execStatements(t, ctx, pool, "insert into trig_widgets (id, name) values (1, 'a')", "update trig_widgets set name = 'b'")
	if got := logNotes(t, ctx, pool); !reflect.DeepEqual(got, []string{"v2"}) {
		t.Fatalf("the changed trigger fires its new body: %v", got)
	}

	// The guard on this version's rendering passes (nothing was touched), the drop runs, and the previous
	// declaration is rendered through the dialect again.
	if _, err := runner.MigrateDown(ctx, 1402, migrations.RunOptions{}); err != nil {
		t.Fatalf("down to V1402: %v", err)
	}
	execStatements(t, ctx, pool, "update trig_widgets set name = 'c'")
	if got := logNotes(t, ctx, pool); !reflect.DeepEqual(got, []string{"v2", "v1"}) {
		t.Errorf("the derived rollback restored the previous declaration: %v", got)
	}
}

func TestTriggerStep_ARollbackOverAnOutsideHotfixRefusesWithMIG012(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	snapshots := triggerSnapshots(t, map[int64]*migrations.TriggerDeclaration{1402: declarationOf(t, trigV1Body)})
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil), triggerPlan(t, V1401{}, V1402{}), snapshots)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}

	execStatements(t, ctx, pool,
		"drop trigger trg_trig_widgets",
		"create trigger trg_trig_widgets after update on trig_widgets for each row begin select 1; end")
	if _, err := runner.MigrateDown(ctx, 1401, migrations.RunOptions{}); core.CodeOf(err) != "MIG-012" {
		t.Fatalf("a rollback must not silently destroy an outside hotfix either: %v", err)
	}
	if rows := storedTrigger(t, ctx, pool); len(rows) != 1 {
		t.Errorf("the refused rollback leaves the hotfix: %v", rows)
	}
}

func TestTriggerStep_DropTriggerRemovesItAndAStepWithoutASnapshotHasNoDerivedRollback(t *testing.T) {
	ctx := context.Background()
	pool := openMigrationsPool(t)
	snapshots := triggerSnapshots(t, map[int64]*migrations.TriggerDeclaration{1402: declarationOf(t, trigV1Body)})
	runner := migrations.NewRunner(pool, sqlite.New(), metadata.NewLoader(nil),
		triggerPlan(t, V1401{}, V1402{}, V1404{}), snapshots)

	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if rows := storedTrigger(t, ctx, pool); len(rows) != 0 {
		t.Fatalf("DropTrigger drops it, but %v is stored", rows)
	}
	// The removal wrote no snapshot of its own (an absent trigger has no declaration), so its rollback is not derivable.
	if _, err := runner.MigrateDown(ctx, 1402, migrations.RunOptions{}); core.CodeOf(err) != "MIG-020" {
		t.Errorf("expected MIG-020, got %v", err)
	}
}

func TestTriggerStep_CreateTriggerOfAnEntityThatDoesNotLinkTheClassIsDDL001(t *testing.T) {
	set := triggerPlan(t, V1405{})
	_, err := set.Render(metadata.NewLoader(nil), sqlite.New())
	if core.CodeOf(err) != "DDL-001" {
		t.Errorf("expected DDL-001 (schema creation that does not apply), got %v", err)
	}
}

func TestTriggerStep_AFormTheDialectLacksIsDDL005WhenTheStepRenders(t *testing.T) {
	version := migrations.NewSQLVersion(1,
		migrations.SQLVersionStep{ObjectName: "trg_x", Description: "create", Trigger: &migrations.TriggerDeclaration{
			Relation: "trig_widgets", Timing: core.TimingInsteadOf, Events: core.EventDelete, SQL: "select 1;",
		}})
	if _, err := triggerPlan(t, version).Render(metadata.NewLoader(nil), sqlite.New()); core.CodeOf(err) != "DDL-005" {
		t.Errorf("instead-of on a table is DDL-005 on SQLite, got %v", err)
	}
}

func TestTriggerSnapshot_ExportAndParseRoundTripAndNeverNamesADialect(t *testing.T) {
	declaration := &migrations.TriggerDeclaration{
		Name: "trg_x", Relation: "widgets", RelationKind: migrations.RelationKindView,
		Timing: core.TimingInsteadOf, Events: core.EventDelete | core.EventInsert, UpdateOf: nil,
		SQL: "insert into l\n  select n.id from {new:id} n;",
	}
	document := migrations.ExportTrigger(declaration, 7, time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	for _, fragment := range []string{`"kind": "trigger"`, `"relationKind": "view"`, `"timing": "instead_of"`, `"asOfVersion": 7`,
		`"sql": "insert into l select n.id from {new: id} n;"`} {
		if !strings.Contains(document, fragment) {
			t.Errorf("document lacks %s:\n%s", fragment, document)
		}
	}
	if strings.Contains(document, "updateOf") {
		t.Errorf("updateOf is exported only when declared:\n%s", document)
	}
	if order := strings.Index(document, `"insert"`); order < 0 || order > strings.Index(document, `"delete"`) {
		t.Errorf("events are in the fixed order:\n%s", document)
	}

	parsed, version, err := migrations.ParseTriggerSnapshot([]byte(document))
	if err != nil || version != 7 {
		t.Fatalf("parse: %v (version %d)", err, version)
	}
	if !parsed.SameAs(declaration) || parsed.Name != "trg_x" || !parsed.OnView() {
		t.Errorf("round trip: %+v", parsed)
	}

	declaration.UpdateOf = []string{"a", "b"}
	declaration.Events = core.EventUpdate
	withUpdateOf := migrations.ExportTrigger(declaration, 8, time.Now())
	if !strings.Contains(withUpdateOf, `"updateOf"`) {
		t.Errorf("a declared update-of list is exported:\n%s", withUpdateOf)
	}
}

func TestTriggerSnapshotSet_IndexesATriggerByNameAndVersionBesideTheOtherKinds(t *testing.T) {
	snapshots := triggerSnapshots(t, map[int64]*migrations.TriggerDeclaration{
		3: declarationOf(t, trigV1Body), 9: declarationOf(t, trigV2Body),
	})
	at := snapshots.At("TRG_TRIG_WIDGETS", 9)
	if at == nil || at.Trigger == nil || at.Table != nil || at.DDL != "" {
		t.Fatalf("At: %+v", at)
	}
	before := snapshots.LatestBefore("trg_trig_widgets", 9)
	if before == nil || before.Trigger == nil || before.Trigger.SQL != trigV1Body {
		t.Errorf("LatestBefore: %+v", before)
	}
	if snapshots.LatestBefore("trg_trig_widgets", 3) != nil {
		t.Error("the creating version has nothing before it")
	}
}

func TestNormalizeTriggerDDL_LowercasesTheLeadingKeywordsAndTheSetComparisonIgnoresOrder(t *testing.T) {
	if got := migrations.NormalizeTriggerDDL("CREATE  OR REPLACE\nFUNCTION \"f\"() RETURNS trigger"); got != `create or replace function "f"() RETURNS trigger` {
		t.Errorf("got %q", got)
	}
	if got := migrations.NormalizeTriggerDDL("CREATE TRIGGER t AFTER UPDATE"); got != "create trigger t AFTER UPDATE" {
		t.Errorf("got %q", got)
	}
	live := []string{"CREATE TRIGGER b x", "CREATE TRIGGER a  y"}
	expected := []string{"create trigger a y", "create trigger b x"}
	if !migrations.TriggerStatementsMatch(live, expected) {
		t.Error("the same set in another order matches")
	}
	if migrations.TriggerStatementsMatch(live[:1], expected) || migrations.TriggerStatementsMatch(nil, expected) {
		t.Error("a different set must not match")
	}
}

func TestDiffTrigger_NewChangedRemovedAndLayoutOnly(t *testing.T) {
	declared := declarationOf(t, trigV1Body)
	if d := migrations.DiffTrigger(declared, nil); !d.IsNew || d.IsChanged || d.IsRemoved {
		t.Errorf("new: %+v", d)
	}
	if d := migrations.DiffTrigger(nil, declared); !d.IsRemoved || d.HasChanges() == false {
		t.Errorf("removed: %+v", d)
	}
	if d := migrations.DiffTrigger(nil, nil); d.HasChanges() {
		t.Errorf("neither: %+v", d)
	}
	layout := declarationOf(t, "insert into trig_log (widget_id, note)\n  select n.id, 'v1'\n  from {new: id} n;")
	if d := migrations.DiffTrigger(layout, declared); d.HasChanges() {
		t.Errorf("a layout-only edit declares nothing: %+v", d)
	}
	for name, change := range map[string]func(*migrations.TriggerDeclaration){
		"relation": func(d *migrations.TriggerDeclaration) { d.Relation = "other" },
		"kind":     func(d *migrations.TriggerDeclaration) { d.RelationKind = migrations.RelationKindView },
		"timing":   func(d *migrations.TriggerDeclaration) { d.Timing = core.TimingBefore },
		"events":   func(d *migrations.TriggerDeclaration) { d.Events |= core.EventDelete },
		"updateOf": func(d *migrations.TriggerDeclaration) { d.UpdateOf = []string{"name"} },
		"body":     func(d *migrations.TriggerDeclaration) { d.SQL = trigV2Body },
	} {
		changed := declarationOf(t, trigV1Body)
		change(changed)
		if d := migrations.DiffTrigger(changed, declared); !d.IsChanged {
			t.Errorf("a different %s is a change", name)
		}
	}
}

// --- the generator --------------------------------------------------------------------------------------

func TestEmitTriggerStep_NewChangeAndRemoval(t *testing.T) {
	entity, class := reflect.TypeFor[trigWidget](), reflect.TypeFor[trigAudit]()
	current := declarationOf(t, "insert into l\nselect 1;")
	current.Events = core.EventUpdate | core.EventDelete
	current.UpdateOf = []string{"name"}
	previous := declarationOf(t, trigV1Body)

	created, err := migrations.EmitTriggerStep(entity, class, 14, "Auto", current, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"package trigaudit", "orm.TriggerMigration[migrations_test.trigWidget, migrations_test.trigAudit]",
		"func (V0014_Auto) Action(actions *orm.TriggerActions)",
		"actions.Recreate(orm.TimingAfter, orm.EventUpdate|orm.EventDelete, `insert into l\nselect 1;`, \"name\")",
	} {
		if !strings.Contains(created, fragment) {
			t.Errorf("a new trigger's step lacks %q:\n%s", fragment, created)
		}
	}
	if strings.Contains(created, "ExpectDefinition") || !migrations.IsGenerated(created) {
		t.Errorf("a new trigger has no guard and carries the generator's header:\n%s", created)
	}

	changed, err := migrations.EmitTriggerStep(entity, class, 15, "Auto", current, previous)
	if err != nil {
		t.Fatal(err)
	}
	if guard, recreate := strings.Index(changed, "actions.ExpectDefinition(orm.TimingAfter, orm.EventUpdate, `"+trigV1Body),
		strings.Index(changed, "actions.Recreate("); guard < 0 || recreate < guard {
		t.Errorf("a change guards the previous declaration, then recreates:\n%s", changed)
	}

	removed, err := migrations.EmitTriggerStep(entity, class, 16, "Auto", nil, previous)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed, "actions.DropTrigger()") || strings.Contains(removed, "Recreate") {
		t.Errorf("a removal is a bare DropTrigger:\n%s", removed)
	}
}

// diffFixture lays out a migrations directory with the table snapshot in place, so only the trigger can change.
func diffFixture(t *testing.T, entity reflect.Type) (dir string, options migrations.DiffOptions) {
	t.Helper()
	dir = testsupport.TempDir(t)
	dialect := sqlite.New()
	m, err := metadata.NewLoader(nil).Load(reflect.TypeFor[trigWidget]())
	if err != nil {
		t.Fatal(err)
	}
	writeSnapshotFile(t, filepath.Join(dir, "Table", entity.Name()), "V0001.schema.json",
		migrations.Export(m, dialect, 1, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)))
	return dir, migrations.DiffOptions{
		Set:          triggerPlan(t, migrations.NewSQLVersion(1)),
		EntityTypes:  []reflect.Type{entity},
		OutDir:       dir,
		Package:      "example.com/app/migrations",
		Dialect:      dialect,
		DialectLabel: "sqlite",
	}
}

func readTree(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
	return string(data)
}

func TestExecuteDiff_ANewTriggerIsAStepComposedAfterTheTables(t *testing.T) {
	dir, options := diffFixture(t, reflect.TypeFor[trigWidget]())
	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}

	step := readTree(t, filepath.Join(dir, "Trigger", "trigAudit", "V0002_Auto.go"))
	if !strings.Contains(step, "actions.Recreate(orm.TimingAfter, orm.EventUpdate,") || strings.Contains(step, "ExpectDefinition") {
		t.Errorf("a new trigger's step:\n%s", step)
	}
	root := readTree(t, filepath.Join(dir, "V0002.go"))
	if !strings.Contains(root, `"example.com/app/migrations/Trigger/trigAudit"`) || !strings.Contains(root, "trigaudit.V0002_Auto{}") {
		t.Errorf("the root composes the trigger step:\n%s", root)
	}
}

func TestExecuteDiff_AChangedTriggerGuardsThePreviousDeclarationAndANoOpEmitsNothing(t *testing.T) {
	dir, options := diffFixture(t, reflect.TypeFor[trigWidget]())

	stale := declarationOf(t, trigV2Body) // the snapshot says v2; the model declares v1
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json",
		migrations.ExportTrigger(stale, 1, time.Now()))
	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	step := readTree(t, filepath.Join(dir, "Trigger", "trigAudit", "V0002_Auto.go"))
	if !strings.Contains(step, "actions.ExpectDefinition(orm.TimingAfter, orm.EventUpdate, `"+trigV2Body) ||
		!strings.Contains(step, "actions.Recreate(orm.TimingAfter, orm.EventUpdate, `"+trigV1Body) {
		t.Errorf("a change guards the snapshot's declaration and recreates the model's:\n%s", step)
	}

	// The snapshot now agrees with the model: nothing to emit.
	dir, options = diffFixture(t, reflect.TypeFor[trigWidget]())
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json",
		migrations.ExportTrigger(declarationOf(t, trigV1Body), 1, time.Now()))
	stdout.Reset()
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no schema changes") {
		t.Errorf("code %d: %s", code, stdout.String())
	}
}

func TestExecuteDiff_ARemovedTriggerNeedsAllowRemoveAndItsClassAndEntityStillPresent(t *testing.T) {
	dir, options := diffFixture(t, reflect.TypeFor[trigWidgetPlain]())
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json",
		migrations.ExportTrigger(declarationOf(t, trigV1Body), 1, time.Now()))
	options.TriggerTypes = []reflect.Type{reflect.TypeFor[trigAudit]()}

	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "DDL-003") ||
		!strings.Contains(stderr.String(), "trigger trg_trig_widgets") {
		t.Fatalf("a removal is gated like a column's (DDL-003): code %d, %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "V0002.go")); err == nil {
		t.Error("a refused diff writes nothing")
	}

	options.AllowRemove = true
	stderr.Reset()
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	step := readTree(t, filepath.Join(dir, "Trigger", "trigAudit", "V0002_Auto.go"))
	if !strings.Contains(step, "actions.DropTrigger()") {
		t.Errorf("a removal is a bare DropTrigger:\n%s", step)
	}

	// Without the class anywhere (registry or steps) the generator cannot write the step, and refuses.
	dir, options = diffFixture(t, reflect.TypeFor[trigWidgetPlain]())
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json",
		migrations.ExportTrigger(declarationOf(t, trigV1Body), 1, time.Now()))
	stderr.Reset()
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "class trigAudit no longer exists") {
		t.Errorf("code %d: %s", code, stderr.String())
	}
}

// A removal leaves no snapshot of its own: a trigger step newer than the newest snapshot is the removal already
// emitted, never emitted twice (spec/migrations.md "The generator toolchain").
func TestExecuteDiff_AnEmittedRemovalIsNeverEmittedTwice(t *testing.T) {
	dir, options := diffFixture(t, reflect.TypeFor[trigWidgetPlain]())
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json",
		migrations.ExportTrigger(declarationOf(t, trigV1Body), 1, time.Now()))
	options.Set = triggerPlan(t, migrations.NewSQLVersion(1), V1404{})
	options.AllowRemove = true

	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "no schema changes") {
		t.Errorf("code %d: %s %s", code, stdout.String(), stderr.String())
	}
}

// A trigger with migration history and no snapshot below the version refuses instead of coming out as new.
func TestExecuteDiff_AMigratedTriggerWithoutASnapshotRefuses(t *testing.T) {
	_, options := diffFixture(t, reflect.TypeFor[trigWidget]())
	options.Set = triggerPlan(t, migrations.NewSQLVersion(1), V1402{})

	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "no recorded schema below V1403 for trg_trig_widgets") {
		t.Errorf("code %d: %s", code, stderr.String())
	}
}

// A declaration moved to another relation is DDL-004: the generated guard and drop would target the new relation.
func TestExecuteDiff_ATriggerMovedToAnotherRelationIsDDL004(t *testing.T) {
	dir, options := diffFixture(t, reflect.TypeFor[trigWidget]())
	moved := declarationOf(t, trigV1Body)
	moved.Relation = "other_widgets"
	writeSnapshotFile(t, filepath.Join(dir, "Trigger", "trigAudit"), "V0001.schema.json", migrations.ExportTrigger(moved, 1, time.Now()))

	var stdout, stderr bytes.Buffer
	if code := migrations.ExecuteDiff(context.Background(), options, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "DDL-004 trigger trg_trig_widgets: moved from other_widgets") {
		t.Errorf("code %d: %s", code, stderr.String())
	}
}

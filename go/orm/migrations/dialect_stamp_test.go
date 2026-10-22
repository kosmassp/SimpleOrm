package migrations_test

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// Snapshot format v3 (ADR-0043, spec/migrations.md "Dialects"): the dialect stamp every non-SQLite snapshot
// carries, MIG-014 on every reader, the neutral view normalizer, and the derived rollback rendered through the
// seam in the contract's order.

var stampTime = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func TestExport_StampsEverySnapshotKindRightAfterObjectExceptSqlites(t *testing.T) {
	schema := &migrations.TableSchema{Name: "widgets", Dialect: "postgres", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "bigint", Key: true, Generated: true},
	}}
	declaration := &migrations.TriggerDeclaration{
		Dialect: "sqlserver", Name: "trg", Relation: "widgets", RelationKind: migrations.RelationKindTable,
		Timing: core.TimingAfter, Events: core.EventUpdate, SQL: "select 1;",
	}
	for _, c := range []struct{ what, document, stamp string }{
		{"table", migrations.ExportSchema(schema, 1, stampTime), "postgres"},
		{"view", migrations.ExportDDL("v", "sqlserver", "view", "create view v as select 1", 1, stampTime), "sqlserver"},
		{"trigger", migrations.ExportTrigger(declaration, 1, stampTime), "sqlserver"},
	} {
		lines := strings.Split(c.document, "\n")
		if len(lines) < 3 || !strings.Contains(lines[1], `"object"`) || strings.TrimSpace(lines[2]) != `"dialect": "`+c.stamp+`",` {
			t.Errorf("%s: the stamp should follow the object line:\n%s", c.what, c.document)
		}
	}

	// SQLite's documents are unstamped (format v2 compatible): nothing on disk changes for a SQLite tree.
	schema.Dialect = "sqlite"
	declaration.Dialect = ""
	for what, document := range map[string]string{
		"table":   migrations.ExportSchema(schema, 1, stampTime),
		"view":    migrations.ExportDDL("v", "sqlite", "view", "create view v as select 1", 1, stampTime),
		"trigger": migrations.ExportTrigger(declaration, 1, stampTime),
	} {
		if strings.Contains(document, `"dialect"`) {
			t.Errorf("%s: a SQLite document carries no stamp:\n%s", what, document)
		}
	}
}

func TestParse_ReadsTheStampBack(t *testing.T) {
	schema := &migrations.TableSchema{Name: "widgets", Dialect: "postgres", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "bigint", Key: true, Generated: true},
	}}
	parsed, _, err := migrations.ParseTable([]byte(migrations.ExportSchema(schema, 1, stampTime)))
	if err != nil || parsed.Dialect != "postgres" {
		t.Errorf("table stamp: %q, %v", parsed.Dialect, err)
	}
	ddl, err := migrations.ParseDDLSnapshot([]byte(migrations.ExportDDL("v", "sqlserver", "view", "create view v as select 1", 1, stampTime)))
	if err != nil || ddl.Dialect != "sqlserver" {
		t.Errorf("view stamp: %+v, %v", ddl, err)
	}
	unstamped, err := migrations.ParseDDLSnapshot([]byte(migrations.ExportDDL("v", "sqlite", "view", "create view v as select 1", 1, stampTime)))
	if err != nil || unstamped.Dialect != "" {
		t.Errorf("an unstamped document reads back with an empty stamp: %+v, %v", unstamped, err)
	}
}

func TestCheckSnapshotDialect_AnUnstampedDocumentIsSqlitesAndAForeignOneIsMIG014(t *testing.T) {
	if err := migrations.CheckSnapshotDialect("", sqlite.New(), "widgets"); err != nil {
		t.Errorf("an unstamped snapshot is SQLite's: %v", err)
	}
	if err := migrations.CheckSnapshotDialect("PostGres", postgres.New(), "widgets"); err != nil {
		t.Errorf("the stamp compares case-insensitively: %v", err)
	}
	err := migrations.CheckSnapshotDialect("postgres", sqlite.New(), "widgets")
	if core.CodeOf(err) != "MIG-014" || !strings.Contains(err.Error(), "widgets") {
		t.Errorf("a snapshot of another dialect is MIG-014 naming the object, got %v", err)
	}
	if core.CodeOf(migrations.CheckSnapshotDialect("", sqlserver.New(), "widgets")) != "MIG-014" {
		t.Error("an unstamped snapshot under SQL Server is MIG-014")
	}
}

func TestViewParts_SplitsAnyDialectsPrefixIntoNameBodyAndKind(t *testing.T) {
	for _, c := range []struct {
		ddl, name, body string
		materialized    bool
	}{
		{"create view totals as select 1", "totals", "select 1", false},
		{"CREATE OR ALTER VIEW [dbo].[Totals] AS\nselect 1", "dbo.Totals", "select 1", false},
		{`create or replace view "totals" as select 1`, "totals", "select 1", false},
		{"create materialized view if not exists `mv` as select  1", "mv", "select 1", true},
	} {
		name, body, materialized, ok := migrations.ViewParts(c.ddl)
		if !ok || name != c.name || body != c.body || materialized != c.materialized {
			t.Errorf("ViewParts(%q) = %q, %q, %v, %v", c.ddl, name, body, materialized, ok)
		}
	}
	if _, _, _, ok := migrations.ViewParts("alter table t add column c TEXT"); ok {
		t.Error("text that is no view create is not a view")
	}
}

// --- the derived rollback ---------------------------------------------------------

func snapshotSetOf(t *testing.T, documents ...string) *migrations.SnapshotSet {
	t.Helper()
	dir := t.TempDir()
	for i, document := range documents {
		writeSnapshotFile(t, dir, "s"+string(rune('0'+i))+".schema.json", document)
	}
	set, err := migrations.SnapshotsFromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func widgetSnapshots(dialectName string, withNote bool) (v1, v2 *migrations.TableSchema) {
	id := migrations.TableColumn{Name: "id", StorageType: "bigint", Key: true, Generated: true}
	name := migrations.TableColumn{Name: "name", StorageType: "text"}
	note := migrations.TableColumn{Name: "note", StorageType: "text", Nullable: true}
	v1 = &migrations.TableSchema{Name: "widgets", Dialect: dialectName, Columns: []migrations.TableColumn{id, name}}
	v2 = &migrations.TableSchema{
		Name: "widgets", Dialect: dialectName, Columns: []migrations.TableColumn{id, name, note},
		Indexes: []migrations.TableIndex{{Name: "ix_widgets_note", Columns: []migrations.IndexPart{{ColumnName: "note"}}}},
	}
	if !withNote {
		v2.Columns = v2.Columns[:2]
	}
	return v1, v2
}

func TestDeriveDown_RefusesASnapshotOfAnotherDialectOnEitherSide(t *testing.T) {
	v1, v2 := widgetSnapshots("postgres", true)
	// The version's own snapshot is the foreign one.
	set := snapshotSetOf(t,
		migrations.ExportSchema(&migrations.TableSchema{Name: "widgets", Columns: v1.Columns}, 1, stampTime),
		migrations.ExportSchema(v2, 2, stampTime))
	var notices []string
	_, err := migrations.DeriveDown("widgets", 2, set, nil, &notices, sqlite.New())
	if core.CodeOf(err) != "MIG-014" {
		t.Errorf("a stamped snapshot at the version under SQLite: want MIG-014, got %v", err)
	}

	// The snapshot before it is the foreign one.
	set = snapshotSetOf(t,
		migrations.ExportSchema(v1, 1, stampTime),
		migrations.ExportSchema(&migrations.TableSchema{Name: "widgets", Columns: v2.Columns, Indexes: v2.Indexes}, 2, stampTime))
	_, err = migrations.DeriveDown("widgets", 2, set, nil, &notices, sqlite.New())
	if core.CodeOf(err) != "MIG-014" {
		t.Errorf("a stamped snapshot before the version under SQLite: want MIG-014, got %v", err)
	}
}

func TestDeriveDown_DropsTheVersionsIndexesBeforeTheColumnsAndRendersThroughTheDialect(t *testing.T) {
	v1, v2 := widgetSnapshots("postgres", true)
	set := snapshotSetOf(t, migrations.ExportSchema(v1, 1, stampTime), migrations.ExportSchema(v2, 2, stampTime))

	var notices []string
	statements, err := migrations.DeriveDown("widgets", 2, set, nil, &notices, postgres.New())
	if err != nil {
		t.Fatal(err)
	}
	var sqlTexts []string
	for _, s := range statements {
		sqlTexts = append(sqlTexts, s.SQL)
	}
	want := []string{`drop index "ix_widgets_note"`, `alter table "widgets" drop column "note"`}
	if !reflect.DeepEqual(sqlTexts, want) {
		t.Errorf("derived statements = %q, want %q", sqlTexts, want)
	}

	// The restore side of the same contract: the previous version's index comes back after its column.
	set = snapshotSetOf(t, migrations.ExportSchema(v2, 1, stampTime), migrations.ExportSchema(v1, 2, stampTime))
	statements, err = migrations.DeriveDown("widgets", 2, set, nil, &notices, postgres.New())
	if err != nil {
		t.Fatal(err)
	}
	sqlTexts = nil
	for _, s := range statements {
		sqlTexts = append(sqlTexts, s.SQL)
	}
	want = []string{
		`alter table "widgets" add column "note" text`,
		`create index if not exists "ix_widgets_note" on "widgets" ("note")`,
	}
	if !reflect.DeepEqual(sqlTexts, want) {
		t.Errorf("derived restore statements = %q, want %q", sqlTexts, want)
	}
}

func TestDeriveDown_ViewRollbackRendersTheDropAndRestoreThroughTheDialect(t *testing.T) {
	set := snapshotSetOf(t,
		migrations.ExportDDL("totals", "sqlserver", "view", "create view totals as select 1 as answer", 1, stampTime),
		migrations.ExportDDL("totals", "sqlserver", "view", "create or alter view [totals] as select 2 as answer", 2, stampTime))

	var notices []string
	statements, err := migrations.DeriveDown("totals", 2, set, nil, &notices, sqlserver.New())
	if err != nil {
		t.Fatal(err)
	}
	var sqlTexts []string
	for _, s := range statements {
		sqlTexts = append(sqlTexts, s.SQL)
	}
	want := []string{
		"create view totals as select 2 as answer", // the MIG-012 guard's expectation, in the neutral form
		"drop view if exists [totals]",
		"create or alter view [totals] as\nselect 1 as answer",
	}
	if !reflect.DeepEqual(sqlTexts, want) {
		t.Errorf("derived statements = %q, want %q", sqlTexts, want)
	}
	if statements[0].GuardView != "totals" {
		t.Errorf("the first statement should be the view guard, got %+v", statements[0])
	}
}

// --- the diff baseline and the shadow's trusted baseline ------------------------------

type StampWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (StampWidget) Entity() core.EntityDef {
	return core.EntityDef{Source: core.Table("stamp_widgets")}
}

func TestExecuteDiff_RefusesABaselineStampedForAnotherDialect(t *testing.T) {
	dir := t.TempDir()
	schema := &migrations.TableSchema{Name: "stamp_widgets", Dialect: "postgres", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "bigint", Key: true, Generated: true},
	}}
	writeSnapshotFile(t, filepath.Join(dir, "Table", "StampWidget"), "V0001.schema.json", migrations.ExportSchema(schema, 1, stampTime))
	set, err := migrations.NewSet(diffBumpRoot{version: 1})
	if err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	exit := migrations.ExecuteDiff(context.Background(), migrations.DiffOptions{
		Set: set, EntityTypes: []reflect.Type{reflect.TypeFor[StampWidget]()}, OutDir: dir,
		Package: "example.com/migrations", Dialect: sqlite.New(), DialectLabel: "sqlite",
	}, &stdout, &stderr)
	if exit == 0 || !strings.Contains(stderr.String(), "MIG-014") {
		t.Fatalf("a postgres-stamped baseline under sqlite should refuse with MIG-014 (exit %d): %s", exit, stderr.String())
	}
}

func TestShadow_TheTrustedBaselineRefusesASnapshotOfAnotherDialect(t *testing.T) {
	dir := t.TempDir()
	schema := &migrations.TableSchema{Name: "stamp_widgets", Dialect: "postgres", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "bigint", Key: true, Generated: true},
	}}
	writeSnapshotFile(t, filepath.Join(dir, "Table", "StampWidget"), "V0001.schema.json", migrations.ExportSchema(schema, 1, stampTime))

	set, err := migrations.NewSet(
		migrations.NewSQLVersion(1, migrations.SQLVersionStep{
			ObjectName: "stamp_widgets", Description: "create", Up: []string{"create table stamp_widgets (id INTEGER PRIMARY KEY) STRICT"},
		}),
		migrations.NewSQLVersion(2, migrations.SQLVersionStep{
			ObjectName: "stamp_widgets", Description: "add", Up: []string{"alter table stamp_widgets add column name TEXT"},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: set, Entities: []reflect.Type{reflect.TypeFor[StampWidget]()}, OutDir: dir, From: 1,
	})
	if core.CodeOf(err) != "MIG-014" {
		t.Fatalf("a --from baseline stamped for another dialect should refuse with MIG-014, got %v", err)
	}
}

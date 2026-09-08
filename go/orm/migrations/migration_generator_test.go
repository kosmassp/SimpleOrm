package migrations_test

import (
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func names(specs []migrations.ColumnSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	sort.Strings(out)
	return out
}

func TestDiffSchemas_NewTable(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "name", StorageType: "TEXT"},
	}}
	diff := migrations.DiffSchemas(current, nil, nil)
	if !diff.IsNew {
		t.Fatal("expected IsNew")
	}
	if !diff.HasChanges() {
		t.Fatal("expected HasChanges")
	}
}

func TestDiffSchemas_AddNullableColumn(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "name", StorageType: "TEXT"},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "name", StorageType: "TEXT"},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if got := names(diff.Added); len(got) != 1 || got[0] != "note" {
		t.Fatalf("expected [note], got %v", got)
	}
}

func TestDiffSchemas_AddNotNullIsUnsupported(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "name", StorageType: "TEXT"},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if len(diff.Added) != 0 {
		t.Fatalf("a NOT NULL addition must not be Added: %v", diff.Added)
	}
	if len(diff.Unsupported) != 1 || !strings.Contains(diff.Unsupported[0], "name") {
		t.Fatalf("expected an unsupported entry naming 'name', got %v", diff.Unsupported)
	}
}

func TestDiffSchemas_RemoveColumn(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "legacy", StorageType: "TEXT", Nullable: true},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if got := names(diff.Removed); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("expected [legacy], got %v", got)
	}
}

func TestDiffSchemas_DeclaredRename(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
	}}
	diff := migrations.DiffSchemas(current, snapshot, map[string]string{"remark": "note"})
	if len(diff.Renamed) != 1 || diff.Renamed[0] != (migrations.ColumnRename{From: "remark", To: "note"}) {
		t.Fatalf("expected the declared rename, got %+v", diff.Renamed)
	}
	if len(diff.Added) != 0 || len(diff.Removed) != 0 {
		t.Fatalf("a declared rename must not also read as add+remove: %+v / %+v", diff.Added, diff.Removed)
	}
}

func TestDiffSchemas_UndeclaredRenameIsAddRemove(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "note", StorageType: "TEXT", Nullable: true},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "remark", StorageType: "TEXT", Nullable: true},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if got := names(diff.Added); len(got) != 1 || got[0] != "note" {
		t.Fatalf("expected added=[note], got %v", got)
	}
	if got := names(diff.Removed); len(got) != 1 || got[0] != "remark" {
		t.Fatalf("expected removed=[remark], got %v", got)
	}
}

func TestDiffSchemas_TypeChangeIsUnsupported(t *testing.T) {
	current := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "price", StorageType: "INTEGER"},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true},
		{Name: "price", StorageType: "TEXT"},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if len(diff.Unsupported) != 1 || !strings.Contains(diff.Unsupported[0], "price") {
		t.Fatalf("expected an unsupported entry naming 'price', got %v", diff.Unsupported)
	}
}

func TestDiffSchemas_IndexMatchesStructurallyAcrossNames(t *testing.T) {
	current := &migrations.TableSchema{
		Name: "widgets",
		Columns: []migrations.TableColumn{
			{Name: "id", StorageType: "INTEGER", Key: true, Generated: true}, {Name: "name", StorageType: "TEXT"},
		},
		Indexes: []migrations.TableIndex{
			{Name: "ix_widgets_name", Columns: []migrations.IndexPart{{ColumnName: "name"}}},
		},
	}
	snapshot := &migrations.TableSchema{
		Name:    "widgets",
		Columns: current.Columns,
		Indexes: []migrations.TableIndex{
			{Name: "idx_dba_hotfix", Columns: []migrations.IndexPart{{ColumnName: "name"}}},
		},
	}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if len(diff.AddedIndexSQL) != 0 || len(diff.RemovedIndexNames) != 0 {
		t.Fatalf("a structurally-identical index under another name must count as implemented: %+v", diff)
	}
}

func TestDiffSchemas_IndexUniqueMismatchAddsAndRemoves(t *testing.T) {
	columns := []migrations.TableColumn{
		{Name: "id", StorageType: "INTEGER", Key: true, Generated: true}, {Name: "name", StorageType: "TEXT"},
	}
	current := &migrations.TableSchema{Name: "widgets", Columns: columns, Indexes: []migrations.TableIndex{
		{Name: "ix_widgets_name", Columns: []migrations.IndexPart{{ColumnName: "name"}}},
	}}
	snapshot := &migrations.TableSchema{Name: "widgets", Columns: columns, Indexes: []migrations.TableIndex{
		{Name: "idx_dba_hotfix", Columns: []migrations.IndexPart{{ColumnName: "name"}}, Unique: true},
	}}
	diff := migrations.DiffSchemas(current, snapshot, nil)
	if len(diff.AddedIndexSQL) != 1 || !strings.Contains(diff.AddedIndexSQL[0], "ix_widgets_name") {
		t.Fatalf("expected the model's index to be added: %v", diff.AddedIndexSQL)
	}
	if len(diff.RemovedIndexNames) != 1 || diff.RemovedIndexNames[0] != "idx_dba_hotfix" {
		t.Fatalf("expected the stranger index to be removed: %v", diff.RemovedIndexNames)
	}
}

// --- Go-source emitters --------------------------------------------------

func mustParse(t *testing.T, source string) {
	t.Helper()
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", source, parser.AllErrors); err != nil {
		t.Fatalf("generated source does not parse: %v\n%s", err, source)
	}
}

func TestEmitTableStep_NewTable(t *testing.T) {
	m := widgetMap()
	diff := migrations.DiffSchemas(migrations.FromMap(m, sqlite.New()), nil, nil)
	source, err := migrations.EmitTableStep(m, sqlite.New(), 1, "CreateWidget", diff)
	if err != nil {
		t.Fatal(err)
	}
	mustParse(t, source)
	for _, want := range []string{
		migrations.GeneratedMarker,
		"package widget",
		"type V0001_CreateWidget struct",
		"orm.TableMigration[",
		"func (V0001_CreateWidget) Action(actions *orm.TableActions)",
		"create table if not exists widgets",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected generated source to contain %q:\n%s", want, source)
		}
	}
}

func TestEmitTableStep_AddRenameRemove(t *testing.T) {
	m := widgetMap()
	diff := &migrations.TableDiff{
		Added:             []migrations.ColumnSpec{{Name: "note", StorageType: "TEXT", Nullable: true}},
		Removed:           []migrations.ColumnSpec{{Name: "legacy", StorageType: "TEXT"}},
		Renamed:           []migrations.ColumnRename{{From: "old_name", To: "name"}},
		RemovedIndexNames: []string{"idx_dba_hotfix"},
	}
	source, err := migrations.EmitTableStep(m, sqlite.New(), 2, "Reshape", diff)
	if err != nil {
		t.Fatal(err)
	}
	mustParse(t, source)
	for _, want := range []string{
		`actions.RenameColumn("old_name", "name")`,
		`actions.AddColumn("note", "TEXT")`,
		`actions.RemoveColumn("legacy")`,
		`actions.DropIndex("idx_dba_hotfix")`,
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected generated source to contain %q:\n%s", want, source)
		}
	}
}

func TestEmitViewStep_NewView(t *testing.T) {
	m := widgetTotalMap()
	source, err := migrations.EmitViewStep(
		m.Type, false, m.RelationName, 1, "CreateTotals", sqlite.New().CreateViewSQL(m), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustParse(t, source)
	for _, want := range []string{
		migrations.GeneratedMarker,
		"package widgettotal",
		"type V0001_CreateTotals struct",
		"orm.ViewMigration[",
		"func (V0001_CreateTotals) Action(actions *orm.ViewActions)",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected generated source to contain %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "ExpectDefinition") {
		t.Errorf("a new view must not carry a guard:\n%s", source)
	}
}

func TestEmitViewStep_ChangeCarriesGuard(t *testing.T) {
	m := widgetTotalMap()
	previous := "create view widget_totals as select 0"
	source, err := migrations.EmitViewStep(
		m.Type, false, m.RelationName, 2, "AddColumn", sqlite.New().CreateViewSQL(m), &previous)
	if err != nil {
		t.Fatal(err)
	}
	mustParse(t, source)
	if !strings.Contains(source, "actions.ExpectDefinition(") {
		t.Errorf("expected an ExpectDefinition guard:\n%s", source)
	}
	if !strings.Contains(source, "drop view if exists widget_totals") {
		t.Errorf("expected a drop before the recreate:\n%s", source)
	}
}

func TestEmitRoot_ComposesStepsInOrder(t *testing.T) {
	source, err := migrations.EmitRoot(
		"github.com/kosmassp/SimpleOrm/go/orm/sample/migrations", 1,
		[]migrations.RootStepRef{
			{ImportPath: ".../Table/User", Package: "user", TypeName: "V0001_CreateUsers"},
			{ImportPath: ".../Table/Role", Package: "role", TypeName: "V0001_CreateRoles"},
		},
		"sqlite")
	if err != nil {
		t.Fatal(err)
	}
	mustParse(t, source)
	for _, want := range []string{
		"Generated by simpleorm diff (ADR-0017, dialect sqlite)",
		"package migrations",
		"type V0001 struct{}",
		"func (V0001) Compose(version *orm.VersionBuilder)",
		"user.V0001_CreateUsers{}",
		"role.V0001_CreateRoles{}",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("expected generated root to contain %q:\n%s", want, source)
		}
	}
	// The user.V0001_CreateUsers{} step must be applied before role.V0001_CreateRoles{}.
	if strings.Index(source, "user.V0001_CreateUsers{}") > strings.Index(source, "role.V0001_CreateRoles{}") {
		t.Errorf("expected steps to compose in declaration order:\n%s", source)
	}
}

func TestIsGenerated_AndDialectLabel(t *testing.T) {
	if migrations.IsGenerated("package foo\n// hand-written") {
		t.Error("hand-written source must not report as generated")
	}
	if !migrations.IsGenerated("// " + migrations.GeneratedMarker + "\npackage foo") {
		t.Error("a source with the marker must report as generated")
	}
	if label := migrations.GeneratedDialectLabel("// Generated by simpleorm diff (ADR-0017, dialect sqlite); more"); label != "sqlite" {
		t.Errorf("expected 'sqlite', got %q", label)
	}
	if label := migrations.GeneratedDialectLabel("// " + migrations.GeneratedMarker); label != "" {
		t.Errorf("expected \"\" for an unstamped generated file, got %q", label)
	}
}

func TestIndexSignature_UniqueAndDirectionMatter(t *testing.T) {
	a := migrations.TableIndex{Columns: []migrations.IndexPart{{ColumnName: "Name"}}}
	b := migrations.TableIndex{Columns: []migrations.IndexPart{{ColumnName: "name"}}}
	if migrations.IndexSignature(a) != migrations.IndexSignature(b) {
		t.Error("index signatures should be case-insensitive on the column name")
	}
	unique := migrations.TableIndex{Columns: []migrations.IndexPart{{ColumnName: "name"}}, Unique: true}
	if migrations.IndexSignature(a) == migrations.IndexSignature(unique) {
		t.Error("unique must be part of the signature")
	}
	desc := migrations.TableIndex{Columns: []migrations.IndexPart{{ColumnName: "name", Descending: true}}}
	if migrations.IndexSignature(a) == migrations.IndexSignature(desc) {
		t.Error("direction must be part of the signature")
	}
}

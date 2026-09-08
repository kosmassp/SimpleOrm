package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// V0006_ExecutionOrder declares raw SQL, a remove, an add, and a rename — out
// of execution order — to prove Set/TableActions reorders to
// rename -> add -> remove -> raw regardless of declaration order (§7.22).
type V0006_ExecutionOrder struct {
	migrations.TableMigration[Widget]
}

func (V0006_ExecutionOrder) Action(a *migrations.TableActions) {
	a.SQL("update widgets set name = name")
	a.RemoveColumn("legacy")
	a.AddColumn("note", "TEXT")
	a.RenameColumn("old_name", "name")
}

type V0006 struct{}

func (V0006) Compose(v *migrations.VersionBuilder) { v.Apply(V0006_ExecutionOrder{}) }

func TestTableActions_ExecutionOrderIsRenameAddRemoveRaw(t *testing.T) {
	set, err := migrations.NewSet(V0006{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	up := rendered[0].Steps[0].Up
	if len(up) != 4 {
		t.Fatalf("expected 4 statements, got %d: %+v", len(up), up)
	}
	wantOrder := []string{
		"alter table widgets rename column old_name to name",
		"alter table widgets add column note TEXT",
		"alter table widgets drop column legacy",
		"update widgets set name = name",
	}
	for i, want := range wantOrder {
		if up[i].SQL != want {
			t.Errorf("statement %d: want %q, got %q", i, want, up[i].SQL)
		}
	}
}

// V0007_CreateWithHooks exercises Pre/Post hooks and CreateTable rendering
// through the real SQLite dialect from a hand-built map.
type V0007_CreateWithHooks struct {
	migrations.TableMigration[Widget]
}

func (V0007_CreateWithHooks) Action(a *migrations.TableActions) {
	a.CreateTable().Post("insert into widgets (name) values ('seed')")
	a.AddColumn("note", "TEXT").Pre("select 1").Post("select 2")
}

type V0007 struct{}

func (V0007) Compose(v *migrations.VersionBuilder) { v.Apply(V0007_CreateWithHooks{}) }

func TestTableActions_CreateTableRendersThroughDialectWithHooks(t *testing.T) {
	set, err := migrations.NewSet(V0007{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	up := rendered[0].Steps[0].Up
	if len(up) != 5 {
		t.Fatalf("expected 5 statements (create, post-create, pre-add, add, post-add), got %d: %+v", len(up), up)
	}
	wantCreate := "create table if not exists widgets (\n    id INTEGER PRIMARY KEY,\n    name TEXT NOT NULL\n) STRICT"
	if up[0].SQL != wantCreate {
		t.Errorf("create table SQL:\n%s\nwant:\n%s", up[0].SQL, wantCreate)
	}
	if up[0].Origin != "create widgets" {
		t.Errorf("create origin: %q", up[0].Origin)
	}
	if up[1].SQL != "insert into widgets (name) values ('seed')" || up[1].Origin != "create widgets post" {
		t.Errorf("post-create hook: %+v", up[1])
	}
	if up[2].SQL != "select 1" || up[2].Origin != "add widgets.note pre" {
		t.Errorf("pre-add hook: %+v", up[2])
	}
	if up[3].SQL != "alter table widgets add column note TEXT" {
		t.Errorf("add column: %+v", up[3])
	}
	if up[4].SQL != "select 2" || up[4].Origin != "add widgets.note post" {
		t.Errorf("post-add hook: %+v", up[4])
	}
}

// V0008_NotNullDefault exercises the AddColumn(NotNull(default)) option.
type V0008_NotNullDefault struct {
	migrations.TableMigration[Widget]
}

func (V0008_NotNullDefault) Action(a *migrations.TableActions) {
	a.AddColumn("count", "INTEGER", migrations.NotNull("0"))
}

type V0008 struct{}

func (V0008) Compose(v *migrations.VersionBuilder) { v.Apply(V0008_NotNullDefault{}) }

func TestTableActions_AddColumnNotNullWithDefault(t *testing.T) {
	set, err := migrations.NewSet(V0008{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	want := "alter table widgets add column count INTEGER not null default 0"
	if got := rendered[0].Steps[0].Up[0].SQL; got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// V0009_Renames declares two renames — recorded structurally for a future derived rollback.
type V0009_Renames struct {
	migrations.TableMigration[Widget]
}

func (V0009_Renames) Action(a *migrations.TableActions) {
	a.RenameColumn("a", "b")
	a.RenameColumn("c", "d")
}

type V0009 struct{}

func (V0009) Compose(v *migrations.VersionBuilder) { v.Apply(V0009_Renames{}) }

func TestTableActions_ColumnRenamesAreRecordedStructurally(t *testing.T) {
	set, err := migrations.NewSet(V0009{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	want := []migrations.ColumnRename{{From: "a", To: "b"}, {From: "c", To: "d"}}
	got := rendered[0].Steps[0].Renames
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("want %+v, got %+v", want, got)
	}
}

// V0010_NotTable targets a view-backed entity through TableMigration -> DDL-001.
type V0010_NotTable struct {
	migrations.TableMigration[WidgetTotal]
}

func (V0010_NotTable) Action(a *migrations.TableActions) { a.SQL("select 1") }

type V0010 struct{}

func (V0010) Compose(v *migrations.VersionBuilder) { v.Apply(V0010_NotTable{}) }

func TestTableActions_ViewBackedEntityIsDDL001(t *testing.T) {
	set, err := migrations.NewSet(V0010{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.Render(testLoader(), sqlite.New())
	if core.CodeOf(err) != "DDL-001" {
		t.Fatalf("expected DDL-001, got %v", err)
	}
}

// V0011_DropTable / DropIndex exercise the remaining literal actions.
type V0011_DropStuff struct {
	migrations.TableMigration[Widget]
}

func (V0011_DropStuff) Action(a *migrations.TableActions) {
	a.DropIndex("ix_widgets_name")
	a.DropTable()
	a.RenameTable("old_widgets")
}

type V0011 struct{}

func (V0011) Compose(v *migrations.VersionBuilder) { v.Apply(V0011_DropStuff{}) }

func TestTableActions_DropTableDropIndexRenameTable(t *testing.T) {
	set, err := migrations.NewSet(V0011{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	up := rendered[0].Steps[0].Up
	// rename -> add -> remove -> raw: RenameTable is a rename, DropTable/DropIndex are removes.
	if up[0].SQL != "alter table old_widgets rename to widgets" {
		t.Errorf("rename table: %+v", up[0])
	}
	if up[1].SQL != "drop index ix_widgets_name" {
		t.Errorf("drop index: %+v", up[1])
	}
	if up[2].SQL != "drop table widgets" {
		t.Errorf("drop table: %+v", up[2])
	}
}

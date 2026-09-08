package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// V0012_CreateTotals / V0002_Recreate exercise CreateView/RecreateView/ExpectDefinition.
type V0012_CreateTotals struct {
	migrations.ViewMigration[WidgetTotal]
}

func (V0012_CreateTotals) Action(a *migrations.ViewActions) { a.CreateView() }

type V0012 struct{}

func (V0012) Compose(v *migrations.VersionBuilder) { v.Apply(V0012_CreateTotals{}) }

func TestViewActions_CreateViewRendersThroughDialect(t *testing.T) {
	set, err := migrations.NewSet(V0012{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	want := "create view if not exists widget_totals as\nselect widget_id, count(*) as total from widgets group by widget_id"
	if got := rendered[0].Steps[0].Up[0].SQL; got != want {
		t.Errorf("create view SQL:\n%s\nwant:\n%s", got, want)
	}
}

type V0013_ExpectAndRecreate struct {
	migrations.ViewMigration[WidgetTotal]
}

func (V0013_ExpectAndRecreate) Action(a *migrations.ViewActions) {
	a.ExpectDefinition("create   view   widget_totals   as select 1")
	a.RecreateView()
}

type V0013 struct{}

func (V0013) Compose(v *migrations.VersionBuilder) { v.Apply(V0013_ExpectAndRecreate{}) }

func TestViewActions_ExpectDefinitionGuardIsNormalizedAndFirst(t *testing.T) {
	set, err := migrations.NewSet(V0013{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	up := rendered[0].Steps[0].Up
	if up[0].SQL != "create view widget_totals as select 1" {
		t.Errorf("expected normalized guard DDL, got %q", up[0].SQL)
	}
	if up[0].GuardView != "widget_totals" {
		t.Errorf("expected GuardView to name the view, got %q", up[0].GuardView)
	}
	if up[1].SQL != "drop view if exists widget_totals" {
		t.Errorf("recreate should drop first: %q", up[1].SQL)
	}
	if up[2].SQL != "create view if not exists widget_totals as\nselect widget_id, count(*) as total from widgets group by widget_id" {
		t.Errorf("recreate should create from the current defining SQL: %q", up[2].SQL)
	}
}

// V0014_MaterializedNotSupported targets a materialized view on SQLite (no support) -> DDL-002.
type V0014_MaterializedNotSupported struct {
	migrations.ViewMigration[WidgetTotal]
}

func (V0014_MaterializedNotSupported) Action(a *migrations.ViewActions) { a.CreateView() }

func TestViewActions_MaterializedViewWithoutDialectSupportIsDDL002(t *testing.T) {
	set, err := migrations.NewSet(v0014Root{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.Render(materializedTestLoader(), sqlite.New())
	if core.CodeOf(err) != "DDL-002" {
		t.Fatalf("expected DDL-002, got %v", err)
	}
}

type v0014Root struct{}

func (v0014Root) Compose(v *migrations.VersionBuilder) { v.Apply(V0014_MaterializedNotSupported{}) }
func (v0014Root) Version() int64                       { return 14 }

// V0015_NotView targets a table-backed entity through ViewMigration -> DDL-001.
type V0015_NotView struct {
	migrations.ViewMigration[Widget]
}

func (V0015_NotView) Action(a *migrations.ViewActions) { a.SQL("select 1") }

type V0015 struct{}

func (V0015) Compose(v *migrations.VersionBuilder) { v.Apply(V0015_NotView{}) }

func TestViewActions_TableBackedEntityIsDDL001(t *testing.T) {
	set, err := migrations.NewSet(V0015{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.Render(testLoader(), sqlite.New())
	if core.CodeOf(err) != "DDL-001" {
		t.Fatalf("expected DDL-001, got %v", err)
	}
}

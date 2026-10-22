package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The shadow takes views and triggers from the replayed steps, not from a catalog read (ADR-0043,
// spec/migrations.md "Dialects"): a view from the step's last create statement (a later drop ends the history),
// a trigger from the declaration the step leaves behind; where the catalog is lossless (SQLite) it is read too
// and only a disagreement is noted.

type histWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (histWidget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("hist_widgets"), Triggers: []orm.TriggerLink{orm.Trigger[histTrigger]()}}
}

type histNames struct {
	ID int64 `orm:"column,key"`
}

func (histNames) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("hist_widget_names", "select id from hist_widgets")}
}

type histTrigger struct{}

func (histTrigger) Trigger() orm.TriggerDef {
	return orm.TriggerDef{Name: "trg_hist", Timing: orm.TimingAfter, Events: orm.EventUpdate, SQL: "select 1;"}
}

func snapshotExists(dir string, parts ...string) bool {
	_, err := os.Stat(filepath.Join(append([]string{dir}, parts...)...))
	return err == nil
}

func TestShadow_AViewIsTheStepsLastCreateAndALaterDropEndsItsHistory(t *testing.T) {
	step := func(version int64, object, description string, up ...string) *migrations.SQLVersion {
		return migrations.NewSQLVersion(version, migrations.SQLVersionStep{ObjectName: object, Description: description, Up: up})
	}
	set, err := migrations.NewSet(
		step(1, "hist_widgets", "create", "create table hist_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT"),
		step(2, "hist_widget_names", "create_view", "create view hist_widget_names as select id, name from hist_widgets"),
		// A drop and a create in one step: the last create is the snapshot.
		step(3, "hist_widget_names", "recreate_view",
			"drop view hist_widget_names", "create view if not exists hist_widget_names as\nselect id from hist_widgets"),
		// No create in the step: SQLite stores the text verbatim, so the catalog stands in for it.
		step(4, "hist_widget_names", "no_create", "select 1"),
		step(5, "hist_widget_names", "drop_view", "drop view hist_widget_names"),
	)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	result, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: set, Entities: []reflect.Type{reflect.TypeFor[histWidget](), reflect.TypeFor[histNames]()}, OutDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range result.Notes {
		t.Logf("note: %s", note)
	}

	read := func(version string) string {
		data, err := os.ReadFile(filepath.Join(dir, "View", "histNames", version))
		if err != nil {
			t.Fatalf("the shadow wrote no %s: %v", version, err)
		}
		return string(data)
	}
	if got := read("V0002.schema.json"); !strings.Contains(got, `"ddl": "create view hist_widget_names as select id, name from hist_widgets"`) {
		t.Errorf("V0002 should hold the created view:\n%s", got)
	}
	if got := read("V0003.schema.json"); !strings.Contains(got, `"ddl": "create view hist_widget_names as select id from hist_widgets"`) {
		t.Errorf("V0003 should hold the last create of the step, neutral (no if not exists):\n%s", got)
	}
	if got := read("V0004.schema.json"); !strings.Contains(got, `"ddl": "create view hist_widget_names as select id from hist_widgets"`) {
		t.Errorf("V0004 has no create: the verbatim catalog stands in:\n%s", got)
	}
	if snapshotExists(dir, "View", "histNames", "V0005.schema.json") {
		t.Error("a dropped view ends its snapshot history: V0005 should not exist")
	}
}

func TestShadow_ATriggerIsTheDeclarationTheStepLeavesBehindAndADropEndsItsHistory(t *testing.T) {
	declaration := &migrations.TriggerDeclaration{
		Relation: "hist_widgets", RelationKind: migrations.RelationKindTable,
		Timing: core.TimingAfter, Events: core.EventUpdate, SQL: "select 1;",
	}
	set, err := migrations.NewSet(
		migrations.NewSQLVersion(1, migrations.SQLVersionStep{
			ObjectName: "hist_widgets", Description: "create",
			Up: []string{"create table hist_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT"},
		}),
		migrations.NewSQLVersion(2, migrations.SQLVersionStep{ObjectName: "trg_hist", Description: "create", Trigger: declaration}),
		// A raw step that drops the trigger declares nothing; the read-back finds it gone: the history ends.
		migrations.NewSQLVersion(3, migrations.SQLVersionStep{
			ObjectName: "trg_hist", Description: "drop", Up: []string{"drop trigger trg_hist"},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := sqlite.Shadow(context.Background(), sqlite.ShadowOptions{
		Set: set, Entities: []reflect.Type{reflect.TypeFor[histWidget]()}, Triggers: []reflect.Type{reflect.TypeFor[histTrigger]()},
		OutDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	if !snapshotExists(dir, "Trigger", "histTrigger", "V0002.schema.json") {
		t.Error("V0002 should hold the declared trigger")
	}
	if snapshotExists(dir, "Trigger", "histTrigger", "V0003.schema.json") {
		t.Error("a dropped trigger ends its snapshot history: V0003 should not exist")
	}
}

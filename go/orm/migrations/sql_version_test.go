package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func TestSQLVersion_RendersRawStepsWithoutTypeNames(t *testing.T) {
	version := migrations.NewSQLVersion(1, migrations.SQLVersionStep{
		ObjectName:  "widgets",
		Description: "Create",
		Up:          []string{"create table widgets (id integer)"},
		Down:        []string{"drop table widgets"},
		Renames:     []migrations.ColumnRename{{From: "old", To: "new"}},
	})

	set, err := migrations.NewSet(version)
	if err != nil {
		t.Fatal(err)
	}
	if numbers := set.VersionNumbers(); len(numbers) != 1 || numbers[0] != 1 {
		t.Fatalf("expected [1], got %v", numbers)
	}

	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	step := rendered[0].Steps[0]
	if step.ObjectName != "widgets" || step.Description != "Create" {
		t.Fatalf("unexpected step: %+v", step)
	}
	if len(step.Up) != 1 || step.Up[0].SQL != "create table widgets (id integer)" {
		t.Fatalf("unexpected up: %+v", step.Up)
	}
	if len(step.Down.Core) != 1 || step.Down.Core[0].SQL != "drop table widgets" {
		t.Fatalf("unexpected down: %+v", step.Down)
	}
	if len(step.Renames) != 1 || step.Renames[0] != (migrations.ColumnRename{From: "old", To: "new"}) {
		t.Fatalf("unexpected renames: %+v", step.Renames)
	}
}

func TestSQLVersion_ExpectDefinitionRendersAsNormalizedGuardFirst(t *testing.T) {
	version := migrations.NewSQLVersion(2, migrations.SQLVersionStep{
		ObjectName:       "widget_totals",
		Description:      "Recreate",
		Up:               []string{"drop view if exists widget_totals", "create view widget_totals as select 1"},
		ExpectDefinition: "create   view  widget_totals   as select 0",
	})
	set, err := migrations.NewSet(version)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	up := rendered[0].Steps[0].Up
	if up[0].SQL != "create view widget_totals as select 0" || up[0].GuardView != "widget_totals" {
		t.Fatalf("expected the normalized guard first, got %+v", up[0])
	}
	if len(up) != 3 {
		t.Fatalf("expected guard + 2 up statements, got %d: %+v", len(up), up)
	}
}

func TestSQLVersion_MultipleStepsComposeInOrder(t *testing.T) {
	version := migrations.NewSQLVersion(3,
		migrations.SQLVersionStep{ObjectName: "a", Description: "First", Up: []string{"sql a"}},
		migrations.SQLVersionStep{ObjectName: "b", Description: "Second", Up: []string{"sql b"}},
	)
	set, err := migrations.NewSet(version)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	steps := rendered[0].Steps
	if len(steps) != 2 || steps[0].ObjectName != "a" || steps[1].ObjectName != "b" {
		t.Fatalf("unexpected steps: %+v", steps)
	}
}

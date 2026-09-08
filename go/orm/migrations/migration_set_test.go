package migrations_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// --- fixture step/version types (named to satisfy MIG-001's regex unless noted) ---

type V0001_CreateWidget struct {
	migrations.TableMigration[Widget]
}

func (V0001_CreateWidget) Action(a *migrations.TableActions) { a.CreateTable() }

type V0002_AddNote struct {
	migrations.TableMigration[Widget]
}

func (V0002_AddNote) Action(a *migrations.TableActions) { a.AddColumn("note", "TEXT") }

type V0001 struct{}

func (V0001) Compose(v *migrations.VersionBuilder) { v.Apply(V0001_CreateWidget{}) }

type V0002 struct{}

func (V0002) Compose(v *migrations.VersionBuilder) { v.Apply(V0002_AddNote{}) }

func TestNewSet_OrdersVersionsByNumber(t *testing.T) {
	set, err := migrations.NewSet(V0002{}, V0001{})
	if err != nil {
		t.Fatal(err)
	}
	if numbers := set.VersionNumbers(); len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("expected [1 2], got %v", numbers)
	}
}

// malformedRoot has a type name that is not V<version>, and does not implement Versioned.
type malformedRoot struct{}

func (malformedRoot) Compose(*migrations.VersionBuilder) {}

func TestNewSet_MalformedRootNameIsMIG001(t *testing.T) {
	_, err := migrations.NewSet(malformedRoot{})
	if core.CodeOf(err) != "MIG-001" {
		t.Fatalf("expected MIG-001, got %v", err)
	}
}

// versionedRootA/B both declare version 1 via Versioned, regardless of type name.
type versionedRootA struct{}

func (versionedRootA) Compose(*migrations.VersionBuilder) {}
func (versionedRootA) Version() int64                     { return 1 }

type versionedRootB struct{}

func (versionedRootB) Compose(*migrations.VersionBuilder) {}
func (versionedRootB) Version() int64                     { return 1 }

func TestNewSet_DuplicateRootVersionIsMIG002(t *testing.T) {
	_, err := migrations.NewSet(versionedRootA{}, versionedRootB{})
	if core.CodeOf(err) != "MIG-002" {
		t.Fatalf("expected MIG-002, got %v", err)
	}
}

// mismatchedRoot claims version 1 (via Versioned) but composes a step declaring version 2.
type mismatchedRoot struct{}

func (mismatchedRoot) Compose(v *migrations.VersionBuilder) { v.Apply(V0002_AddNote{}) }
func (mismatchedRoot) Version() int64                       { return 1 }

func TestNewSet_StepVersionMismatchIsMIG003(t *testing.T) {
	_, err := migrations.NewSet(mismatchedRoot{})
	if core.CodeOf(err) != "MIG-003" {
		t.Fatalf("expected MIG-003, got %v", err)
	}
}

// V0004_Bad embeds the table marker but never implements Action — a shape error, not a naming one.
type V0004_Bad struct {
	migrations.TableMigration[Widget]
}

type V0004 struct{}

func (V0004) Compose(v *migrations.VersionBuilder) { v.Apply(V0004_Bad{}) }

func TestNewSet_TableStepWithoutActionIsMIG001(t *testing.T) {
	_, err := migrations.NewSet(V0004{})
	if core.CodeOf(err) != "MIG-001" {
		t.Fatalf("expected MIG-001, got %v", err)
	}
}

// describedStep overrides name-based parsing entirely via Described; its type
// name matches neither V<version> nor V<version>_<Description>.
type describedStep struct {
	migrations.TableMigration[Widget]
}

func (describedStep) Action(a *migrations.TableActions) { a.AddColumn("note", "TEXT") }
func (describedStep) Version() int64                    { return 3 }
func (describedStep) Description() string               { return "CustomDescription" }

type V0003 struct{}

func (V0003) Compose(v *migrations.VersionBuilder) { v.Apply(describedStep{}) }

func TestNewSet_DescribedOverridesStepNameParsing(t *testing.T) {
	set, err := migrations.NewSet(V0003{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered[0].Steps[0].Description; got != "CustomDescription" {
		t.Fatalf("expected the Described description, got %q", got)
	}
}

// V0005_Second is a second step type also targeting Widget, composed alongside V0001_CreateWidget.
type V0005_Second struct {
	migrations.TableMigration[Widget]
}

func (V0005_Second) Action(a *migrations.TableActions) { a.AddColumn("extra", "TEXT") }

type V0005_First struct {
	migrations.TableMigration[Widget]
}

func (V0005_First) Action(a *migrations.TableActions) { a.AddColumn("extra2", "TEXT") }

type V0005 struct{}

func (V0005) Compose(v *migrations.VersionBuilder) { v.Apply(V0005_First{}).Apply(V0005_Second{}) }

func TestSet_Render_ComposingSameObjectTwiceIsMIG002(t *testing.T) {
	set, err := migrations.NewSet(V0005{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = set.Render(testLoader(), sqlite.New())
	if core.CodeOf(err) != "MIG-002" {
		t.Fatalf("expected MIG-002 at render, got %v", err)
	}
}

func TestSet_Render_RendersUpStatementsInOrder(t *testing.T) {
	set, err := migrations.NewSet(V0001{}, V0002{})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := set.Render(testLoader(), sqlite.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 2 {
		t.Fatalf("expected 2 rendered versions, got %d", len(rendered))
	}
	first := rendered[0]
	if first.Version != 1 || first.Steps[0].ObjectName != "widgets" || first.Steps[0].Description != "CreateWidget" {
		t.Fatalf("unexpected first version: %+v", first)
	}
	second := rendered[1]
	if second.Steps[0].Up[0].SQL != "alter table widgets add column note TEXT" {
		t.Fatalf("unexpected add-column SQL: %+v", second.Steps[0].Up)
	}
}

func TestNewSet_NoVersionsIsEmptySet(t *testing.T) {
	set, err := migrations.NewSet()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Versions()) != 0 {
		t.Fatalf("expected an empty set, got %v", set.Versions())
	}
}

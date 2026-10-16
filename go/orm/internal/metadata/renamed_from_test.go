package metadata_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// The declared rename of Level 3 M5 (spec/metadata-model.md "Declared renames", MAP-027): the renamed_from tag
// option, its export, and the declaration errors.

type renamedFromFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"column=note,renamed_from=remark"`
	Kept string  `orm:"column"`
}

func TestRenamedFrom_LoadsOntoThePropertyAndExportsOnlyWhenDeclared(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[renamedFromFixture](loader)
	if err != nil {
		t.Fatal(err)
	}

	note := m.Property("Note")
	if note.RenamedFrom == nil || *note.RenamedFrom != "remark" {
		t.Fatalf("Note.RenamedFrom = %v, want remark", note.RenamedFrom)
	}
	if m.Property("Kept").RenamedFrom != nil {
		t.Errorf("Kept declares no rename but RenamedFrom = %v", m.Property("Kept").RenamedFrom)
	}

	exported, err := metadata.Export(m, loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(exported, `"renamedFrom"`); got != 1 {
		t.Errorf("expected exactly one renamedFrom in the export, found %d:\n%s", got, exported)
	}
	if !strings.Contains(exported, `"renamedFrom": "remark"`) {
		t.Errorf("expected renamedFrom remark in the export:\n%s", exported)
	}
}

func TestRenamedFrom_TheSampleRoleDeclaresTheV0004Rename(t *testing.T) {
	m, err := metadata.Load[sample.Role](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	name := m.Property("Name")
	if name.ColumnName != "role_name" || name.RenamedFrom == nil || *name.RenamedFrom != "name" {
		t.Errorf("Role.Name = %s renamed from %v, want role_name renamed from name", name.ColumnName, name.RenamedFrom)
	}
}

type renamedFromWithoutColumnFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"renamed_from=remark"`
}

func TestRenamedFrom_WithoutAColumn_IsMAP027(t *testing.T) {
	assertCode[renamedFromWithoutColumnFixture](t, "MAP-027")
}

type renamedFromOnIgnoredFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"ignore,renamed_from=remark"`
}

func TestRenamedFrom_OnAnIgnoredField_IsMAP027(t *testing.T) {
	assertCode[renamedFromOnIgnoredFixture](t, "MAP-027")
}

type renamedFromEmptyNameFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"column,renamed_from="`
}

func TestRenamedFrom_WithAnEmptyName_IsMAP027(t *testing.T) {
	assertCode[renamedFromEmptyNameFixture](t, "MAP-027")
}

type renamedFromBareFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"column,renamed_from"`
}

func TestRenamedFrom_BareOption_IsMAP027(t *testing.T) {
	assertCode[renamedFromBareFixture](t, "MAP-027")
}

type renamedFromTwiceFixture struct {
	ID   int64   `orm:"column,key,generated"`
	Note *string `orm:"column,renamed_from=a,renamed_from=b"`
}

func TestRenamedFrom_NamedTwice_IsMAP023(t *testing.T) {
	assertCode[renamedFromTwiceFixture](t, "MAP-023")
}

func TestRenamedFrom_BuilderCarriesTheDeclarationAndRefusesAnEmptyName(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[legacyFixture]().ToTable("legacy_items")
	builder.Property("ID").Column("legacy_id").Key().Generated()
	builder.Property("DisplayName").Column("display_name").RenamedFrom("title")

	m, err := metadata.Load[legacyFixture](metadata.NewLoader((&orm.MappingOptions{}).Register(builder)))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Property("DisplayName").RenamedFrom; got == nil || *got != "title" {
		t.Fatalf("DisplayName.RenamedFrom = %v, want title", got)
	}

	empty := metadata.NewEntityMapBuilder[legacyFixture]().ToTable("legacy_items")
	empty.Property("ID").Column("legacy_id").Key().Generated()
	empty.Property("DisplayName").Column("display_name").RenamedFrom("")
	_, err = metadata.Load[legacyFixture](metadata.NewLoader((&orm.MappingOptions{}).Register(empty)))
	if !core.HasCode(err, "MAP-027") {
		t.Fatalf("expected MAP-027 for an empty builder name, got %v", err)
	}
}

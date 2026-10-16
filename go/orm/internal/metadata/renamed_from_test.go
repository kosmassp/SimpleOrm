package metadata_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

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

// assertOnlyMAP027 pins that a misplaced declaration is MAP-027 and never also another code (spec/migrations.md
// "Draft migrations" Clarifications: MAP-027 covers every misplaced declaration).
func assertOnlyMAP027[T any](t *testing.T) {
	t.Helper()
	_, err := metadata.Load[T](metadata.NewLoader(nil))
	var mapping *core.MappingErrors
	if !errors.As(err, &mapping) || len(mapping.Errors) == 0 {
		t.Fatalf("expected a MappingErrors carrying MAP-027, got %v", err)
	}
	for _, e := range mapping.Errors {
		if e.Code != "MAP-027" {
			t.Errorf("expected only MAP-027, also got %v", e)
		}
	}
}

type renamedFromOnNavigationFixture struct {
	ID     int64        `orm:"column,key,generated"`
	UserID int64        `orm:"column"`
	User   *sample.User `orm:"many_to_one=UserID,renamed_from=old"`
}

func (renamedFromOnNavigationFixture) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("renamed_on_navigation"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[sample.User]("UserID")},
	}
}

func TestRenamedFrom_OnANavigation_IsMAP027(t *testing.T) {
	assertOnlyMAP027[renamedFromOnNavigationFixture](t)
}

type renamedFromOwnedPlain struct {
	Street string `orm:"column"`
}

func (renamedFromOwnedPlain) OwnedType() {}

type renamedFromOwnedWithRename struct {
	Street string `orm:"column,renamed_from=old"`
}

func (renamedFromOwnedWithRename) OwnedType() {}

type renamedFromOnOwnedNavigationFixture struct {
	ID      int64                  `orm:"column,key,generated"`
	Address *renamedFromOwnedPlain `orm:"owned,renamed_from=old"`
}

func TestRenamedFrom_OnAnOwnedNavigation_IsMAP027(t *testing.T) {
	assertOnlyMAP027[renamedFromOnOwnedNavigationFixture](t)
}

type renamedFromOnOwnedMemberFixture struct {
	ID      int64                       `orm:"column,key,generated"`
	Address *renamedFromOwnedWithRename `orm:"owned"`
}

func TestRenamedFrom_OnAnOwnedTypesMember_IsMAP027(t *testing.T) {
	assertOnlyMAP027[renamedFromOnOwnedMemberFixture](t)
}

type renamedFromOnViewFixture struct {
	ID   int64  `orm:"column"`
	Note *int64 `orm:"column,renamed_from=old"`
}

func (renamedFromOnViewFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("renamed_view", "select 1 as id, 2 as note")}
}

func TestRenamedFrom_OnAView_IsMAP027(t *testing.T) {
	assertOnlyMAP027[renamedFromOnViewFixture](t)
}

type renamedFromSameOriginFixture struct {
	ID     int64  `orm:"column,key,generated"`
	First  string `orm:"column,renamed_from=old"`
	Second string `orm:"column,renamed_from= OLD "`
}

func TestRenamedFrom_TwoPropertiesDeclaringOnePreviousColumn_AreMAP027AtLoad(t *testing.T) {
	assertOnlyMAP027[renamedFromSameOriginFixture](t)
}

func TestRenamedFrom_TheBuilderRefusesTwoPropertiesWithOnePreviousColumn(t *testing.T) {
	type built struct {
		ID    int64
		Title string
		Other string
	}
	builder := metadata.NewEntityMapBuilder[built]().ToTable("built")
	builder.Property("ID").Key()
	builder.Property("Title").RenamedFrom("legacy")
	builder.Property("Other").RenamedFrom(" LEGACY ")

	_, err := metadata.Load[built](metadata.NewLoader((&orm.MappingOptions{}).Register(builder)))
	var mapping *core.MappingErrors
	if !errors.As(err, &mapping) || len(mapping.Errors) != 1 || mapping.Errors[0].Code != "MAP-027" {
		t.Fatalf("expected exactly one MAP-027, got %v", err)
	}
}

func TestRenamedFrom_ADeclaredNameIsTrimmedInTheMapAndTheExport(t *testing.T) {
	type built struct {
		ID    int64
		Title string
	}
	builder := metadata.NewEntityMapBuilder[built]().ToTable("built")
	builder.Property("ID").Key()
	builder.Property("Title").RenamedFrom("  name ")

	loader := metadata.NewLoader((&orm.MappingOptions{}).Register(builder))
	m, err := metadata.Load[built](loader)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Property("Title").RenamedFrom; got == nil || *got != "name" {
		t.Fatalf("Title.RenamedFrom = %v, want the trimmed name", got)
	}
	exported, err := metadata.Export(m, loader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(exported, `"renamedFrom": "name"`) {
		t.Errorf("expected the trimmed name in the export:\n%s", exported)
	}
}

func TestRenamedFrom_IsTheLastOptionalKeyOfAnExportedColumn(t *testing.T) {
	type built struct {
		ID      int64
		Stamped time.Time
	}
	builder := metadata.NewEntityMapBuilder[built]().ToTable("built")
	builder.Property("ID").Key().Generated().RenamedFrom("legacy_id")
	builder.Property("Stamped").CreatedAt().RenamedFrom("old_stamp")

	loader := metadata.NewLoader((&orm.MappingOptions{}).Register(builder))
	m, err := metadata.Load[built](loader)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := metadata.Export(m, loader)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`"key": true,\s+"generated": true,\s+"renamedFrom": "legacy_id"\s+\}`),
		regexp.MustCompile(`"audit": "created_at",\s+"renamedFrom": "old_stamp"\s+\}`),
	} {
		if !want.MatchString(exported) {
			t.Errorf("renamedFrom must close the column object after %v:\n%s", want, exported)
		}
	}
}

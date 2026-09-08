package metadata_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Every loader error code reachable in Go (spec/errors.md, CODING-STANDARD
// §10) has a fixture that must fail with it. Mirrors
// dotnet/tests/SimpleOrm.Tests/MappingErrorTests.cs; MAP-011 and MAP-012 are
// not reachable in Go (unenforceable / unreachable per §10) and have no cases
// here.

func assertCode[T any](t *testing.T, code string) {
	t.Helper()
	_, err := metadata.Load[T](metadata.NewLoader(nil))
	if err == nil {
		t.Fatalf("expected an error carrying %s, got none", code)
	}
	if !core.HasCode(err, code) {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

// --- MAP-010: an exported field with no orm tag ---

type map010Fixture struct {
	ID        int64 `orm:"column,key,generated"`
	Forgotten string
}

func TestUnannotatedField_IsMAP010(t *testing.T) { assertCode[map010Fixture](t, "MAP-010") }

// --- MAP-013: generated/version on a non-table; key on a statement/procedure ---

type map013ViewFixture struct {
	Version int64 `orm:"column,version"`
}

func (map013ViewFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("f", "select 1 as version")}
}

func TestVersionOnView_IsMAP013(t *testing.T) { assertCode[map013ViewFixture](t, "MAP-013") }

type map013StatementFixture struct {
	ID int64 `orm:"column,key"`
}

func (map013StatementFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement("select 1 as id")}
}

func TestKeyOnStatement_IsMAP013(t *testing.T) { assertCode[map013StatementFixture](t, "MAP-013") }

// --- MAP-014: index on a source that cannot carry one ---

type map014Fixture struct {
	ID int64 `orm:"column"`
}

func (map014Fixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("f", "select 1 as id"), Indexes: []orm.IndexDef{orm.Index("ID")}}
}

func TestIndexOnView_IsMAP014(t *testing.T) { assertCode[map014Fixture](t, "MAP-014") }

// --- MAP-015: invalid index column stream ---

type map015UnknownFixture struct {
	ID int64 `orm:"column,key,generated"`
}

func (map015UnknownFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("f"), Indexes: []orm.IndexDef{orm.Index("Missing")}}
}

func TestIndexWithUnknownProperty_IsMAP015(t *testing.T) {
	assertCode[map015UnknownFixture](t, "MAP-015")
}

type map015LeadingFixture struct {
	ID int64 `orm:"column,key,generated"`
}

func (map015LeadingFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("f"), Indexes: []orm.IndexDef{orm.Index(orm.Desc, "ID")}}
}

func TestIndexWithLeadingSortOrder_IsMAP015(t *testing.T) {
	assertCode[map015LeadingFixture](t, "MAP-015")
}

type map015DoubledFixture struct {
	ID int64 `orm:"column,key,generated"`
}

func (map015DoubledFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("f"), Indexes: []orm.IndexDef{orm.Index("ID", orm.Desc, orm.Asc)}}
}

func TestIndexWithDoubledSortOrder_IsMAP015(t *testing.T) {
	assertCode[map015DoubledFixture](t, "MAP-015")
}

type map015TokenFixture struct {
	ID int64 `orm:"column,key,generated"`
}

func (map015TokenFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("f"), Indexes: []orm.IndexDef{orm.Index("ID", 42)}}
}

func TestIndexWithAlienToken_IsMAP015(t *testing.T) { assertCode[map015TokenFixture](t, "MAP-015") }

// --- MAP-016: many_to_one FK unknown, unmapped, or arity mismatch ---

type map016Target struct {
	ID int64 `orm:"column,key,generated"`
}

type map016Fixture struct {
	ID    int64         `orm:"column,key,generated"`
	Other *map016Target `orm:"many_to_one=Nope"`
}

func TestManyToOneWithUnknownFK_IsMAP016(t *testing.T) { assertCode[map016Fixture](t, "MAP-016") }

// --- MAP-017: duplicate statement parameter name ---

type map017Fixture struct {
	ID int64 `orm:"column"`
}

func (map017Fixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement("select 1 as id where x = @dup", orm.Param[int64]("dup"), orm.Param[int64]("dup"))}
}

func TestDuplicateStatementParameter_IsMAP017(t *testing.T) { assertCode[map017Fixture](t, "MAP-017") }

// --- MAP-018: two properties map to the same column ---

type map018Fixture struct {
	ID   int64  `orm:"column=same,key,generated"`
	Twin string `orm:"column=same"`
}

func TestDuplicateColumn_IsMAP018(t *testing.T) { assertCode[map018Fixture](t, "MAP-018") }

// --- MAP-019: key shape rules ---

type map019NoKeyFixture struct {
	Value string `orm:"column"`
}

func TestTableWithoutKey_IsMAP019(t *testing.T) { assertCode[map019NoKeyFixture](t, "MAP-019") }

type map019VersionFixture struct {
	ID      int64  `orm:"column,key,generated"`
	Version string `orm:"column,version"`
}

func TestVersionOfWrongType_IsMAP019(t *testing.T) { assertCode[map019VersionFixture](t, "MAP-019") }

type map019CompositeFixture struct {
	Left  int64 `orm:"column,key,generated"`
	Right int64 `orm:"column,key"`
}

func TestGeneratedOnCompositeKey_IsMAP019(t *testing.T) {
	assertCode[map019CompositeFixture](t, "MAP-019")
}

type map019BareKeyFixture struct {
	ID    int64  `orm:"key"`
	Value string `orm:"column"`
}

func TestKeyWithoutColumn_IsMAP019(t *testing.T) { assertCode[map019BareKeyFixture](t, "MAP-019") }

type map019NonIntegerKeyFixture struct {
	ID string `orm:"column,key,generated"`
}

func TestGeneratedNonIntegerKey_IsMAP019(t *testing.T) {
	assertCode[map019NonIntegerKeyFixture](t, "MAP-019")
}

type map019EmptyViewFixture struct {
	ID int64 `orm:"column"`
}

func (map019EmptyViewFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("f", "  ")}
}

func TestViewWithEmptySQL_IsMAP019(t *testing.T) { assertCode[map019EmptyViewFixture](t, "MAP-019") }

// (a navigation combined with column is MAP-019 too — see
// relationship_metadata_test.go's TestColumnOnANavigation_IsMAP019.)

// --- MAP-023 (ADR-0027): malformed mapping declaration ---

type map023UnknownOptionFixture struct {
	ID int64 `orm:"column,bogus_option"`
}

func TestUnknownTagOption_IsMAP023(t *testing.T) {
	assertCode[map023UnknownOptionFixture](t, "MAP-023")
}

type map023UnknownTypeFixture struct {
	ID int64 `orm:"column,type=nope"`
}

func TestUnknownTypeToken_IsMAP023(t *testing.T) { assertCode[map023UnknownTypeFixture](t, "MAP-023") }

type map023EnumIntOnNonEnumFixture struct {
	ID      int64  `orm:"column,key,generated"`
	NotEnum string `orm:"column,enum_int"`
}

func TestEnumAsIntOnNonEnum_IsMAP023(t *testing.T) {
	assertCode[map023EnumIntOnNonEnumFixture](t, "MAP-023")
}

type map023EmbeddedPointerFixture struct {
	*sample.BaseModel
	ID int64 `orm:"column,key,generated"`
}

func TestEmbeddedPointer_IsMAP023(t *testing.T) {
	assertCode[map023EmbeddedPointerFixture](t, "MAP-023")
}

// --- PRM-010 / PRM-011 ---

type prm010Fixture struct {
	ID int64 `orm:"column"`
}

func (prm010Fixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement("select 1 as id where x = @mystery")}
}

func TestUndeclaredPlaceholder_IsPRM010(t *testing.T) { assertCode[prm010Fixture](t, "PRM-010") }

type prm010ViewFixture struct {
	ID int64 `orm:"column"`
}

func (prm010ViewFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("f", "select 1 as id where x = @oops")}
}

func TestViewSQLWithPlaceholder_IsPRM010(t *testing.T) { assertCode[prm010ViewFixture](t, "PRM-010") }

type prm011Fixture struct {
	ID int64 `orm:"column"`
}

func (prm011Fixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Statement("select 1 as id", orm.Param[int64]("unused"))}
}

func TestUnusedDeclaredParameter_IsPRM011(t *testing.T) { assertCode[prm011Fixture](t, "PRM-011") }

// --- every violation is collected before failing ---

type multiErrorFixture struct {
	Value     string `orm:"column"`
	Forgotten string
}

func TestAllViolations_AreCollectedBeforeFailing(t *testing.T) {
	_, err := metadata.Load[multiErrorFixture](metadata.NewLoader(nil))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !core.HasCode(err, "MAP-010") {
		t.Errorf("expected MAP-010 among the errors, got %v", err)
	}
	if !core.HasCode(err, "MAP-019") {
		t.Errorf("expected MAP-019 among the errors, got %v", err)
	}
}

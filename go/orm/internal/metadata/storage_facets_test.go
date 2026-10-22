package metadata_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// The storage facets of Level 4 (ADR-0043, spec/metadata-model.md "Storage facets", MAP-032): the length,
// precision, and scale tag options, their export, and every misplaced declaration.

type facetsFixture struct {
	ID    int64       `orm:"column,key,generated"`
	Name  string      `orm:"column,length=50"`
	Blob  []byte      `orm:"column,length=16"`
	Price orm.Decimal `orm:"column,precision=18,scale=2"`
	Whole orm.Decimal `orm:"column,precision=9"`
	Plain string      `orm:"column"`
}

func TestFacets_LoadOntoThePropertyAndExportOnlyWhenDeclared(t *testing.T) {
	loader := metadata.NewLoader(nil)
	m, err := metadata.Load[facetsFixture](loader)
	if err != nil {
		t.Fatal(err)
	}

	if name := m.Property("Name"); name.Length != 50 || name.Precision != 0 {
		t.Errorf("Name facets = length %d precision %d, want 50 and none", name.Length, name.Precision)
	}
	if blob := m.Property("Blob"); blob.Length != 16 {
		t.Errorf("Blob.Length = %d, want 16", blob.Length)
	}
	if price := m.Property("Price"); price.Precision != 18 || price.Scale != 2 || price.Length != 0 {
		t.Errorf("Price facets = %d,%d length %d, want 18,2", price.Precision, price.Scale, price.Length)
	}
	if whole := m.Property("Whole"); whole.Precision != 9 || whole.Scale != 0 {
		t.Errorf("Whole facets = %d,%d, want a precision with the scale left out as 0", whole.Precision, whole.Scale)
	}
	if plain := m.Property("Plain"); plain.Length != 0 || plain.Precision != 0 || plain.Scale != 0 {
		t.Errorf("Plain declares no facet but carries %d/%d/%d", plain.Length, plain.Precision, plain.Scale)
	}

	exported, err := metadata.Export(m, loader)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"length": 50`, `"length": 16`, `"precision": 18`, `"scale": 2`, `"precision": 9`, `"scale": 0`} {
		if !strings.Contains(exported, want) {
			t.Errorf("expected %s in the export:\n%s", want, exported)
		}
	}
	if got := strings.Count(exported, `"length"`); got != 2 {
		t.Errorf("expected two length keys, found %d:\n%s", got, exported)
	}
	if got := strings.Count(exported, `"precision"`); got != 2 {
		t.Errorf("expected two precision keys, found %d:\n%s", got, exported)
	}
}

func TestFacets_TheSampleTransactionAmountDeclaresPrecision18Scale2(t *testing.T) {
	m, err := metadata.Load[sample.Transaction](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if amount := m.Property("Amount"); amount.Precision != 18 || amount.Scale != 2 {
		t.Errorf("Transaction.Amount = %d,%d, want 18,2", amount.Precision, amount.Scale)
	}
}

type facetsOnOwnedMemberOwned struct {
	City string `orm:"column,length=60"`
}

func (facetsOnOwnedMemberOwned) OwnedType() {}

type facetsOnOwnedMemberFixture struct {
	ID      int64                     `orm:"column,key,generated"`
	Address *facetsOnOwnedMemberOwned `orm:"owned"`
}

func TestFacets_AnOwnedTypesColumnMemberCarriesItsFacet(t *testing.T) {
	m, err := metadata.Load[facetsOnOwnedMemberFixture](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if city := m.Property("Address.City"); city == nil || city.Length != 60 {
		t.Fatalf("Address.City = %+v, want length 60", city)
	}
}

// assertOnlyMAP032 pins that a misplaced facet is MAP-032 and never also another code.
func assertOnlyMAP032[T any](t *testing.T) {
	t.Helper()
	_, err := metadata.Load[T](metadata.NewLoader(nil))
	var mapping *core.MappingErrors
	if !errors.As(err, &mapping) || len(mapping.Errors) == 0 {
		t.Fatalf("expected a MappingErrors carrying MAP-032, got %v", err)
	}
	for _, e := range mapping.Errors {
		if e.Code != "MAP-032" {
			t.Errorf("expected only MAP-032, also got %v", e)
		}
	}
}

type lengthOnAnIntegerFixture struct {
	ID    int64 `orm:"column,key,generated"`
	Count int32 `orm:"column,length=10"`
}

func TestFacets_LengthOnANonStringColumn_IsMAP032(t *testing.T) {
	assertOnlyMAP032[lengthOnAnIntegerFixture](t)
}

type precisionOnAStringFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column,precision=10"`
}

func TestFacets_PrecisionOnANonDecimalColumn_IsMAP032(t *testing.T) {
	assertOnlyMAP032[precisionOnAStringFixture](t)
}

type scaleWithoutPrecisionFixture struct {
	ID    int64       `orm:"column,key,generated"`
	Price orm.Decimal `orm:"column,scale=2"`
}

func TestFacets_AScaleWithoutAPrecision_IsMAP032(t *testing.T) {
	assertOnlyMAP032[scaleWithoutPrecisionFixture](t)
}

type scaleAbovePrecisionFixture struct {
	ID    int64       `orm:"column,key,generated"`
	Price orm.Decimal `orm:"column,precision=4,scale=5"`
}

func TestFacets_AScaleAbovePrecision_IsMAP032(t *testing.T) {
	assertOnlyMAP032[scaleAbovePrecisionFixture](t)
}

type nonPositiveLengthFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column,length=0"`
}

func TestFacets_ANonPositiveLength_IsMAP032(t *testing.T) {
	assertOnlyMAP032[nonPositiveLengthFixture](t)
}

type nonPositivePrecisionFixture struct {
	ID    int64       `orm:"column,key,generated"`
	Price orm.Decimal `orm:"column,precision=-3"`
}

func TestFacets_ANonPositivePrecision_IsMAP032(t *testing.T) {
	assertOnlyMAP032[nonPositivePrecisionFixture](t)
}

type facetWithoutColumnFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"length=50"`
}

func TestFacets_WithoutAColumn_IsMAP032(t *testing.T) { assertOnlyMAP032[facetWithoutColumnFixture](t) }

type facetOnIgnoredFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"ignore,length=50"`
}

func TestFacets_OnAnIgnoredField_IsMAP032(t *testing.T) { assertOnlyMAP032[facetOnIgnoredFixture](t) }

type facetOnViewFixture struct {
	ID   int64  `orm:"column"`
	Name string `orm:"column,length=50"`
}

func (facetOnViewFixture) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("facet_view", "select 1 as id, 'x' as name")}
}

func TestFacets_OnANonTableSource_IsMAP032(t *testing.T) { assertOnlyMAP032[facetOnViewFixture](t) }

type facetOnNavigationFixture struct {
	ID     int64        `orm:"column,key,generated"`
	UserID int64        `orm:"column"`
	User   *sample.User `orm:"many_to_one=UserID,length=5"`
}

func (facetOnNavigationFixture) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:      orm.Table("facet_on_navigation"),
		ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[sample.User]("UserID")},
	}
}

func TestFacets_OnANavigation_IsMAP032(t *testing.T) { assertOnlyMAP032[facetOnNavigationFixture](t) }

type facetOnOwnedNavigationFixture struct {
	ID      int64                     `orm:"column,key,generated"`
	Address *facetsOnOwnedMemberOwned `orm:"owned,length=5"`
}

func TestFacets_OnAnOwnedNavigation_IsMAP032(t *testing.T) {
	assertOnlyMAP032[facetOnOwnedNavigationFixture](t)
}

type malformedFacetValueFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column,length=wide"`
}

func TestFacets_AMalformedValue_StaysMAP023(t *testing.T) {
	assertCode[malformedFacetValueFixture](t, "MAP-023")
}

type repeatedFacetFixture struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column,length=5,length=6"`
}

func TestFacets_ARepeatedOption_IsMAP023(t *testing.T) {
	assertCode[repeatedFacetFixture](t, "MAP-023")
}

func TestFacets_TheBuilderDeclaresThemUnderTheSameRule(t *testing.T) {
	builder := metadata.NewEntityMapBuilder[legacyFixture]().ToTable("legacy_items")
	builder.Property("ID").Column("legacy_id").Key().Generated()
	builder.Property("DisplayName").Column("display_name").Length(40)

	m, err := metadata.Load[legacyFixture](metadata.NewLoader((&orm.MappingOptions{}).Register(builder)))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Property("DisplayName").Length; got != 40 {
		t.Fatalf("DisplayName.Length = %d, want 40", got)
	}

	misplaced := metadata.NewEntityMapBuilder[legacyFixture]().ToTable("legacy_items")
	misplaced.Property("ID").Column("legacy_id").Key().Generated().Length(8)
	misplaced.Property("DisplayName").Column("display_name").Precision(5)
	_, err = metadata.Load[legacyFixture](metadata.NewLoader((&orm.MappingOptions{}).Register(misplaced)))
	if !core.HasCode(err, "MAP-032") {
		t.Fatalf("expected MAP-032 for facets off their column kinds, got %v", err)
	}
}

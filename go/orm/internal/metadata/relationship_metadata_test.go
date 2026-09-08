package metadata_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Level 2 milestone 1 (ADR-0019): declaration-only relationship metadata.
// Mirrors dotnet/tests/SimpleOrm.Tests/RelationshipMetadataTests.cs; MAP-011
// is not enforced in Go (CODING-STANDARD §10) and has no case here.

func relationshipByProperty(m *core.EntityMap, propertyName string) *core.RelationshipMap {
	for _, r := range m.Relationships {
		if r.PropertyName == propertyName {
			return r
		}
	}
	return nil
}

func TestUser_DeclaresAllNavigationKinds(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Relationships) != 3 {
		t.Fatalf("Relationships = %+v, want 3", m.Relationships)
	}

	transactions := relationshipByProperty(m, "Transactions")
	if transactions == nil || transactions.Kind != core.RelationshipOneToMany ||
		transactions.TargetType != reflect.TypeFor[sample.Transaction]() ||
		!equalStrings(transactions.ForeignKeyProperties, []string{"UserID"}) {
		t.Errorf("Transactions relationship = %+v", transactions)
	}

	roles := relationshipByProperty(m, "Roles")
	if roles == nil || roles.Kind != core.RelationshipManyToMany ||
		roles.TargetType != reflect.TypeFor[sample.Role]() ||
		roles.LinkType != reflect.TypeFor[sample.UserRole]() ||
		!equalStrings(roles.LinkForeignKeysToOwner, []string{"UserID"}) ||
		!equalStrings(roles.LinkForeignKeysToTarget, []string{"RoleID"}) {
		t.Errorf("Roles relationship = %+v", roles)
	}

	profile := relationshipByProperty(m, "Profile")
	if profile == nil || profile.Kind != core.RelationshipOneToOne ||
		profile.TargetType != reflect.TypeFor[sample.UserProfile]() ||
		!equalStrings(profile.ForeignKeyProperties, []string{"UserID"}) {
		t.Errorf("Profile relationship = %+v", profile)
	}
}

func TestTransaction_KeepsManyToOneBesideTheCollection(t *testing.T) {
	m, err := metadata.Load[sample.Transaction](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	user := relationshipByProperty(m, "User")
	if user == nil || user.Kind != core.RelationshipManyToOne || !equalStrings(user.ForeignKeyProperties, []string{"UserID"}) {
		t.Errorf("User relationship = %+v", user)
	}

	details := relationshipByProperty(m, "Details")
	if details == nil || details.Kind != core.RelationshipOneToMany || details.TargetType != reflect.TypeFor[sample.TransactionDetail]() {
		t.Errorf("Details relationship = %+v", details)
	}
}

func TestNavigationsAreTransientAndNeverColumns(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Properties {
		if p.PropertyName == "Transactions" || p.PropertyName == "Roles" || p.PropertyName == "Profile" {
			t.Errorf("navigation %q must never be a mapped column", p.PropertyName)
		}
	}
}

// Composite_key_target_takes_a_foreign_key_list_in_key_order: UserRole's key is
// (UserID, RoleID); the referencing side declares both, in that order.

type compositeReferenceFixture struct {
	ID     int64            `orm:"column,key,generated"`
	UserID int64            `orm:"column"`
	RoleID int64            `orm:"column"`
	Grant  *sample.UserRole `orm:"many_to_one=UserID+RoleID"`
}

func TestCompositeKeyTarget_TakesAForeignKeyListInKeyOrder(t *testing.T) {
	m, err := metadata.Load[compositeReferenceFixture](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}
	grant := relationshipByProperty(m, "Grant")
	if grant == nil || grant.Kind != core.RelationshipManyToOne || grant.TargetType != reflect.TypeFor[sample.UserRole]() ||
		!equalStrings(grant.ForeignKeyProperties, []string{"UserID", "RoleID"}) {
		t.Errorf("Grant relationship = %+v", grant)
	}
}

type compositeArityFixture struct {
	ID     int64            `orm:"column,key,generated"`
	UserID int64            `orm:"column"`
	Grant  *sample.UserRole `orm:"many_to_one=UserID"` // UserRole's key has two parts
}

func TestForeignKeyArity_MustMatchTheTargetKey(t *testing.T) {
	assertCode[compositeArityFixture](t, "MAP-016")
}

// One_to_one_on_a_collection_is_MAP020

type map020OneToOneFixture struct {
	ID    int64                 `orm:"column,key,generated"`
	Child []*sample.Transaction `orm:"one_to_one=UserID"` // a collection is not one-to-one
}

func TestOneToOneOnACollection_IsMAP020(t *testing.T) {
	assertCode[map020OneToOneFixture](t, "MAP-020")
}

// Non_collection_navigation_is_MAP020

type map020Fixture struct {
	ID       int64  `orm:"column,key,generated"`
	Children string `orm:"one_to_many=UserID"` // not a collection of an entity
}

func TestNonCollectionNavigation_IsMAP020(t *testing.T) { assertCode[map020Fixture](t, "MAP-020") }

// Unknown_target_foreign_key_is_MAP021

type map021Fixture struct {
	ID       int64                 `orm:"column,key,generated"`
	Children []*sample.Transaction `orm:"one_to_many=NoSuchProperty"`
}

func TestUnknownTargetForeignKey_IsMAP021(t *testing.T) { assertCode[map021Fixture](t, "MAP-021") }

// Link_missing_a_side_is_MAP022: UserRole's ForeignKeys reference User and
// Role — neither side is this type.

type map022MissingFixture struct {
	ID    int64          `orm:"column,key,generated"`
	Roles []*sample.Role `orm:"many_to_many"`
}

func (map022MissingFixture) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:     orm.Table("m22_missing_widgets"),
		ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[sample.UserRole]("Roles")},
	}
}

func TestLinkMissingASide_IsMAP022(t *testing.T) { assertCode[map022MissingFixture](t, "MAP-022") }

// Link_with_an_ambiguous_side_is_MAP022: a single-part key, but the link
// declares two ForeignKeys to this type — the count must equal the key arity.

type map022AmbiguousFixture struct {
	ID    int64          `orm:"column,key,generated"`
	Roles []*sample.Role `orm:"many_to_many"`
}

func (map022AmbiguousFixture) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:     orm.Table("m22_ambiguous_widgets"),
		ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[map022AmbiguousLink]("Roles")},
	}
}

type map022AmbiguousLink struct {
	WidgetID      int64 `orm:"column,key"`
	OtherWidgetID int64 `orm:"column,key"`
	RoleID        int64 `orm:"column"`
}

func (map022AmbiguousLink) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source: orm.Table("m22_links"),
		ForeignKeys: []orm.ForeignKeyDef{
			orm.ForeignKey[map022AmbiguousFixture]("WidgetID"),
			orm.ForeignKey[map022AmbiguousFixture]("OtherWidgetID"),
			orm.ForeignKey[sample.Role]("RoleID"),
		},
	}
}

func TestLinkWithAnAmbiguousSide_IsMAP022(t *testing.T) {
	assertCode[map022AmbiguousFixture](t, "MAP-022")
}

// Column_on_a_navigation_is_MAP019

type map019NavColumnFixture struct {
	ID       int64                 `orm:"column,key,generated"`
	Children []*sample.Transaction `orm:"column,one_to_many=UserID"`
}

func TestColumnOnANavigation_IsMAP019(t *testing.T) { assertCode[map019NavColumnFixture](t, "MAP-019") }

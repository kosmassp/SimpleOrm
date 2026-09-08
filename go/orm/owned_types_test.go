package orm_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Owned value types (ADR-0030): an orm.OwnedType struct whose column fields
// flatten into the owner's table under a prefix; read back as one instance (or
// nil when every member column is NULL and the navigation is a pointer);
// written, filtered, and column-listed through dotted property paths. Mirrors
// dotnet's OwnedTypesTests.

func newProfile(userID int64, address *sample.Address) *sample.UserProfile {
	bio := "bio"
	profile := &sample.UserProfile{UserID: userID, Bio: &bio, Address: address}
	profile.CreatedAtUtc = testsupport.SeedTime
	return profile
}

func TestOwned_LoaderFlattensMembersUnderTheNavigationPrefix(t *testing.T) {
	db := testsupport.OpenSample(t)
	m, err := db.Maps().Load(orm.TypeOf[sample.UserProfile]())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.OwnedTypes) != 1 {
		t.Fatalf("expected one owned type, got %d", len(m.OwnedTypes))
	}
	owned := m.OwnedTypes[0]
	if owned.PropertyName() != "Address" || owned.Prefix != "address_" || !owned.IsNullable {
		t.Fatalf("unexpected owned map: %+v", owned)
	}
	var names, columns []string
	for _, member := range owned.Members {
		names = append(names, member.PropertyName)
		columns = append(columns, member.ColumnName)
		if !member.IsNullable {
			t.Errorf("%s: a nullable navigation makes every member nullable", member.PropertyName)
		}
		if m.Property(member.PropertyName) != member {
			t.Errorf("%s: the same instance must appear in the owner's property list", member.PropertyName)
		}
	}
	if strings.Join(names, ",") != "Address.Street,Address.City,Address.PostalCode" {
		t.Errorf("member names: %v", names)
	}
	if strings.Join(columns, ",") != "address_street,address_city,address_postal_code" {
		t.Errorf("member columns: %v", columns)
	}
}

func TestOwned_RoundTripsAnAddressAndReadsAnAllNullSegmentAsNoAddress(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	withAddress := newProfile(ada.ID, &sample.Address{Street: "1 Main St", City: "Paris"})
	if err := orm.Insert(ctx, db, withAddress); err != nil {
		t.Fatal(err)
	}
	without := newProfile(grace.ID, nil)
	if err := orm.Insert(ctx, db, without); err != nil {
		t.Fatal(err)
	}

	loaded, err := orm.Get[sample.UserProfile](ctx, db, withAddress.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Address == nil || loaded.Address.Street != "1 Main St" || loaded.Address.City != "Paris" || loaded.Address.PostalCode != nil {
		t.Fatalf("unexpected address: %+v", loaded.Address)
	}

	none, err := orm.Get[sample.UserProfile](ctx, db, without.ID)
	if err != nil {
		t.Fatal(err)
	}
	if none.Address != nil { // all three columns NULL → no instance
		t.Fatalf("expected nil address, got %+v", none.Address)
	}
}

func TestOwned_CriteriaAndColumnListsUseTheDottedPathAndTheNavigationName(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	profile := newProfile(ada.ID, &sample.Address{Street: "1 Main St", City: "Paris"})
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatal(err)
	}

	found, err := orm.From[sample.UserProfile](db).
		Where(orm.Eq("Address.City", "Paris")).
		OrderBy("Address.Street").
		List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != profile.ID {
		t.Fatalf("expected the profile by owned criteria, got %+v", found)
	}

	postal := "69001"
	profile.Address.City = "Lyon"
	profile.Address.PostalCode = &postal
	notWritten := "not written"
	profile.Bio = &notWritten
	if err := orm.UpdateOnly(ctx, db, profile, "Address"); err != nil { // the navigation name expands to its members
		t.Fatal(err)
	}
	loaded, err := orm.Get[sample.UserProfile](ctx, db, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Address.City != "Lyon" || loaded.Address.PostalCode == nil || *loaded.Address.PostalCode != "69001" || *loaded.Bio != "bio" {
		t.Fatalf("unexpected row after column-list update: %+v %+v", loaded, loaded.Address)
	}

	if err := orm.UpdateOnly(ctx, db, profile, "Address", "Address.City"); orm.CodeOf(err) != "CRUD-007" {
		t.Fatalf("expected CRUD-007, got %v", err)
	}
	if _, err := orm.From[sample.UserProfile](db).Where(orm.Eq("Address.Nope", "x")).List(ctx); orm.CodeOf(err) != "QRY-006" {
		t.Fatalf("expected QRY-006, got %v", err)
	}
}

func TestOwned_DdlFromMetadataAndExportCarryTheFlattenedColumns(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	if err := orm.CreateTable[parcel](ctx, db); err != nil {
		t.Fatal(err)
	}

	p := &parcel{Label: "box", Origin: point{X: 1, Y: 2}, Destination: point{X: 3, Y: 4}}
	if err := orm.Insert(ctx, db, p); err != nil {
		t.Fatal(err)
	}
	loaded, err := orm.Get[parcel](ctx, db, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Origin != (point{1, 2}) || loaded.Destination != (point{3, 4}) {
		t.Fatalf("unexpected owned values: %+v", loaded)
	}

	m, err := db.Maps().Load(orm.TypeOf[parcel]())
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for _, property := range m.Properties {
		columns = append(columns, property.ColumnName)
	}
	if strings.Join(columns, ",") != "id,label,from_x,from_y,x,y" {
		t.Errorf("columns: %v", columns)
	}
	if m.Property("Origin.X").IsNullable { // a required navigation keeps member nullability
		t.Error("Origin.X must not be nullable")
	}

	json, err := orm.ExportEntityMap(m, db.Maps())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(json, `"column": "from_x"`) || strings.Contains(json, "Origin") {
		t.Errorf("the export must be column-centric with no owned structure:\n%s", json)
	}
}

func TestOwned_LoaderRefusesInvalidDeclarations(t *testing.T) {
	db := testsupport.OpenSample(t)
	codesOf := func(entityType any) []string {
		var me *orm.MappingErrors
		_, err := db.Maps().Load(reflect.TypeOf(entityType))
		if !errors.As(err, &me) {
			t.Fatalf("expected mapping errors for %T, got %v", entityType, err)
		}
		var codes []string
		for _, e := range me.Errors {
			codes = append(codes, e.Code)
		}
		return codes
	}
	contains := func(codes []string, want string) bool {
		for _, c := range codes {
			if c == want {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name   string
		entity any
		code   string
	}{
		{"owned type is an entity", ownsAnEntity{}, "MAP-024"},
		{"owned type lacks the marker", ownsUndeclared{}, "MAP-024"},
		{"key inside the owned type", ownsAKeyedType{}, "MAP-024"},
		{"nested owned", ownsANestedOwned{}, "MAP-024"},
		{"scalar navigation", ownsAScalar{}, "MAP-024"},
		{"no column member", ownsNothingMapped{}, "MAP-024"},
		{"owned combined with column", ownedAndColumn{}, "MAP-019"},
		{"prefix collision", prefixCollision{}, "MAP-018"},
		{"owned type loaded as an entity", point{}, "MAP-024"},
	}
	for _, tc := range cases {
		if codes := codesOf(tc.entity); !contains(codes, tc.code) {
			t.Errorf("%s: expected %s, got %v", tc.name, tc.code, codes)
		}
	}
}

// --- fixtures (test-local, never in sample) -----------------------------------------

type point struct {
	X int32 `orm:"column"`
	Y int32 `orm:"column"`
}

func (point) OwnedType() {}

type parcel struct {
	ID          int64  `orm:"column,key,generated"`
	Label       string `orm:"column"`
	Origin      point  `orm:"owned=from_"`
	Destination point  `orm:"owned="`
}

func (parcel) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("parcels")} }

type ownsAnEntity struct {
	ID   int64        `orm:"column,key"`
	Role *sample.Role `orm:"owned"`
}

type undeclared struct {
	Name *string `orm:"column"`
}

type ownsUndeclared struct {
	ID    int64       `orm:"column,key"`
	Value *undeclared `orm:"owned"`
}

type keyed struct {
	ID int64 `orm:"column,key"`
}

func (keyed) OwnedType() {}

type ownsAKeyedType struct {
	ID    int64  `orm:"column,key"`
	Value *keyed `orm:"owned"`
}

type nested struct {
	Name  *string `orm:"column"`
	Inner *point  `orm:"owned"`
}

func (nested) OwnedType() {}

type ownsANestedOwned struct {
	ID    int64   `orm:"column,key"`
	Value *nested `orm:"owned"`
}

type ownsAScalar struct {
	ID    int64   `orm:"column,key"`
	Value *string `orm:"owned"`
}

type empty struct {
	notMapped string //nolint:unused // unexported: invisible to the loader
}

func (empty) OwnedType() {}

type ownsNothingMapped struct {
	ID    int64  `orm:"column,key"`
	Value *empty `orm:"owned"`
}

type ownedAndColumn struct {
	ID    int64  `orm:"column,key"`
	Value *point `orm:"owned,column"`
}

type prefixCollision struct {
	ID     int64  `orm:"column,key"`
	Direct int32  `orm:"column=x"`
	Value  *point `orm:"owned="`
}

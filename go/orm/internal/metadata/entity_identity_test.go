package metadata_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Entity identity (§7.4): key extraction and equality, incl. composite keys.
// Mirrors dotnet/tests/SimpleOrm.Tests/EntityIdentityTests.cs.

func TestSingleKeyExtractionAndEquality(t *testing.T) {
	m, err := metadata.Load[sample.User](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	a := &sample.User{ID: 42, Name: "Ada", Email: "ada@example.com"}
	b := &sample.User{ID: 42, Name: "Different", Email: "other@example.com"}
	c := &sample.User{ID: 7, Name: "Ada", Email: "ada@example.com"}

	keys, err := m.KeyValues(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != int64(42) {
		t.Errorf("KeyValues(a) = %v", keys)
	}

	equal, err := m.KeysEqual(a, b)
	if err != nil || !equal {
		t.Errorf("KeysEqual(a, b) = %v, %v; want true", equal, err)
	}
	equal, err = m.KeysEqual(a, c)
	if err != nil || equal {
		t.Errorf("KeysEqual(a, c) = %v, %v; want false", equal, err)
	}
}

func TestCompositeKeyExtractionPreservesDeclarationOrder(t *testing.T) {
	m, err := metadata.Load[sample.UserRole](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	link := &sample.UserRole{UserID: 1, RoleID: 2}
	keys, err := m.KeyValues(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != int64(1) || keys[1] != int64(2) {
		t.Errorf("KeyValues(link) = %v", keys)
	}

	equal, err := m.KeysEqual(link, &sample.UserRole{UserID: 1, RoleID: 2})
	if err != nil || !equal {
		t.Errorf("KeysEqual with matching composite key = %v, %v; want true", equal, err)
	}
	equal, err = m.KeysEqual(link, &sample.UserRole{UserID: 2, RoleID: 1})
	if err != nil || equal {
		t.Errorf("KeysEqual with swapped composite key = %v, %v; want false", equal, err)
	}
}

func TestKeylessEntities_HaveNoIdentity(t *testing.T) {
	m, err := metadata.Load[sample.DailySales](metadata.NewLoader(nil))
	if err != nil {
		t.Fatal(err)
	}

	_, err = m.KeyValues(&sample.DailySales{})
	if !core.HasCode(err, "CRUD-002") {
		t.Fatalf("expected CRUD-002, got %v", err)
	}
}

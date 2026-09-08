package amendfixture_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/conformance/amendfixture"
)

func TestSet_HasTwoVersions(t *testing.T) {
	set, err := amendfixture.Set()
	if err != nil {
		t.Fatal(err)
	}
	if numbers := set.VersionNumbers(); len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("expected [1 2], got %v", numbers)
	}
}

func TestNone_HasNoVersions(t *testing.T) {
	set, err := amendfixture.None()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Versions()) != 0 {
		t.Fatalf("expected an empty set, got %v", set.Versions())
	}
}

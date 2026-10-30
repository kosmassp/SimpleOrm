package mapping_test

import (
	"reflect"
	"testing"
)

type planCacheRow struct {
	A *string
	B *string
}

type planCacheOther struct {
	A *string
	B *string
}

// The plan cache is keyed by the result type and the column names, compared structurally (ADR-0048 item 6): the same
// pair is one plan, every other pair its own, and the caller's column slice is never held.
func TestPlan_IsCachedPerResultTypeAndColumnListStructurally(t *testing.T) {
	mapper := newMapper()
	plan := func(resultType reflect.Type, columns ...string) any {
		p, err := mapper.Plan(resultType, columns, "q")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	row, other := reflect.TypeFor[planCacheRow](), reflect.TypeFor[planCacheOther]()

	first := plan(row, "a", "b")
	if plan(row, "a", "b") != first {
		t.Error("the same type and columns built a second plan")
	}
	if plan(row, "b", "a") == first {
		t.Error("another column order shared a plan")
	}
	if plan(other, "a", "b") == first {
		t.Error("another result type shared a plan")
	}
	columns := []string{"a", "b"}
	if p, _ := mapper.Plan(row, columns, "q"); p != first {
		t.Error("a fresh slice of the same columns missed the cache")
	}
	columns[0] = "b" // the cache keeps its own copy
	if plan(row, "a", "b") != first {
		t.Error("mutating a caller's slice changed the cached key")
	}
}

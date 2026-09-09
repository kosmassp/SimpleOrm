package core

import (
	"testing"
	"time"
)

// TestCompareValues_OrdersEveryFixedTypeNumerically pins the one comparator
// db_eager.go's sortByProperties and db_eager_join.go's collection ordering
// both use (through KeyTuple.Compare): every key/FK type orders by value, "10
// after 2" for every integer width — signed and unsigned — never by a
// stringified rendering (spec/loading.md, ADR-0021 add.1).
func TestCompareValues_OrdersEveryFixedTypeNumerically(t *testing.T) {
	cases := []struct {
		name string
		a, b any
	}{
		{"int", 2, 10},
		{"int8", int8(2), int8(10)},
		{"int16", int16(2), int16(10)},
		{"int32", int32(2), int32(10)},
		{"int64", int64(2), int64(10)},
		{"uint", uint(2), uint(10)},
		{"uint8", uint8(2), uint8(10)},
		{"uint16", uint16(2), uint16(10)},
		{"uint32", uint32(2), uint32(10)},
		{"uint64", uint64(2), uint64(10)},
		{"float32", float32(2), float32(10)},
		{"float64", float64(2), float64(10)},
		{"decimal", MustDecimal("2"), MustDecimal("10")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if CompareValues(c.a, c.b) >= 0 {
				t.Errorf("%v should sort before %v, not after it by leading-digit text order", c.a, c.b)
			}
			if CompareValues(c.b, c.a) <= 0 {
				t.Errorf("%v should sort after %v", c.b, c.a)
			}
			if CompareValues(c.a, c.a) != 0 {
				t.Errorf("%v should compare equal to itself", c.a)
			}
		})
	}
}

func TestCompareValues_StringsBoolsTemporalsAndGUIDs(t *testing.T) {
	if CompareValues("ada", "grace") >= 0 {
		t.Error("'ada' should sort before 'grace'")
	}
	if CompareValues(false, true) >= 0 {
		t.Error("false should sort before true")
	}
	early := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if CompareValues(early, late) >= 0 {
		t.Error("an earlier instant should sort before a later one")
	}
	var a, b GUID
	a[15] = 1
	b[15] = 2
	if CompareValues(a, b) >= 0 {
		t.Error("GUIDs should order lexicographically over their bytes")
	}
}

func TestKeyTuple_ComparesElementWise(t *testing.T) {
	left := KeyTuple{int64(1), "ada"}
	right := KeyTuple{int64(1), "grace"}
	if left.Compare(right) >= 0 {
		t.Error("a tie on the first element should fall through to the second")
	}
	if right.Compare(left) <= 0 {
		t.Error("comparison should be antisymmetric")
	}
	if left.Compare(left) != 0 {
		t.Error("a tuple should compare equal to itself")
	}
}

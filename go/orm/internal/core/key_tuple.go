package core

import "reflect"

// KeyTuple is an ordered set of key or foreign-key values, compared by
// structural value equality (§7.4, spec/loading.md: "Key and FK tuples match
// by structural value equality ... never by stringified tokens — lossy
// stringification silently loads the wrong entity for date and blob keys").
// Loading (db_loading.go/db_eager.go) is the one consumer; it lives in core
// because it is a direct extension of entity identity (§7.4), not because
// another package needs it yet.
type KeyTuple []any

// Equal compares two tuples position by position with reflect.DeepEqual —
// the same rule EntityMap.KeysEqual uses for entity identity.
func (t KeyTuple) Equal(other KeyTuple) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		if !reflect.DeepEqual(t[i], other[i]) {
			return false
		}
	}
	return true
}

// HasNull reports whether any part is nil: the rule that excludes an owner
// from querying, symmetric between a null key part and a null many-to-one FK
// part (spec/loading.md).
func (t KeyTuple) HasNull() bool {
	for _, v := range t {
		if v == nil {
			return true
		}
	}
	return false
}

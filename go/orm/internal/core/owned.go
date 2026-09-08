package core

import "reflect"

// OwnedType marks a value object stored as columns of its owner's table
// (ADR-0030): the Go analog of the C# class-level [Owned]. A type implements
// it with an empty method — `func (Address) OwnedType() {}` — and is then
// never an entity: loading it directly is MAP-024, and registries never list
// it. The owner's field opts in with the `owned` tag option.
type OwnedType interface {
	OwnedType()
}

var ownedTypeInterface = reflect.TypeFor[OwnedType]()

// IsOwnedType reports whether t (a struct, or a pointer to one) declares
// itself owned.
func IsOwnedType(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	return t.Implements(ownedTypeInterface) || reflect.PointerTo(t).Implements(ownedTypeInterface)
}

// OwnedMap is an owned value type flattened into its owner's table
// (ADR-0030): the navigation field, the column prefix its members carry, and
// the members. An owned type is not an entity — no table, key, version,
// generated column, relationship, or nested owned type — so every subsystem
// sees only the flattened PropertyMaps; this is how the mapper regroups them.
type OwnedMap struct {
	// Field is the navigation field on the owner; Index its full path there.
	Field reflect.StructField
	Index []int
	// OwnedType is the value type (the struct behind a pointer navigation).
	OwnedType reflect.Type
	// Prefix is prepended to every member's column name; may be empty.
	Prefix string
	// IsNullable is a pointer navigation: then every member column is nullable
	// and a row whose member columns are all NULL leaves the navigation nil.
	IsNullable bool
	// Members are the flattened members, in declaration order; the same
	// instances appear in the owner's property list.
	Members []*PropertyMap
}

// PropertyName is the navigation's field name.
func (o *OwnedMap) PropertyName() string { return o.Field.Name }

// navigation returns the owned struct value inside entity (a pointer to the
// owner, or a struct), allocating a nil pointer navigation when allocate is
// set; the second result is false when the navigation is nil and not allocated.
func (o *OwnedMap) navigation(owner reflect.Value, allocate bool) (reflect.Value, bool) {
	field := owner.FieldByIndex(o.Index)
	if field.Kind() != reflect.Pointer {
		return field, true
	}
	if field.IsNil() {
		if !allocate {
			return reflect.Value{}, false
		}
		field.Set(reflect.New(field.Type().Elem()))
	}
	return field.Elem(), true
}

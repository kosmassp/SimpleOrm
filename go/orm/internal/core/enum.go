package core

import (
	"reflect"
	"strings"
)

// Enum is implemented by a named type that stores as an enum (§7.9): by name
// as TEXT (enum_text, matched case-insensitively on read) or, with the
// `enum_int` tag option, by position as INTEGER. Go has no enum construct, so
// the declaration is explicit — a type that does not implement Enum is a plain
// scalar, never guessed. A string-kinded enum's value is its name; an
// integer-kinded enum's value is its position in EnumNames.
//
//	type TransactionStatus string
//	const (Pending TransactionStatus = "Pending"; ...)
//	func (TransactionStatus) EnumNames() []string { return []string{"Pending", "Completed", "Cancelled"} }
type Enum interface {
	// EnumNames returns the member names in declaration order.
	EnumNames() []string
}

// EnumNamesOf returns the names declared by an enum type (or *type); nil when t is not an enum.
func EnumNamesOf(t reflect.Type) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if !IsEnumType(t) {
		return nil
	}
	value := reflect.New(t)
	if e, ok := value.Interface().(Enum); ok {
		return e.EnumNames()
	}
	if e, ok := value.Elem().Interface().(Enum); ok {
		return e.EnumNames()
	}
	return nil
}

// EnumName is the declared name for an enum value: the value itself for a
// string-kinded enum (normalized to the declared spelling), the name at its
// position for an integer-kinded one. ok is false for a value outside the
// declared members (MAP-031 territory for the caller).
func EnumName(value any) (name string, ok bool) {
	e, isEnum := value.(Enum)
	if !isEnum {
		return "", false
	}
	names := e.EnumNames()
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		index, found := EnumIndex(names, v.String())
		if !found {
			return "", false
		}
		return names[index], true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i := v.Int()
		if i < 0 || i >= int64(len(names)) {
			return "", false
		}
		return names[i], true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i := v.Uint()
		if i >= uint64(len(names)) {
			return "", false
		}
		return names[i], true
	}
	return "", false
}

// EnumIndex finds a name case-insensitively; -1 and false when unknown.
func EnumIndex(names []string, name string) (int, bool) {
	for i, candidate := range names {
		if strings.EqualFold(candidate, name) {
			return i, true
		}
	}
	return -1, false
}

// EnumValue builds a value of the enum type t from a member index: the name
// for a string-kinded enum, the index for an integer-kinded one.
func EnumValue(t reflect.Type, index int, names []string) reflect.Value {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	value := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		value.SetString(names[index])
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(index))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(index))
	}
	return value
}

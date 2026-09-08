package core

import (
	"reflect"
	"time"
)

// ColumnType is the neutral type vocabulary of spec/metadata-model.md — the
// export token and the key of the fixed conversion table (§7.9). The loaders
// resolve every mapped field to one token once (from the Go type, or from an
// explicit `type=` tag option); every other subsystem — conversion, storage
// types, export — reads the token, never the Go type (CODING-STANDARD §10).
type ColumnType string

const (
	TypeInt16          ColumnType = "int16"
	TypeInt32          ColumnType = "int32"
	TypeInt64          ColumnType = "int64"
	TypeDecimal        ColumnType = "decimal"
	TypeDouble         ColumnType = "double"
	TypeFloat          ColumnType = "float"
	TypeBool           ColumnType = "bool"
	TypeString         ColumnType = "string"
	TypeGUID           ColumnType = "guid"
	TypeBytes          ColumnType = "bytes"
	TypeDateTime       ColumnType = "datetime"
	TypeDateTimeOffset ColumnType = "datetimeoffset"
	TypeDate           ColumnType = "date"
	TypeTime           ColumnType = "time"
	TypeEnumText       ColumnType = "enum_text"
	TypeEnumInt        ColumnType = "enum_int"
	// TypeCustom is a type outside the vocabulary: it needs a registered
	// TypeHandler, exports as go:<package>.<Type>, and is not portable.
	TypeCustom ColumnType = "custom"
)

var columnTypes = map[string]ColumnType{
	"int16": TypeInt16, "int32": TypeInt32, "int64": TypeInt64, "decimal": TypeDecimal,
	"double": TypeDouble, "float": TypeFloat, "bool": TypeBool, "string": TypeString,
	"guid": TypeGUID, "bytes": TypeBytes, "datetime": TypeDateTime, "datetimeoffset": TypeDateTimeOffset,
	"date": TypeDate, "time": TypeTime, "enum_text": TypeEnumText, "enum_int": TypeEnumInt,
}

// ParseColumnType resolves a `type=` tag token; false for anything outside the vocabulary (custom is never declared).
func ParseColumnType(token string) (ColumnType, bool) {
	t, ok := columnTypes[token]
	return t, ok
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	decimalType = reflect.TypeFor[Decimal]()
	guidType    = reflect.TypeFor[GUID]()
	bytesType   = reflect.TypeFor[[]byte]()
	enumType    = reflect.TypeFor[Enum]()
)

// ColumnTypeOf is the default token for a Go type (CODING-STANDARD §10) — the
// only place the Go-type → token rule lives. A nullable pointer resolves to its
// element. Enum types (see Enum) resolve to enum_text; the `enum_int` tag option
// switches them in the loader. Anything else is TypeCustom.
func ColumnTypeOf(t reflect.Type) ColumnType {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case timeType:
		return TypeDateTime
	case decimalType:
		return TypeDecimal
	case guidType:
		return TypeGUID
	case bytesType:
		return TypeBytes
	}
	if IsEnumType(t) {
		return TypeEnumText
	}
	switch t.Kind() {
	case reflect.Int8, reflect.Int16, reflect.Uint8:
		return TypeInt16
	case reflect.Int32, reflect.Uint16:
		return TypeInt32
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return TypeInt64
	case reflect.Float32:
		return TypeFloat
	case reflect.Float64:
		return TypeDouble
	case reflect.Bool:
		return TypeBool
	case reflect.String:
		return TypeString
	}
	return TypeCustom
}

// TypeToken is the export token for a resolved property: the neutral token, or
// go:<package>.<Type> for a custom type (implementation-specific, needs a handler).
func TypeToken(t reflect.Type, columnType ColumnType) string {
	if columnType != TypeCustom {
		return string(columnType)
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return "go:" + TypeName(t)
}

func (c ColumnType) IsInteger() bool {
	return c == TypeInt16 || c == TypeInt32 || c == TypeInt64
}

func (c ColumnType) IsEnum() bool { return c == TypeEnumText || c == TypeEnumInt }

func (c ColumnType) IsTemporal() bool {
	return c == TypeDateTime || c == TypeDateTimeOffset || c == TypeDate || c == TypeTime
}

// IsEnumType reports whether t (or *t) implements Enum — the explicit, never
// guessed, way a named type declares itself an enum (§7.9).
func IsEnumType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Implements(enumType) || reflect.PointerTo(t).Implements(enumType)
}

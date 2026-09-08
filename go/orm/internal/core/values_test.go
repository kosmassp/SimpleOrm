package core

import (
	"errors"
	"reflect"
	"testing"
)

func TestGUID_RoundTripsThroughTextAndBytes(t *testing.T) {
	g := NewGUID()
	if g.IsZero() {
		t.Fatal("NewGUID is never zero")
	}
	parsed, err := ParseGUID(g.String())
	if err != nil || parsed != g {
		t.Errorf("text round trip: %v %v", parsed, err)
	}
	upper, err := ParseGUID("{" + g.String() + "}")
	if err != nil || upper != g {
		t.Errorf("braces accepted: %v", err)
	}
	fromBytes, err := GUIDFromBytes(g[:])
	if err != nil || fromBytes != g {
		t.Errorf("blob round trip: %v", err)
	}
	if _, err := ParseGUID("nope"); CodeOf(err) != "MAP-031" {
		t.Errorf("garbage is MAP-031, got %q", CodeOf(err))
	}
	if len(g.String()) != 36 || g.String()[8] != '-' {
		t.Errorf("D format: %q", g.String())
	}
}

type colour string

func (colour) EnumNames() []string { return []string{"Red", "Green"} }

type shade int

func (shade) EnumNames() []string { return []string{"Light", "Dark"} }

func TestEnum_NamesAndPositions(t *testing.T) {
	if !IsEnumType(reflect.TypeFor[colour]()) || !IsEnumType(reflect.TypeFor[*shade]()) {
		t.Fatal("types implementing Enum are enums")
	}
	if IsEnumType(reflect.TypeFor[string]()) {
		t.Fatal("string is not an enum")
	}
	if name, ok := EnumName(colour("green")); !ok || name != "Green" {
		t.Errorf("string enum normalizes to the declared spelling: %q %v", name, ok)
	}
	if _, ok := EnumName(colour("Blue")); ok {
		t.Error("unknown member is not ok")
	}
	if name, ok := EnumName(shade(1)); !ok || name != "Dark" {
		t.Errorf("int enum names by position: %q %v", name, ok)
	}
	if _, ok := EnumName(shade(7)); ok {
		t.Error("out-of-range position is not ok")
	}
	if got := EnumValue(reflect.TypeFor[colour](), 0, []string{"Red", "Green"}).Interface(); got != colour("Red") {
		t.Errorf("EnumValue string: %v", got)
	}
	if got := EnumValue(reflect.TypeFor[shade](), 1, []string{"Light", "Dark"}).Interface(); got != shade(1) {
		t.Errorf("EnumValue int: %v", got)
	}
	if got := ColumnTypeOf(reflect.TypeFor[*colour]()); got != TypeEnumText {
		t.Errorf("enum token: %v", got)
	}
}

func TestColumnTypeOf_DefaultTokens(t *testing.T) {
	cases := map[reflect.Type]ColumnType{
		reflect.TypeFor[int16]():           TypeInt16,
		reflect.TypeFor[int32]():           TypeInt32,
		reflect.TypeFor[int]():             TypeInt64,
		reflect.TypeFor[*int64]():          TypeInt64,
		reflect.TypeFor[float32]():         TypeFloat,
		reflect.TypeFor[float64]():         TypeDouble,
		reflect.TypeFor[bool]():            TypeBool,
		reflect.TypeFor[string]():          TypeString,
		reflect.TypeFor[[]byte]():          TypeBytes,
		reflect.TypeFor[Decimal]():         TypeDecimal,
		reflect.TypeFor[GUID]():            TypeGUID,
		reflect.TypeFor[struct{ X int }](): TypeCustom,
	}
	for typ, expected := range cases {
		if got := ColumnTypeOf(typ); got != expected {
			t.Errorf("ColumnTypeOf(%s) = %s, want %s", typ, got, expected)
		}
	}
	if got := TypeToken(reflect.TypeFor[Decimal](), TypeCustom); got != "go:github.com/kosmassp/SimpleOrm/go/orm/internal/core.Decimal" {
		t.Errorf("custom token: %q", got)
	}
}

func TestErrors_CodesAndAggregates(t *testing.T) {
	single := NewError("PRM-001", "q", "missing")
	if single.Error() != "PRM-001 q: missing" || CodeOf(single) != "PRM-001" {
		t.Errorf("single: %s", single)
	}
	mapping := &MappingErrors{EntityType: reflect.TypeFor[Decimal](), Errors: []*Error{NewError("MAP-010", "T.X", "a"), NewError("MAP-018", "T.Y", "b")}}
	if !HasCode(mapping, "MAP-018") || HasCode(mapping, "MAP-001") || CodeOf(mapping) != "MAP-010" {
		t.Errorf("aggregate codes: %s", mapping)
	}
	var target *MappingErrors
	if !errors.As(error(mapping), &target) {
		t.Error("errors.As on the aggregate")
	}
	concurrency := &ConcurrencyError{Target: "Transaction", Message: "stale"}
	if CodeOf(concurrency) != "CRUD-010" || !HasCode(concurrency, "CRUD-010") {
		t.Errorf("concurrency: %s", concurrency)
	}
	report := &ValidationErrors{Errors: []*ValidationError{{"VAL-001", "q1", "x"}, {"VAL-021", "q1", "y"}, {"MIG-030", "migrations", "z"}}}
	expected := "Schema validation failed with 3 violations\n  q1\n    VAL-001: x\n    VAL-021: y\n  migrations\n    MIG-030: z"
	if report.Error() != expected {
		t.Errorf("report:\n%s", report.Error())
	}
}

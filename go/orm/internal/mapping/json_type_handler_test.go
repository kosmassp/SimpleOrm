package mapping_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

type detailLine struct {
	Description string
	Quantity    int32
	UnitPrice   core.Decimal
}

func TestJSONTypeHandler_SingleObjectRoundTrips(t *testing.T) {
	handler := mapping.JSONTypeHandler[detailLine]{}

	line, err := handler.Parse(`{"description":"cake","quantity":2,"unit_price":"5.00"}`)
	if err != nil {
		t.Fatal(err)
	}
	if line.Description != "cake" || line.Quantity != 2 || !line.UnitPrice.Equal(core.MustDecimal("5.00")) {
		t.Errorf("parsed: %+v", line)
	}

	formatted, err := handler.Format(line)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"description":"cake","quantity":2,"unit_price":"5.00"}`
	if formatted != want {
		t.Errorf("got %q, want %q", formatted, want)
	}
}

func TestJSONTypeHandler_ListOfObjectsRoundTripsViaJSONGroupArray(t *testing.T) {
	handler := mapping.JSONTypeHandler[[]detailLine]{}

	lines, err := handler.Parse(`[{"description":"cake","quantity":2,"unit_price":"5.00"},` +
		`{"description":"candle","quantity":1,"unit_price":"5.25"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Description != "cake" || lines[1].Description != "candle" {
		t.Errorf("parsed list: %+v", lines)
	}
}

func TestJSONTypeHandler_EmptyJSONArrayParsesToEmptyListNotNil(t *testing.T) {
	handler := mapping.JSONTypeHandler[[]detailLine]{}
	lines, err := handler.Parse("[]")
	if err != nil {
		t.Fatal(err)
	}
	if lines == nil || len(lines) != 0 {
		t.Errorf("expected an empty (non-nil) slice, got %#v", lines)
	}
}

type bag struct {
	Name  string
	Count int
}

func TestJSONTypeHandler_KeyMatchingIsCaseInsensitiveAndSnakeCase(t *testing.T) {
	handler := mapping.JSONTypeHandler[bag]{}
	got, err := handler.Parse(`{"NAME":"Ada","count":3}`)
	if err != nil || got.Name != "Ada" || got.Count != 3 {
		t.Errorf("got %+v %v", got, err)
	}
}

type acronym struct {
	OrderID string
}

func TestJSONTypeHandler_SnakeCasesLikeTheNamingConvention(t *testing.T) {
	handler := mapping.JSONTypeHandler[acronym]{}
	formatted, err := handler.Format(acronym{OrderID: "A1"})
	if err != nil {
		t.Fatal(err)
	}
	if formatted != `{"order_id":"A1"}` {
		t.Errorf("got %q", formatted)
	}
}

type nested struct {
	Total core.Decimal
	Lines []detailLine
	At    time.Time
}

func TestJSONTypeHandler_NestedStructsAndSlices(t *testing.T) {
	handler := mapping.JSONTypeHandler[nested]{}
	json := `{"total":"10.25","at":"2026-08-28T10:00:00.0000000Z",` +
		`"lines":[{"description":"cake","quantity":2,"unit_price":"5.00"}]}`
	got, err := handler.Parse(json)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Total.Equal(core.MustDecimal("10.25")) || len(got.Lines) != 1 || got.Lines[0].Description != "cake" {
		t.Errorf("nested parse: %+v", got)
	}
	if got.At.Format("2006-01-02T15:04:05") != "2026-08-28T10:00:00" {
		t.Errorf("nested time: %v", got.At)
	}

	formatted, err := handler.Format(got)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := handler.Parse(formatted.(string))
	if err != nil {
		t.Fatal(err)
	}
	if !roundTripped.Total.Equal(got.Total) || len(roundTripped.Lines) != 1 {
		t.Errorf("round trip mismatch: %+v", roundTripped)
	}
}

type pointerBag struct {
	Note *string
}

func TestJSONTypeHandler_JSONNullIntoAPointerIsNil(t *testing.T) {
	handler := mapping.JSONTypeHandler[pointerBag]{}
	got, err := handler.Parse(`{"note":null}`)
	if err != nil || got.Note != nil {
		t.Errorf("got %+v %v", got, err)
	}

	value := "hi"
	formatted, err := handler.Format(pointerBag{Note: &value})
	if err != nil || formatted != `{"note":"hi"}` {
		t.Errorf("got %v %v", formatted, err)
	}
}

type enumBag struct {
	Status sample.TransactionStatus
}

func TestJSONTypeHandler_EnumByName(t *testing.T) {
	handler := mapping.JSONTypeHandler[enumBag]{}
	got, err := handler.Parse(`{"status":"completed"}`)
	if err != nil || got.Status != sample.Completed {
		t.Errorf("got %+v %v", got, err)
	}
	formatted, err := handler.Format(enumBag{Status: sample.Cancelled})
	if err != nil || formatted != `{"status":"Cancelled"}` {
		t.Errorf("got %v %v", formatted, err)
	}
}

func TestJSONTypeHandler_UnknownKeysAreIgnored(t *testing.T) {
	handler := mapping.JSONTypeHandler[bag]{}
	got, err := handler.Parse(`{"name":"Ada","count":3,"extra":"ignored"}`)
	if err != nil || got.Name != "Ada" || got.Count != 3 {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestJSONTypeHandler_NumbersReadableFromStrings(t *testing.T) {
	handler := mapping.JSONTypeHandler[bag]{}
	got, err := handler.Parse(`{"name":"Ada","count":"3"}`)
	if err != nil || got.Count != 3 {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestRegisterJSON_RegistersAHandlerTheRegistryFinds(t *testing.T) {
	registry := core.NewTypeHandlerRegistry()
	mapping.RegisterJSON[[]detailLine](registry)
	if !registry.Contains(reflect.TypeFor[[]detailLine]()) {
		t.Error("RegisterJSON should register a handler for the type")
	}
}

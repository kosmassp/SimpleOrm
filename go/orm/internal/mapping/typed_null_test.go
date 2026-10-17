package mapping_test

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
)

// Every token's NULL carries a type and still reaches the driver as SQL NULL (ADR-0040 add.1).
func TestTypedNull_EveryTokenNullsWithItsType(t *testing.T) {
	cases := map[core.ColumnType]any{
		core.TypeInt16: sql.NullInt16{}, core.TypeInt32: sql.NullInt32{}, core.TypeInt64: sql.NullInt64{},
		core.TypeEnumInt: sql.NullInt64{}, core.TypeDouble: sql.NullFloat64{}, core.TypeFloat: sql.NullFloat64{},
		core.TypeBool: sql.NullBool{}, core.TypeDateTime: sql.NullTime{}, core.TypeDateTimeOffset: sql.NullTime{},
		core.TypeString: sql.NullString{}, core.TypeDecimal: sql.NullString{}, core.TypeGUID: sql.NullString{},
		core.TypeDate: sql.NullString{}, core.TypeTime: sql.NullString{}, core.TypeEnumText: sql.NullString{},
	}
	for token, want := range cases {
		got := mapping.TypedNull(token)
		if got != want {
			t.Errorf("%s: want %#v, got %#v", token, want, got)
		}
		if v, err := got.(driver.Valuer).Value(); err != nil || v != nil {
			t.Errorf("%s: the typed null must be SQL NULL, got %v, %v", token, v, err)
		}
	}
	if b, ok := mapping.TypedNull(core.TypeBytes).([]byte); !ok || b != nil {
		t.Errorf("bytes: want []byte(nil), got %#v", mapping.TypedNull(core.TypeBytes))
	}
	if got := mapping.TypedNull(core.TypeCustom); got != nil {
		t.Errorf("custom: a handler type stays an untyped nil, got %#v", got)
	}
}

// ToParameter types the NULL; ToDatabase — the snapshot form — keeps the plain nil.
func TestToParameter_TypesOnlyTheNull(t *testing.T) {
	converter := mapping.NewTypeConverter(core.NewTypeHandlerRegistry(), false)
	var avatar *[]byte
	if got, err := converter.ToParameter(avatar, core.TypeBytes, "ctx"); err != nil || got == nil {
		t.Fatalf("a nil bytes parameter should be []byte(nil), got %#v, %v", got, err)
	}
	if got, _ := converter.ToDatabase(avatar, core.TypeBytes, "ctx"); got != nil {
		t.Errorf("ToDatabase keeps the untyped nil, got %#v", got)
	}
	if got, _ := converter.ToParameter("text", core.TypeString, "ctx"); got != "text" {
		t.Errorf("a non-null value converts as before, got %#v", got)
	}
}

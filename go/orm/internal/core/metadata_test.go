package core

import (
	"reflect"
	"testing"
)

type audit struct {
	Created string
}

type widget struct {
	audit
	ID   int64
	Note *string
}

func widgetMap() *EntityMap {
	widgetType := reflect.TypeFor[widget]()
	idField, _ := widgetType.FieldByName("ID")
	noteField, _ := widgetType.FieldByName("Note")
	createdField, _ := widgetType.FieldByName("Created")
	return NewEntityMap(widgetType, RelationTable, "widgets", "", "", nil, []*PropertyMap{
		{Field: idField, Index: idField.Index, DeclaringType: widgetType, PropertyName: "ID", ColumnName: "id", Type: idField.Type, ColumnType: TypeInt64, IsKey: true, IsGenerated: true},
		{Field: noteField, Index: noteField.Index, DeclaringType: widgetType, PropertyName: "Note", ColumnName: "note", Type: noteField.Type, ColumnType: TypeString, IsNullable: true},
		{Field: createdField, Index: createdField.Index, DeclaringType: reflect.TypeFor[audit](), PropertyName: "Created", ColumnName: "created", Type: createdField.Type, ColumnType: TypeString},
	}, KeyDatabaseGenerated, nil, nil)
}

func TestPropertyMap_GetAndSetFollowEmbeddedAndPointerFields(t *testing.T) {
	m := widgetMap()
	w := &widget{ID: 7}
	if m.Property("ID").Get(w) != int64(7) || m.Property("Note").Get(w) != nil || m.Property("Created").Get(*w) != "" {
		t.Error("Get on pointer and value entities")
	}
	if err := m.Property("Note").Set(w, "hello"); err != nil || *w.Note != "hello" {
		t.Errorf("Set allocates a pointer field: %v", err)
	}
	if err := m.Property("Note").Set(w, nil); err != nil || w.Note != nil {
		t.Errorf("Set nil clears: %v", err)
	}
	if err := m.Property("Created").Set(w, "now"); err != nil || w.Created != "now" {
		t.Errorf("Set through the embedded struct: %v", err)
	}
	if err := m.Property("ID").Set(w, "seven"); CodeOf(err) != "MAP-030" {
		t.Errorf("a wrong type is MAP-030, got %v", err)
	}
	if err := m.Property("ID").Set(*w, int64(1)); CodeOf(err) != "MAP-030" {
		t.Errorf("Set needs a pointer, got %v", err)
	}
	if m.Property("Created").Target() != "audit.Created" || m.Property("ID").Target() != "widget.ID" {
		t.Error("Target names the declaring type")
	}
}

func TestEntityMap_IdentityIsTheOrderedKeyValues(t *testing.T) {
	m := widgetMap()
	if len(m.KeyProperties) != 1 || m.KeyProperties[0].PropertyName != "ID" || m.VersionProperty != nil {
		t.Fatal("derived key/version")
	}
	keys, err := m.KeyValues(&widget{ID: 3})
	if err != nil || len(keys) != 1 || keys[0] != int64(3) {
		t.Errorf("KeyValues: %v %v", keys, err)
	}
	equal, _ := m.KeysEqual(&widget{ID: 3}, widget{ID: 3})
	different, _ := m.KeysEqual(&widget{ID: 3}, &widget{ID: 4})
	if !equal || different {
		t.Error("KeysEqual compares position by position")
	}
	keyless := NewEntityMap(reflect.TypeFor[widget](), RelationStatement, "", "", "select 1", nil, nil, KeyNone, nil, nil)
	if _, err := keyless.KeyValues(&widget{}); CodeOf(err) != "CRUD-002" {
		t.Errorf("keyless identity is CRUD-002, got %v", err)
	}
	if m.EntityName() != "widget" || m.PropertyByColumn("note").PropertyName != "Note" || m.Property("nope") != nil {
		t.Error("lookups")
	}
}

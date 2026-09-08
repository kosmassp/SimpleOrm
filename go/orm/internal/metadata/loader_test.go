package metadata_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

type untagged struct {
	ID   int64
	Name string
}

type explicitWidget struct {
	ID int64
}

// A hand-built explicit map: the first loader tier (§7.2), independent of tags and conventions.
type explicitWidgetMap struct{}

func (explicitWidgetMap) EntityType() reflect.Type { return reflect.TypeFor[explicitWidget]() }

func (explicitWidgetMap) Build(convention core.NamingConvention) (*core.EntityMap, error) {
	t := reflect.TypeFor[explicitWidget]()
	field, _ := t.FieldByName("ID")
	return core.NewEntityMap(t, core.RelationTable, convention.TableName("Widget"), "", "", nil, []*core.PropertyMap{
		{Field: field, Index: field.Index, DeclaringType: t, PropertyName: "ID", ColumnName: "id", Type: field.Type, ColumnType: core.TypeInt64, IsKey: true, IsGenerated: true},
	}, core.KeyDatabaseGenerated, nil, nil), nil
}

func TestDescriptor_ReadsTheSampleEntities(t *testing.T) {
	def, ok := metadata.Descriptor(reflect.TypeFor[sample.User]())
	if !ok || def.Source.Kind != core.RelationTable || def.Source.Name != "users" {
		t.Fatalf("User descriptor: %+v %v", def, ok)
	}
	if len(def.Indexes) != 2 || !def.Indexes[0].IsUnique || def.Indexes[0].Columns[0] != "Email" {
		t.Errorf("User indexes: %+v", def.Indexes)
	}
	if len(def.ManyToMany) != 1 || def.ManyToMany[0].Link != reflect.TypeFor[sample.UserRole]() {
		t.Errorf("User links: %+v", def.ManyToMany)
	}
	statement, _ := metadata.Descriptor(reflect.TypeFor[*sample.DailySales]())
	if statement.Source.Kind != core.RelationStatement || len(statement.Source.Parameters) != 1 || statement.Source.Parameters[0].Name != "since" {
		t.Errorf("DailySales descriptor: %+v", statement.Source)
	}
	if _, ok := metadata.Descriptor(reflect.TypeFor[untagged]()); ok {
		t.Error("a type without Entity() has no descriptor")
	}
}

func TestHasMappingDeclarations_SeesTagsThroughEmbeddedStructs(t *testing.T) {
	type onlyBase struct{ sample.BaseModel }
	if !metadata.HasMappingDeclarations(reflect.TypeFor[onlyBase]()) {
		t.Error("an embedded tagged field counts")
	}
	if !metadata.HasMappingDeclarations(reflect.TypeFor[*sample.User]()) {
		t.Error("a descriptor counts")
	}
	if metadata.HasMappingDeclarations(reflect.TypeFor[untagged]()) {
		t.Error("no tags, no descriptor: the convention loader's territory")
	}
}

func TestLoader_ExplicitRegistrationWinsAndCaches(t *testing.T) {
	options := (&orm.MappingOptions{}).Register(explicitWidgetMap{})
	loader := metadata.NewLoader(options)
	first, err := metadata.Load[explicitWidget](loader)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load(reflect.TypeFor[*explicitWidget]())
	if err != nil || first != second {
		t.Errorf("the pointer form loads the same cached map: %v", err)
	}
	if first.RelationName != "widget" || first.KeyStrategy != core.KeyDatabaseGenerated {
		t.Errorf("explicit map: %+v", first)
	}
	if _, err := loader.Load(reflect.TypeFor[int]()); core.CodeOf(err) != "MAP-023" {
		t.Errorf("non-struct types are MAP-023, got %v", err)
	}
}

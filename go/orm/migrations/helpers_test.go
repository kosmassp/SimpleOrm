package migrations_test

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// Fixture entities and hand-built EntityMaps for the migrations package's own
// tests (package_test §7): registered explicitly through metadata.Options.Register
// — the same pattern loader_test.go uses — independent of the struct-tag loader.

type Widget struct {
	ID   int64
	Name string
	Note *string
}

type WidgetTotal struct {
	WidgetID int64
	Total    int64
}

// explicitMap is a minimal metadata.ExplicitMap that always returns a fixed map.
type explicitMap struct {
	entityType reflect.Type
	m          *core.EntityMap
}

func (e explicitMap) EntityType() reflect.Type { return e.entityType }

func (e explicitMap) Build(core.NamingConvention) (*core.EntityMap, error) { return e.m, nil }

func property(t reflect.Type, name, column string, columnType core.ColumnType, opts ...func(*core.PropertyMap)) *core.PropertyMap {
	field, ok := t.FieldByName(name)
	if !ok {
		panic("no field " + name + " on " + t.Name())
	}
	p := &core.PropertyMap{
		Field: field, Index: field.Index, DeclaringType: t,
		PropertyName: name, ColumnName: column, Type: field.Type, ColumnType: columnType,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func key(p *core.PropertyMap)       { p.IsKey = true }
func generated(p *core.PropertyMap) { p.IsGenerated = true }

// widgetMap is a plain table: id (generated key), name, an optional note.
func widgetMap() *core.EntityMap {
	t := reflect.TypeFor[Widget]()
	return core.NewEntityMap(t, core.RelationTable, "widgets", "", "", nil, []*core.PropertyMap{
		property(t, "ID", "id", core.TypeInt64, key, generated),
		property(t, "Name", "name", core.TypeString),
	}, core.KeyDatabaseGenerated, nil, nil)
}

// widgetTotalMap is a view over widgets, keyed by widget_id.
func widgetTotalMap() *core.EntityMap {
	t := reflect.TypeFor[WidgetTotal]()
	return core.NewEntityMap(
		t, core.RelationView, "widget_totals", "",
		"select widget_id, count(*) as total from widgets group by widget_id", nil,
		[]*core.PropertyMap{
			property(t, "WidgetID", "widget_id", core.TypeInt64, key),
			property(t, "Total", "total", core.TypeInt64),
		}, core.KeyNone, nil, nil)
}

// widgetMaterializedTotalMap is the same shape but materialized-view-backed (DDL-002 fixture).
func widgetMaterializedTotalMap() *core.EntityMap {
	m := widgetTotalMap()
	return core.NewEntityMap(
		m.Type, core.RelationMaterializedView, m.RelationName, m.Schema, m.DefiningSQL, nil,
		m.Properties, m.KeyStrategy, m.Indexes, m.Relationships)
}

// testLoader is a metadata.Loader with widgetMap/widgetTotalMap explicitly
// registered (an ExplicitMap, independent of struct-tag declarations).
func testLoader() *metadata.Loader {
	options := (&orm.MappingOptions{}).
		Register(explicitMap{entityType: reflect.TypeFor[Widget](), m: widgetMap()}).
		Register(explicitMap{entityType: reflect.TypeFor[WidgetTotal](), m: widgetTotalMap()})
	return metadata.NewLoader(options)
}

// materializedTestLoader registers WidgetTotal as materialized-view-backed (the DDL-002 fixture).
func materializedTestLoader() *metadata.Loader {
	options := (&orm.MappingOptions{}).
		Register(explicitMap{entityType: reflect.TypeFor[Widget](), m: widgetMap()}).
		Register(explicitMap{entityType: reflect.TypeFor[WidgetTotal](), m: widgetMaterializedTotalMap()})
	return metadata.NewLoader(options)
}

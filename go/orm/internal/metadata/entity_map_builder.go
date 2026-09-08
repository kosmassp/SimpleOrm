package metadata

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// EntityMapBuilder is the manual loader (§7.2, the port of
// dotnet/src/SimpleOrm/EntityMapBuilder.cs): a fluent map for types you can't
// or won't annotate. Registered on MappingOptions, where it takes precedence
// over declarations and conventions. Mapping stays opt-in: only properties
// named with Property are mapped. Kept as a generic type (not a generic
// method) because Go methods cannot take type parameters (CODING-STANDARD
// §3).
type EntityMapBuilder[T any] struct {
	specs     []*MappedPropertySpec
	tableName string
	schema    string
}

// NewEntityMapBuilder creates an empty builder for T.
func NewEntityMapBuilder[T any]() *EntityMapBuilder[T] {
	return &EntityMapBuilder[T]{}
}

// ToTable sets the table name; when never called, the naming convention derives it from the type name.
func (b *EntityMapBuilder[T]) ToTable(name string) *EntityMapBuilder[T] {
	b.tableName = name
	return b
}

// InSchema sets the schema; empty means unqualified.
func (b *EntityMapBuilder[T]) InSchema(schema string) *EntityMapBuilder[T] {
	b.schema = schema
	return b
}

// Property maps one field by name; chain the returned configuration for
// column name, key, generated, version, enum storage, or a type override. An
// unknown field name fails at Build (MAP-023).
func (b *EntityMapBuilder[T]) Property(fieldName string) *PropertyConfiguration {
	spec := &MappedPropertySpec{PropertyName: fieldName}
	b.specs = append(b.specs, spec)
	return &PropertyConfiguration{spec: spec}
}

// EntityType implements metadata.ExplicitMap.
func (b *EntityMapBuilder[T]) EntityType() reflect.Type { return EntityType(reflect.TypeFor[T]()) }

// Build implements metadata.ExplicitMap: resolves every named property against
// T's fields and runs it through the same assembler and validations as the
// other loaders.
func (b *EntityMapBuilder[T]) Build(convention core.NamingConvention) (*core.EntityMap, error) {
	entityType := b.EntityType()
	errs := &errorCollector{entityType: entityType}

	specs := make([]*MappedPropertySpec, 0, len(b.specs))
	for _, spec := range b.specs {
		field, ok := entityType.FieldByName(spec.PropertyName)
		if !ok || field.PkgPath != "" {
			errs.add("MAP-023", target(entityType, spec.PropertyName), "no such exported field on %s", entityType.Name())
			continue
		}
		spec.Field = field
		spec.Index = field.Index
		spec.DeclaringType = declaringTypeAt(entityType, field.Index)
		specs = append(specs, spec)
	}

	if mapping := errs.mappingErrors(); mapping != nil {
		return nil, mapping
	}

	tableName := b.tableName
	if tableName == "" {
		tableName = convention.TableName(entityType.Name())
	}

	return assemble(entityType, core.RelationTable, tableName, b.schema, "", nil, specs, nil, nil, convention, errs)
}

// declaringTypeAt walks the index path (embedded structs included) to find
// the struct that actually declares the field — what PropertyMap.Target uses
// to name errors.
func declaringTypeAt(entityType reflect.Type, index []int) reflect.Type {
	current := entityType
	for _, i := range index[:len(index)-1] {
		current = current.Field(i).Type
		if current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
	}
	return current
}

// PropertyConfiguration is the fluent configuration of one property mapped by EntityMapBuilder.
type PropertyConfiguration struct {
	spec *MappedPropertySpec
}

// Column binds an explicit column name instead of the convention-derived one.
func (c *PropertyConfiguration) Column(name string) *PropertyConfiguration {
	c.spec.ExplicitColumn = &name
	return c
}

// Key marks the property as (part of) the key.
func (c *PropertyConfiguration) Key() *PropertyConfiguration {
	c.spec.IsKey = true
	return c
}

// Generated marks a single integer key as database-generated.
func (c *PropertyConfiguration) Generated() *PropertyConfiguration {
	c.spec.IsGenerated = true
	return c
}

// Version marks the property as the optimistic-concurrency version column.
func (c *PropertyConfiguration) Version() *PropertyConfiguration {
	c.spec.IsVersion = true
	return c
}

// EnumAsInt stores an orm.Enum property by position instead of by name.
func (c *PropertyConfiguration) EnumAsInt() *PropertyConfiguration {
	c.spec.EnumAsInt = true
	return c
}

// Type overrides the neutral ColumnType core.ColumnTypeOf would otherwise derive.
func (c *PropertyConfiguration) Type(columnType core.ColumnType) *PropertyConfiguration {
	c.spec.ColumnTypeOverride = &columnType
	return c
}

package core

import (
	"fmt"
	"reflect"
)

// RelationKind is what backs an entity (ADR-0008): exactly one per type. The
// zero value is a table, so a zero EntityDef declares a convention-named table.
type RelationKind int

const (
	RelationTable RelationKind = iota
	RelationView
	RelationMaterializedView
	RelationStatement
	RelationProcedure
)

// String is the reference's name for the kind, as error messages use it ("is View-backed").
func (k RelationKind) String() string {
	switch k {
	case RelationTable:
		return "Table"
	case RelationView:
		return "View"
	case RelationMaterializedView:
		return "MaterializedView"
	case RelationStatement:
		return "Statement"
	case RelationProcedure:
		return "Procedure"
	}
	return fmt.Sprintf("RelationKind(%d)", int(k))
}

// Token is the export's kind token (spec/metadata-model.md).
func (k RelationKind) Token() string {
	switch k {
	case RelationTable:
		return "table"
	case RelationView:
		return "view"
	case RelationMaterializedView:
		return "materialized_view"
	case RelationStatement:
		return "statement"
	case RelationProcedure:
		return "procedure"
	}
	return fmt.Sprintf("relation_kind_%d", int(k))
}

// KeyStrategy is how key values come to exist (§7.14).
type KeyStrategy int

const (
	// KeyNone: no key declared (statements, procedures, keyless views).
	KeyNone KeyStrategy = iota
	// KeyDatabaseGenerated: the database generates the key (INTEGER PRIMARY KEY); inserts read it back via RETURNING.
	KeyDatabaseGenerated
	// KeyClientGuid: the client supplies a GUID before insert.
	KeyClientGuid
	// KeyNatural: the caller supplies natural or composite key values.
	KeyNatural
)

// Token is the export's strategy token (spec/metadata-model.md).
func (s KeyStrategy) Token() string {
	switch s {
	case KeyNone:
		return "none"
	case KeyDatabaseGenerated:
		return "database_generated"
	case KeyClientGuid:
		return "client_guid"
	case KeyNatural:
		return "natural"
	}
	return fmt.Sprintf("key_strategy_%d", int(s))
}

func (s KeyStrategy) String() string { return s.Token() }

// RelationshipKind is a navigation's cardinality (ADR-0005/0019). Polymorphic
// and "through" relations are ruled out permanently (ADR-0019 add.1).
type RelationshipKind int

const (
	RelationshipManyToOne RelationshipKind = iota
	RelationshipOneToMany
	RelationshipManyToMany
	RelationshipOneToOne
)

// Token is the export's kind token.
func (k RelationshipKind) Token() string {
	switch k {
	case RelationshipManyToOne:
		return "many_to_one"
	case RelationshipOneToMany:
		return "one_to_many"
	case RelationshipManyToMany:
		return "many_to_many"
	case RelationshipOneToOne:
		return "one_to_one"
	}
	return fmt.Sprintf("relationship_kind_%d", int(k))
}

func (k RelationshipKind) String() string { return k.Token() }

// StatementParameter is a declared parameter of a statement or procedure entity
// (ADR-0008 add.2): the SQL-side name and the Go type it binds from.
type StatementParameter struct {
	Name string
	Type reflect.Type
}

// IndexColumn is one column of a declared index, in index order.
type IndexColumn struct {
	PropertyName string
	ColumnName   string
	Descending   bool
}

// EntityIndex is a declared index (ADR-0007); declaration-only until the diff generator consumes it.
type EntityIndex struct {
	Name    string
	Columns []IndexColumn
	Unique  bool
}

// RelationshipMap is a declared navigation (ADR-0005, extended by ADR-0019):
// many-to-one through a foreign key on this type, one-to-many/one-to-one through
// a foreign key on the target, or many-to-many through an explicit link entity.
// Metadata only in this port (loading is Level 2); it exists so the EntityMap
// export is complete.
type RelationshipMap struct {
	PropertyName string
	Kind         RelationshipKind
	// TargetType is the related entity (a collection navigation's element type).
	TargetType reflect.Type
	// ForeignKeyProperties: many-to-one — the FK properties on this type, in the
	// target's key order; one-to-many / one-to-one — the FK properties on the
	// target, in this entity's key order; empty for many-to-many. One entry per
	// key part (ADR-0019 add.1).
	ForeignKeyProperties []string
	// LinkType is the many-to-many link entity; nil otherwise.
	LinkType reflect.Type
	// LinkForeignKeysToOwner: many-to-many only — the link properties referencing this type, in declaration order.
	LinkForeignKeysToOwner []string
	// LinkForeignKeysToTarget: many-to-many only — the link properties referencing the element type, in declaration order.
	LinkForeignKeysToTarget []string
}

// PropertyMap is one mapped field ↔ column pair (§7.1). The loader resolves the
// neutral ColumnType once; conversion, storage types, and the export read it
// and never re-derive it from the Go type.
type PropertyMap struct {
	// Field is the struct field; Index is its full path from the entity type
	// (embedded structs included) — what Get and Set follow.
	Field reflect.StructField
	Index []int
	// DeclaringType is the struct that declares the field (an embedded base for
	// inherited columns); Target() names errors with it.
	DeclaringType reflect.Type
	PropertyName  string
	ColumnName    string
	// Type is the field's Go type, pointer included when nullable.
	Type        reflect.Type
	ColumnType  ColumnType
	IsNullable  bool
	IsKey       bool
	IsGenerated bool
	IsVersion   bool
	// ForeignKeyReferences is the entity this FK column references (the descriptor's ForeignKey), or nil.
	ForeignKeyReferences reflect.Type
}

// EnumAsInt is the §7.9 enum storage flag, carried by the token.
func (p *PropertyMap) EnumAsInt() bool { return p.ColumnType == TypeEnumInt }

// Target is "Type.Property", the form error messages use.
func (p *PropertyMap) Target() string {
	if p.DeclaringType != nil {
		return p.DeclaringType.Name() + "." + p.PropertyName
	}
	return p.PropertyName
}

// ValueType is the field type with a nullable pointer removed: the type a
// converter produces and Set accepts.
func (p *PropertyMap) ValueType() reflect.Type {
	if p.Type.Kind() == reflect.Pointer {
		return p.Type.Elem()
	}
	return p.Type
}

// Get reads the field from an entity (a struct or a pointer to one). A nil
// pointer field reads as nil; a non-nil pointer reads as the pointed-to value.
func (p *PropertyMap) Get(entity any) any {
	v := reflect.Indirect(reflect.ValueOf(entity))
	field := v.FieldByIndex(p.Index)
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		return field.Elem().Interface()
	}
	return field.Interface()
}

// Set writes the field on an entity, which must be a pointer to the struct.
// A nil value clears a pointer field; a non-pointer value assigns to a pointer
// field by allocation. The value must already be of the field's value type (the
// converter's job); anything else is an error, never a silent coercion.
func (p *PropertyMap) Set(entity any, value any) error {
	target := reflect.ValueOf(entity)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return Errorf("MAP-030", p.Target(), "Set needs a non-nil pointer to the entity, got %T", entity)
	}
	field := target.Elem().FieldByIndex(p.Index)
	if value == nil {
		field.Set(reflect.Zero(field.Type()))
		return nil
	}
	source := reflect.ValueOf(value)
	if field.Kind() == reflect.Pointer && source.Kind() != reflect.Pointer {
		if !source.Type().AssignableTo(field.Type().Elem()) {
			return Errorf("MAP-030", p.Target(), "cannot assign %s to %s", source.Type(), field.Type())
		}
		pointer := reflect.New(field.Type().Elem())
		pointer.Elem().Set(source)
		field.Set(pointer)
		return nil
	}
	if !source.Type().AssignableTo(field.Type()) {
		return Errorf("MAP-030", p.Target(), "cannot assign %s to %s", source.Type(), field.Type())
	}
	field.Set(source)
	return nil
}

// EntityMap is the single source of truth about a mapped type (§7.1,
// spec/metadata-model.md). Produced only by the loaders; every other subsystem —
// mapping, generated CRUD, validation, migrations, the criteria core — reads
// this and never the struct tags or descriptor. KeyProperties and
// VersionProperty are derived once by NewEntityMap.
type EntityMap struct {
	Type reflect.Type
	Kind RelationKind
	// RelationName is the table/view/procedure name; "" for statement-backed entities.
	RelationName string
	Schema       string
	// DefiningSQL is a statement's query or a view's defining SELECT; "" for tables and procedures.
	DefiningSQL         string
	StatementParameters []StatementParameter
	Properties          []*PropertyMap
	KeyStrategy         KeyStrategy
	Indexes             []*EntityIndex
	Relationships       []*RelationshipMap
	// KeyProperties are the key properties in declaration order (composite keys are ordered).
	KeyProperties   []*PropertyMap
	VersionProperty *PropertyMap
}

// NewEntityMap assembles a map and derives its key and version properties.
func NewEntityMap(
	entityType reflect.Type,
	kind RelationKind,
	relationName string,
	schema string,
	definingSQL string,
	statementParameters []StatementParameter,
	properties []*PropertyMap,
	keyStrategy KeyStrategy,
	indexes []*EntityIndex,
	relationships []*RelationshipMap,
) *EntityMap {
	m := &EntityMap{
		Type:                entityType,
		Kind:                kind,
		RelationName:        relationName,
		Schema:              schema,
		DefiningSQL:         definingSQL,
		StatementParameters: statementParameters,
		Properties:          properties,
		KeyStrategy:         keyStrategy,
		Indexes:             indexes,
		Relationships:       relationships,
	}
	for _, p := range properties {
		if p.IsKey {
			m.KeyProperties = append(m.KeyProperties, p)
		}
		if p.IsVersion && m.VersionProperty == nil {
			m.VersionProperty = p
		}
	}
	return m
}

// EntityName is the unqualified type name: the export's "entity" and the target of error messages.
func (m *EntityMap) EntityName() string { return m.Type.Name() }

// Property returns the mapped property with the given name, or nil (exact match; callers resolve case rules).
func (m *EntityMap) Property(propertyName string) *PropertyMap {
	for _, p := range m.Properties {
		if p.PropertyName == propertyName {
			return p
		}
	}
	return nil
}

// PropertyByColumn returns the mapped property bound to the column, or nil (exact match).
func (m *EntityMap) PropertyByColumn(columnName string) *PropertyMap {
	for _, p := range m.Properties {
		if p.ColumnName == columnName {
			return p
		}
	}
	return nil
}

// KeyValues is entity identity (§7.4): the key values of an instance, in key
// order. A keyless entity has no identity (CRUD-002).
func (m *EntityMap) KeyValues(entity any) ([]any, error) {
	if len(m.KeyProperties) == 0 {
		return nil, NewError("CRUD-002", m.EntityName(), "the entity defines no key; identity is undefined")
	}
	values := make([]any, len(m.KeyProperties))
	for i, key := range m.KeyProperties {
		values[i] = key.Get(entity)
	}
	return values, nil
}

// KeysEqual is true when two instances have equal key values, position by position (§7.4).
func (m *EntityMap) KeysEqual(left, right any) (bool, error) {
	leftKeys, err := m.KeyValues(left)
	if err != nil {
		return false, err
	}
	rightKeys, err := m.KeyValues(right)
	if err != nil {
		return false, err
	}
	for i := range leftKeys {
		if !reflect.DeepEqual(leftKeys[i], rightKeys[i]) {
			return false, nil
		}
	}
	return true, nil
}

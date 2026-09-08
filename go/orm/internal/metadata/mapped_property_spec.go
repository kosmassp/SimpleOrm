package metadata

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// MappedPropertySpec is the loader-internal working shape of one mapped field
// before assembly (the C# MappedPropertySpec.cs): every annotation collected
// from the `orm` tag, the descriptor, or the builder, resolved into a
// core.PropertyMap only after assembly-wide validation (map_assembler.go).
type MappedPropertySpec struct {
	// Field, Index, DeclaringType mirror core.PropertyMap's identity fields;
	// the builder fills them in at Build() once the field name resolves.
	Field         reflect.StructField
	Index         []int
	DeclaringType reflect.Type
	PropertyName  string

	// ExplicitColumn is the `column=<name>` value, or the builder's Column();
	// nil means the naming convention derives the name.
	ExplicitColumn *string
	// ColumnTypeOverride is the `type=<token>` value, or the builder's Type().
	ColumnTypeOverride *core.ColumnType
	IsKey              bool
	IsGenerated        bool
	IsVersion          bool
	EnumAsInt          bool
	// ForeignKeyReferences is set from the descriptor's EntityDef.ForeignKeys,
	// after every spec's PropertyName is known (ADR-0027, CODING-STANDARD §10).
	ForeignKeyReferences reflect.Type
	// Owner is set when the field is a member of an owned type flattened into
	// the entity (ADR-0030); Field/Index then describe the member inside the
	// owned struct and PropertyName is the member's own name.
	Owner *OwnedSpec
}

// OwnedSpec is the loader-internal working shape of an `owned` navigation
// whose members flatten into the owner (ADR-0030; the C# OwnedSpec); the
// prefix resolves at assembly.
type OwnedSpec struct {
	Field          reflect.StructField
	Index          []int
	OwnedType      reflect.Type
	IsNullable     bool
	ExplicitPrefix *string
}

// IndexColumnSpec is one column of a declared index before its property resolves
// to a mapped column (the C# IndexSpec's column tuple).
type IndexColumnSpec struct {
	PropertyName string
	Descending   bool
}

// IndexSpec is the loader-internal working shape of a declared index, after its
// token-stream shape validates (MAP-014/015) but before column resolution.
type IndexSpec struct {
	// Name is the explicit index name (IndexDef.Named); nil derives it from the convention.
	Name    *string
	Unique  bool
	Columns []IndexColumnSpec
}

// RelationshipSpec is the loader-internal working shape of a declared
// navigation (the C# RelationshipSpec.cs), before the assembler validates
// foreign-key resolution and arity (MAP-016/021/022).
type RelationshipSpec struct {
	PropertyName string
	Kind         core.RelationshipKind
	// TargetType is the related entity (a collection navigation's element type).
	TargetType reflect.Type
	// ForeignKeyProperties: many-to-one — properties of this type; one-to-many/
	// one-to-one — properties of TargetType. Empty for many-to-many.
	ForeignKeyProperties []string
	// LinkType, LinkForeignKeysToOwner, LinkForeignKeysToTarget: many-to-many only.
	LinkType                reflect.Type
	LinkForeignKeysToOwner  []string
	LinkForeignKeysToTarget []string
}

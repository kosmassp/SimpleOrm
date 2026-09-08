package core

import "reflect"

// EntityDefiner is implemented by an entity type to declare what a struct tag
// cannot carry (CODING-STANDARD §10 — the Go analog of C#'s class-level
// attributes): the relation source and its SQL, declared indexes, and typed
// references (foreign keys, many-to-many links). Per-field facts stay in the
// `orm` struct tag. A type with tags but no descriptor is a convention-named
// table; a type with neither goes through the convention loader.
//
//	func (User) Entity() orm.EntityDef {
//	    return orm.EntityDef{
//	        Source:  orm.Table("users"),
//	        Indexes: []orm.IndexDef{orm.Index("Email").Unique(), orm.Index("DisplayName")},
//	        ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[UserRole]("Roles")},
//	    }
//	}
type EntityDefiner interface {
	Entity() EntityDef
}

// EntityDef is the descriptor an entity returns from Entity().
type EntityDef struct {
	// Source is the relation source; the zero value is a table named by the convention.
	Source Source
	// Indexes are the declared indexes (tables and materialized views only, MAP-014).
	Indexes []IndexDef
	// ForeignKeys name the FK properties of this type and the entity each
	// references (the C# [ForeignKey]), in declaration order — what a
	// many-to-many link's sides resolve through (MAP-022).
	ForeignKeys []ForeignKeyDef
	// ManyToMany declares collection navigations resolved through an explicit
	// link entity — declared, never inferred (ADR-0019).
	ManyToMany []ManyToManyDef
}

// Source is the relation source (ADR-0008): exactly one kind per entity.
// Every non-table source is self-contained — views carry their defining
// SELECT, statements and procedures their SQL and parameter contract.
type Source struct {
	Kind RelationKind
	// Name is the table/view/procedure name; "" for statements and for a table named by convention.
	Name   string
	Schema string
	// SQL is a view's defining SELECT, a statement's query, a procedure's body.
	SQL        string
	Parameters []StatementParameter
}

// Table names a table-backed entity.
func Table(name string) Source { return Source{Kind: RelationTable, Name: name} }

// View names a view and carries its defining SELECT (ADR-0008 add.3).
func View(name string, sql string) Source { return Source{Kind: RelationView, Name: name, SQL: sql} }

// MaterializedView is a view that may carry indexes; dormant on SQLite (DDL-002).
func MaterializedView(name string, sql string) Source {
	return Source{Kind: RelationMaterializedView, Name: name, SQL: sql}
}

// Statement makes the type the query (ADR-0008 add.2/ADR-0010): inline SQL plus
// the declared parameter contract, validated against the SQL's placeholders
// (PRM-010/011).
func Statement(sql string, parameters ...StatementParameter) Source {
	return Source{Kind: RelationStatement, SQL: sql, Parameters: parameters}
}

// Procedure names a stored procedure with its body and parameters; dormant on SQLite.
func Procedure(name string, sql string, parameters ...StatementParameter) Source {
	return Source{Kind: RelationProcedure, Name: name, SQL: sql, Parameters: parameters}
}

// Param declares one statement/procedure parameter: the SQL-side name and the
// Go type it binds from (the C# ("since", typeof(DateTime)) pair, typed).
func Param[T any](name string) StatementParameter {
	return StatementParameter{Name: name, Type: reflect.TypeFor[T]()}
}

// IndexDef is a declared index (ADR-0007): a token stream of property names
// with an optional SortOrder after the column it applies to (ADR-0007 add.3),
// validated by the loader (MAP-015). Name derives from the convention when empty.
type IndexDef struct {
	Columns  []any
	Name     string
	IsUnique bool
}

// Index declares an index over property names, e.g. Index("Status", "CreatedAtUtc", Desc).
func Index(columns ...any) IndexDef { return IndexDef{Columns: columns} }

// Named sets an explicit index name instead of the convention-derived one.
func (i IndexDef) Named(name string) IndexDef {
	i.Name = name
	return i
}

// Unique marks the index unique.
func (i IndexDef) Unique() IndexDef {
	i.IsUnique = true
	return i
}

// ForeignKeyDef records that a mapped property references another entity's key.
type ForeignKeyDef struct {
	Property   string
	References reflect.Type
}

// ForeignKey declares that the property references T (the C# [ForeignKey(typeof(T))]).
func ForeignKey[T any](property string) ForeignKeyDef {
	return ForeignKeyDef{Property: property, References: reflect.TypeFor[T]()}
}

// ManyToManyDef declares a collection navigation resolved through a link entity.
type ManyToManyDef struct {
	Property string
	Link     reflect.Type
}

// ManyToMany declares that the collection property is resolved through the
// link entity TLink (the C# [ManyToMany(typeof(TLink))]); the element type
// comes from the field.
func ManyToMany[TLink any](property string) ManyToManyDef {
	return ManyToManyDef{Property: property, Link: reflect.TypeFor[TLink]()}
}

package orm

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// The spec-level contracts live in internal/core so every internal package can
// import them without a cycle; application code sees them here under the
// reference's names (CODING-STANDARD §1). Aliases are identical types — an
// orm.Error is a core.Error, so errors.As works either way — and the generic
// factories are thin wrappers, never second implementations (§8).

type (
	// Error is a runtime failure with a stable code (spec/errors.md); use errors.As.
	Error = core.Error
	// MappingErrors is every violation found while loading one type's EntityMap.
	MappingErrors = core.MappingErrors
	// ValidationError is one SchemaGuard violation.
	ValidationError = core.ValidationError
	// ValidationErrors is SchemaGuard's complete report.
	ValidationErrors = core.ValidationErrors
	// ConcurrencyError is an optimistic-concurrency conflict (CRUD-010).
	ConcurrencyError = core.ConcurrencyError

	// EntityMap is the single source of truth about a mapped type (§7.1).
	EntityMap = core.EntityMap
	// OwnedType marks a value object stored as columns of its owner (ADR-0030): `func (Address) OwnedType() {}`.
	OwnedType = core.OwnedType
	// OwnedMap is an owned value type flattened into its owner's table (ADR-0030).
	OwnedMap = core.OwnedMap
	// PropertyMap is one mapped field ↔ column pair.
	PropertyMap = core.PropertyMap
	// EntityIndex is a declared index in the metadata.
	EntityIndex = core.EntityIndex
	// IndexColumn is one column of a declared index.
	IndexColumn = core.IndexColumn
	// RelationshipMap is a declared navigation in the metadata.
	RelationshipMap = core.RelationshipMap
	// StatementParameter is a declared statement/procedure parameter.
	StatementParameter = core.StatementParameter
	// RelationKind is what backs an entity.
	RelationKind = core.RelationKind
	// KeyStrategy is how key values come to exist.
	KeyStrategy = core.KeyStrategy
	// RelationshipKind is a navigation's cardinality.
	RelationshipKind = core.RelationshipKind
	// ColumnType is the neutral type token vocabulary.
	ColumnType = core.ColumnType

	// EntityDefiner is implemented by entities that carry a descriptor.
	EntityDefiner = core.EntityDefiner
	// EntityDef is the descriptor: source, indexes, typed references.
	EntityDef = core.EntityDef
	// Source is the relation source of a descriptor.
	Source = core.Source
	// IndexDef is a declared index in a descriptor.
	IndexDef = core.IndexDef
	// ForeignKeyDef is a typed foreign-key reference in a descriptor.
	ForeignKeyDef = core.ForeignKeyDef
	// ManyToManyDef is a link-resolved collection navigation in a descriptor.
	ManyToManyDef = core.ManyToManyDef

	// Decimal is the exact, string-backed decimal value (§7.9).
	Decimal = core.Decimal
	// GUID is the 128-bit identifier behind the guid token.
	GUID = core.GUID
	// Enum is implemented by named types that store as enums.
	Enum = core.Enum

	// NamingConvention translates Go names to database names.
	NamingConvention = core.NamingConvention
	// SnakeCase is the default naming convention.
	SnakeCase = core.SnakeCase
	// MappingOptions configures metadata loading: the convention and explicit maps.
	MappingOptions = metadata.Options
	// ExplicitMap is a manual map registration (the builder implements it).
	ExplicitMap = metadata.ExplicitMap
	// EntityMapBuilder is the manual loader (§7.2): a fluent map for types you cannot or will not annotate.
	EntityMapBuilder[T any] = metadata.EntityMapBuilder[T]
	// PropertyConfiguration is the fluent configuration of one property on the builder.
	PropertyConfiguration = metadata.PropertyConfiguration

	// TypeHandler converts one Go type both directions (§7.9).
	TypeHandler[T any] = core.TypeHandler[T]
	// TypeHandlerRegistry is the per-options handler registry.
	TypeHandlerRegistry = core.TypeHandlerRegistry

	// Dialect is the seam a database provider implements (§7.25).
	Dialect = core.Dialect
	// StatementDescriber is the optional dialect capability SchemaGuard needs: describe a statement without executing it.
	StatementDescriber = core.StatementDescriber
	// ColumnDescription is one described result column.
	ColumnDescription = core.ColumnDescription
	// BindCriteriaParameter binds one criteria value and returns its placeholder.
	BindCriteriaParameter = core.BindCriteriaParameter

	// Criteria is a node of the query AST.
	Criteria = core.Criteria
	// SelectJoin is one LEFT JOIN of a select (Level 2 AST, spec/query-ast.md).
	SelectJoin = core.SelectJoin
	// JoinPair is one ON equality of a SelectJoin: parent property = target property.
	JoinPair = core.JoinPair
	// FetchMode chooses how Include fills navigations (spec/loading.md).
	FetchMode = core.FetchMode
	// SelectAst is the criteria query as data.
	SelectAst = core.SelectAst
	// Ordering is one ORDER BY term.
	Ordering = core.Ordering
	// SortOrder is the sort direction token.
	SortOrder = core.SortOrder
)

const (
	RelationTable            = core.RelationTable
	RelationView             = core.RelationView
	RelationMaterializedView = core.RelationMaterializedView
	RelationStatement        = core.RelationStatement
	RelationProcedure        = core.RelationProcedure

	KeyNone              = core.KeyNone
	KeyDatabaseGenerated = core.KeyDatabaseGenerated
	KeyClientGuid        = core.KeyClientGuid
	KeyNatural           = core.KeyNatural

	RelationshipManyToOne  = core.RelationshipManyToOne
	RelationshipOneToMany  = core.RelationshipOneToMany
	RelationshipManyToMany = core.RelationshipManyToMany
	RelationshipOneToOne   = core.RelationshipOneToOne

	TypeInt16          = core.TypeInt16
	TypeInt32          = core.TypeInt32
	TypeInt64          = core.TypeInt64
	TypeDecimal        = core.TypeDecimal
	TypeDouble         = core.TypeDouble
	TypeFloat          = core.TypeFloat
	TypeBool           = core.TypeBool
	TypeString         = core.TypeString
	TypeGUID           = core.TypeGUID
	TypeBytes          = core.TypeBytes
	TypeDateTime       = core.TypeDateTime
	TypeDateTimeOffset = core.TypeDateTimeOffset
	TypeDate           = core.TypeDate
	TypeTime           = core.TypeTime
	TypeEnumText       = core.TypeEnumText
	TypeEnumInt        = core.TypeEnumInt
	TypeCustom         = core.TypeCustom

	// Asc and Desc are the SortOrder tokens; Desc follows the column it applies to in an Index stream.
	Asc  = core.Asc
	Desc = core.Desc
)

// --- descriptor factories (see EntityDefiner) ---

// Table names a table-backed entity.
func Table(name string) Source { return core.Table(name) }

// View names a view and carries its defining SELECT.
func View(name string, sql string) Source { return core.View(name, sql) }

// MaterializedView is a view that may carry indexes; dormant on SQLite.
func MaterializedView(name string, sql string) Source { return core.MaterializedView(name, sql) }

// Statement makes the type the query: inline SQL plus its parameter contract.
func Statement(sql string, parameters ...StatementParameter) Source {
	return core.Statement(sql, parameters...)
}

// Procedure names a stored procedure with its body and parameters; dormant on SQLite.
func Procedure(name string, sql string, parameters ...StatementParameter) Source {
	return core.Procedure(name, sql, parameters...)
}

// Param declares one statement/procedure parameter with the Go type it binds from.
func Param[T any](name string) StatementParameter { return core.Param[T](name) }

// Index declares an index over property names with optional Desc tokens, e.g. Index("Status", "CreatedAtUtc", Desc).
func Index(columns ...any) IndexDef { return core.Index(columns...) }

// ForeignKey declares that the property references T's key.
func ForeignKey[T any](property string) ForeignKeyDef { return core.ForeignKey[T](property) }

// ManyToMany declares that the collection property resolves through the link entity TLink.
func ManyToMany[TLink any](property string) ManyToManyDef { return core.ManyToMany[TLink](property) }

// --- criteria factories (spec/query-ast.md) ---

// Eq is equality; nil renders is null — never = NULL.
func Eq(property string, value any) Criteria { return core.Eq(property, value) }

// Ne is inequality; nil renders is not null.
func Ne(property string, value any) Criteria { return core.Ne(property, value) }

// Gt is a > comparison; unlike Eq/Ne, a nil value is QRY-007.
func Gt(property string, value any) Criteria { return core.Gt(property, value) }

// Ge is a >= comparison; unlike Eq/Ne, a nil value is QRY-007.
func Ge(property string, value any) Criteria { return core.Ge(property, value) }

// Lt is a < comparison; unlike Eq/Ne, a nil value is QRY-007.
func Lt(property string, value any) Criteria { return core.Lt(property, value) }

// Le is a <= comparison; unlike Eq/Ne, a nil value is QRY-007.
func Le(property string, value any) Criteria { return core.Le(property, value) }

// Like is SQL LIKE; the caller supplies the wildcards.
func Like(property string, pattern string) Criteria { return core.Like(property, pattern) }

// In is membership over a typed list: In("ID", ids...) or In("Name", "Ada", "Grace"); empty matches no rows.
func In[T any](property string, values ...T) Criteria { return core.In(property, values...) }

// IsNull is the explicit is-null check.
func IsNull(property string) Criteria { return core.IsNull(property) }

// IsNotNull is the explicit is-not-null check.
func IsNotNull(property string) Criteria { return core.IsNotNull(property) }

// And composes criteria with AND; an empty list renders its identity truth-value (true).
func And(criteria ...Criteria) Criteria { return core.And(criteria...) }

// Or composes criteria with OR; an empty list renders its identity truth-value (false).
func Or(criteria ...Criteria) Criteria { return core.Or(criteria...) }

// Not negates one criteria node.
func Not(criteria Criteria) Criteria { return core.Not(criteria) }

// InSelect is subquery membership (Level 2 AST): properties in (select …).
// Loading builds it and the conformance runner replays it; the chain does not expose it.
func InSelect(properties []string, subquery *core.SelectAst) Criteria {
	return core.InSelect(properties, subquery)
}

// The fetch modes (spec/loading.md): identical graphs, different round trips.
const (
	FetchMultiQuery = core.FetchMultiQuery
	FetchSubSelect  = core.FetchSubSelect
	FetchJoin       = core.FetchJoin
)

// --- value and handler helpers ---

// ParseDecimal reads a decimal literal (digits, optional sign and fraction).
func ParseDecimal(text string) (Decimal, error) { return core.ParseDecimal(text) }

// MustDecimal is ParseDecimal for literals in code; it panics on a malformed literal.
func MustDecimal(text string) Decimal { return core.MustDecimal(text) }

// NewGUID is a random version-4 GUID.
func NewGUID() GUID { return core.NewGUID() }

// ParseGUID reads the hyphenated or 32-digit form.
func ParseGUID(text string) (GUID, error) { return core.ParseGUID(text) }

// NewTypeHandlerRegistry is an empty handler registry for Options.
func NewTypeHandlerRegistry() *TypeHandlerRegistry { return core.NewTypeHandlerRegistry() }

// RegisterHandler registers a handler for T on the registry and returns it for chaining.
func RegisterHandler[T any](registry *TypeHandlerRegistry, handler TypeHandler[T]) *TypeHandlerRegistry {
	return core.RegisterHandler(registry, handler)
}

// RegisterJSON registers T as a JSON column (TEXT holding JSON, §7.10): snake_case names, case-insensitive, numbers readable from strings.
func RegisterJSON[T any](registry *TypeHandlerRegistry) *TypeHandlerRegistry {
	return mapping.RegisterJSON[T](registry)
}

// NewEntityMapBuilder starts a manual map for T; register it on MappingOptions to override tags and conventions.
func NewEntityMapBuilder[T any]() *EntityMapBuilder[T] { return metadata.NewEntityMapBuilder[T]() }

// ExportEntityMap is the conformance JSON of a map (spec/metadata-model.md),
// byte-identical across ports; maps (db.Maps()) resolves the related entities
// whose columns the export names (ADR-0029).
func ExportEntityMap(m *EntityMap, maps *metadata.Loader) (string, error) {
	return metadata.Export(m, maps)
}

// CodeOf returns the stable code an error carries ("" when none).
func CodeOf(err error) string { return core.CodeOf(err) }

// HasCode reports whether an error carries the code, directly or inside an aggregate report.
func HasCode(err error, code string) bool { return core.HasCode(err, code) }

// TypeOf is reflect.TypeFor spelled for registries: Entities: []reflect.Type{orm.TypeOf[User]()}.
func TypeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

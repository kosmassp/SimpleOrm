package mapping

import (
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// ResultMapper is the one row-mapping pipeline (§7.11, mirrors
// dotnet/src/SimpleOrm/ResultMapper.cs for database/sql): a Plan is built once
// per (result type, column set) and cached, shared by raw SQL, statement
// entities, and generated reads. Strictness matches the reference exactly: an
// entity result (a type with mapping declarations) must match its EntityMap
// exactly both ways (MAP-001/002); a DTO matches columns to exported fields
// case- and underscore-insensitively, and an unbound non-pointer field is
// MAP-002 (the Go "required" rule, CODING-STANDARD §10). Go has no
// constructors, so MAP-003 never comes from this package.
type ResultMapper struct {
	loader    *metadata.Loader
	converter *TypeConverter
	plans     sync.Map // reflect.Type -> *planSet
	// markers judges a column's driver-reported type for the VAL-020 read rule on databases whose
	// native temporals carry no marker (spec/dialects.md "Temporals"); nil on SQLite, whose TEXT
	// temporals carry their own marker.
	markers core.TemporalMarkers
}

// NewResultMapper builds a mapper sharing loader's metadata cache and converter's conversion rules.
func NewResultMapper(loader *metadata.Loader, converter *TypeConverter) *ResultMapper {
	return &ResultMapper{loader: loader, converter: converter}
}

// UseTemporalMarkers sets the dialect capability that judges markerless temporal
// columns (core.TemporalMarkers); call it once, before the first Plan. A mapper
// without one reads temporals exactly as before.
func (m *ResultMapper) UseTemporalMarkers(judge core.TemporalMarkers) { m.markers = judge }

// binding is one result column bound to a target. For an entity or a DTO, the
// target is an ad-hoc *core.PropertyMap — the loader's own for an entity, one
// built on the fly wrapping a plain struct field for a DTO — so Get/Set is the
// one implementation either way (CODING-STANDARD §8). A scalar result has no
// property; columnType alone drives its conversion.
type binding struct {
	property   *core.PropertyMap
	columnType core.ColumnType
	context    string
}

// Plan is a compiled, cached row-reading plan for one (result type, column set) pair.
type Plan struct {
	resultType reflect.Type
	structType reflect.Type
	isPointer  bool
	scalar     bool
	bindings   []binding
	converter  *TypeConverter
	// markerChecks are the binding indices read into a datetime/datetimeoffset without a registered
	// handler, judged against the column's database type at read time; empty without a judge.
	markerChecks []int
	markers      core.TemporalMarkers
}

// Plan returns the plan for resultType against columns, building and caching
// it on first use (so MAP-001/002 fire even for an empty result set, since the
// caller builds the plan from the schema before reading any row).
//
// The cache key is structural (ADR-0048 item 6): the result type by identity, then the column names compared one by
// one against the reader's — a hit builds no string. (It is stricter than the joined-string key it replaces only in
// that two distinct types printing alike, or column lists that join alike, no longer share a plan.)
func (m *ResultMapper) Plan(resultType reflect.Type, columns []string, queryName string) (*Plan, error) {
	set := m.planSet(resultType)
	if plan := set.find(columns); plan != nil {
		return plan, nil
	}

	plan, err := m.buildPlan(resultType, columns, queryName)
	if err != nil {
		return nil, err
	}
	return set.store(columns, plan), nil
}

// planSet is one result type's plans by column list: an immutable slice replaced on write (copy-on-write), so a
// lookup takes no lock.
type planSet struct {
	mu      sync.Mutex
	entries atomic.Pointer[[]planEntry]
}

type planEntry struct {
	columns []string
	plan    *Plan
}

func (m *ResultMapper) planSet(resultType reflect.Type) *planSet {
	if set, ok := m.plans.Load(resultType); ok {
		return set.(*planSet)
	}
	set, _ := m.plans.LoadOrStore(resultType, &planSet{})
	return set.(*planSet)
}

func (s *planSet) find(columns []string) *Plan {
	if entries := s.entries.Load(); entries != nil {
		for _, e := range *entries {
			if slices.Equal(e.columns, columns) {
				return e.plan
			}
		}
	}
	return nil
}

// store keeps plan for columns unless another caller stored one first, and returns the one kept.
func (s *planSet) store(columns []string, plan *Plan) *Plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.find(columns); existing != nil {
		return existing
	}
	var next []planEntry
	if entries := s.entries.Load(); entries != nil {
		next = append(next, *entries...)
	}
	next = append(next, planEntry{columns: slices.Clone(columns), plan: plan})
	s.entries.Store(&next)
	return plan
}

func (m *ResultMapper) buildPlan(resultType reflect.Type, columns []string, queryName string) (*Plan, error) {
	if IsScalarType(resultType, m.converter) {
		if len(columns) == 0 {
			return nil, core.Errorf("MAP-002", queryName, "%s expects one result column, the result has none", resultType)
		}
		plan := &Plan{
			resultType: resultType,
			scalar:     true,
			converter:  m.converter,
			bindings:   []binding{{columnType: core.ColumnTypeOf(resultType), context: queryName + " → " + resultType.String()}},
		}
		m.planMarkerChecks(plan)
		return plan, nil
	}

	structType := resultType
	isPointer := false
	if structType.Kind() == reflect.Pointer {
		isPointer = true
		structType = structType.Elem()
	}

	var bindings []binding
	var err error
	if metadata.HasMappingDeclarations(structType) {
		bindings, err = m.entityBindings(structType, columns, queryName)
	} else {
		bindings, err = dtoBindings(structType, columns, queryName)
	}
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		resultType: resultType,
		structType: structType,
		isPointer:  isPointer,
		converter:  m.converter,
		bindings:   bindings,
	}
	m.planMarkerChecks(plan)
	return plan, nil
}

// planMarkerChecks marks the bindings the VAL-020 database-type rule applies to: a datetime or
// datetimeoffset token read without a registered handler (handlers win over the fixed table — the
// documented escape for legacy columns). date and time carry no kind by design and never refuse.
func (m *ResultMapper) planMarkerChecks(plan *Plan) {
	if m.markers == nil {
		return
	}
	plan.markers = m.markers
	for i, b := range plan.bindings {
		columnType, target := b.columnType, plan.resultType
		if b.property != nil {
			columnType, target = b.property.ColumnType, b.property.Type
		}
		if (columnType == core.TypeDateTime || columnType == core.TypeDateTimeOffset) && !m.converter.HasHandler(target) {
			plan.markerChecks = append(plan.markerChecks, i)
		}
	}
}

// entityBindings is the §7.7 strictness rule: every result column resolves to
// a mapped property (case-insensitive, MAP-001 otherwise) and every mapped
// property is consumed by some column (MAP-002 otherwise).
func (m *ResultMapper) entityBindings(structType reflect.Type, columns []string, queryName string) ([]binding, error) {
	entityMap, err := m.loader.Load(structType)
	if err != nil {
		return nil, err
	}

	bindings := make([]binding, len(columns))
	matched := make(map[*core.PropertyMap]bool, len(entityMap.Properties))
	for i, column := range columns {
		property := propertyByColumnFold(entityMap, column)
		if property == nil {
			return nil, core.Errorf("MAP-001", queryName,
				"result column '%s' has no mapped property on %s", column, structType.Name())
		}
		matched[property] = true
		bindings[i] = binding{property: property, context: queryName + " → " + structType.Name() + "." + property.PropertyName}
	}

	var missing []string
	for _, property := range entityMap.Properties {
		if !matched[property] {
			missing = append(missing, property.ColumnName)
		}
	}
	if len(missing) > 0 {
		return nil, core.Errorf("MAP-002", queryName,
			"%s expects column(s) %s which the result does not contain", structType.Name(), strings.Join(missing, ", "))
	}

	return bindings, nil
}

// dtoBindings matches columns to exported fields case- and
// underscore-insensitively; an unbound non-pointer exported field is MAP-002
// (CODING-STANDARD §10 — the Go "required" rule). Embedded structs contribute
// their promoted fields (the container field itself is skipped: it is never
// itself a bindable column target).
func dtoBindings(structType reflect.Type, columns []string, queryName string) ([]binding, error) {
	var fields []reflect.StructField
	for _, field := range reflect.VisibleFields(structType) {
		if field.IsExported() && !field.Anonymous {
			fields = append(fields, field)
		}
	}

	bindings := make([]binding, len(columns))
	bound := map[string]bool{}
	for i, column := range columns {
		field, found := findDTOField(fields, column)
		if !found {
			return nil, core.Errorf("MAP-001", queryName, "result column '%s' matches no field on %s", column, structType.Name())
		}
		property := &core.PropertyMap{
			Field:         field,
			Index:         field.Index,
			DeclaringType: structType,
			PropertyName:  field.Name,
			ColumnName:    column,
			Type:          field.Type,
			ColumnType:    core.ColumnTypeOf(field.Type),
		}
		bindings[i] = binding{property: property, context: queryName + " → " + structType.Name() + "." + field.Name}
		bound[fieldKey(field.Index)] = true
	}

	var missing []string
	for _, field := range fields {
		if bound[fieldKey(field.Index)] {
			continue
		}
		if field.Type.Kind() != reflect.Pointer {
			missing = append(missing, field.Name)
		}
	}
	if len(missing) > 0 {
		return nil, core.Errorf("MAP-002", queryName,
			"required field(s) %s of %s have no matching result column", strings.Join(missing, ", "), structType.Name())
	}

	return bindings, nil
}

// findDTOField matches a column to a field case- and underscore-insensitively
// (created_at ↔ CreatedAt), mirroring ResultMapper.cs's NamesMatch.
func findDTOField(fields []reflect.StructField, column string) (reflect.StructField, bool) {
	for _, field := range fields {
		if NamesMatch(field.Name, column) {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

// NamesMatch is the DTO name-matching rule (case- and underscore-insensitive,
// created_at ↔ CreatedAt), mirroring ResultMapper.cs's NamesMatch. Exported so
// SchemaGuard's independent column-resolution walk (orm/schema_guard.go) uses
// the one implementation instead of its own copy (CODING-STANDARD §8).
func NamesMatch(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "_", ""), strings.ReplaceAll(b, "_", ""))
}

func fieldKey(index []int) string {
	parts := make([]string, len(index))
	for i, v := range index {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ".")
}

// propertyByColumnFold is EntityMap.PropertyByColumn's case-insensitive form
// (spec/mapping-rules.md: entity column matching is case-insensitive, unlike a
// DTO's underscore-insensitive match).
func propertyByColumnFold(m *core.EntityMap, column string) *core.PropertyMap {
	for _, property := range m.Properties {
		if strings.EqualFold(property.ColumnName, column) {
			return property
		}
	}
	return nil
}

// Read scans one row and returns a value of the plan's result type.
func (p *Plan) Read(rows *sql.Rows) (any, error) {
	raw := make([]any, len(p.bindings))
	scanArgs := make([]any, len(raw))
	for i := range raw {
		scanArgs[i] = &raw[i]
	}
	if err := rows.Scan(scanArgs...); err != nil {
		return nil, err
	}
	var types []*sql.ColumnType
	if len(p.markerChecks) > 0 {
		var err error
		if types, err = rows.ColumnTypes(); err != nil {
			return nil, err
		}
	}
	return p.ReadValuesTyped(raw, types)
}

// ReadValues assembles a result from column values read elsewhere, in the
// same order and count as the plan's bindings — Read's post-scan logic,
// factored out as the entry point join-mode eager loading uses
// (orm/db_eager_join.go): one joined row is scanned once, then each entity
// segment (the root, and each projected navigation) calls ReadValues on its
// own slice of that row's already-read values, sized and ordered exactly like
// the plan built for that segment's stripped column names. Read and
// ReadValues share every conversion rule (owned-member nullability included);
// this addition does not change Read's behavior.
func (p *Plan) ReadValues(raw []any) (any, error) { return p.ReadValuesTyped(raw, nil) }

// ReadValuesTyped is ReadValues with the driver-reported column types of the
// same values, in the same order: where the dialect judges temporals by their
// column type (core.TemporalMarkers), a non-NULL value read from a markerless
// column is VAL-020 before any conversion. types may be nil (nothing judged).
func (p *Plan) ReadValuesTyped(raw []any, types []*sql.ColumnType) (any, error) {
	if len(raw) != len(p.bindings) {
		return nil, fmt.Errorf("simpleorm: ReadValues got %d value(s), the plan expects %d", len(raw), len(p.bindings))
	}
	if types != nil {
		for _, i := range p.markerChecks {
			if raw[i] == nil || i >= len(types) {
				continue
			}
			if name := types[i].DatabaseTypeName(); p.markers.IsMarkerlessTemporal(name) {
				return nil, core.Errorf("VAL-020", p.bindings[i].context,
					"the %s column stores datetimes without a UTC/offset marker; declare it with time zone (timestamptz / datetimeoffset), or register a TypeHandler for the property to read a legacy column", name)
			}
		}
	}

	if p.scalar {
		b := p.bindings[0]
		converted, err := p.converter.FromDatabase(raw[0], p.resultType, b.columnType, b.context)
		if err != nil {
			return nil, err
		}
		return wrapPointer(converted, p.resultType), nil
	}

	instance := reflect.New(p.structType)
	// Owned members (ADR-0030): a nullable navigation whose member columns are
	// all NULL stays nil — the row said "no value"; Set allocates it otherwise.
	skipOwner := map[*core.OwnedMap]bool{}
	for i, b := range p.bindings {
		owner := b.property.Owner
		if owner != nil && owner.IsNullable {
			if _, decided := skipOwner[owner]; !decided {
				skipOwner[owner] = allNull(p.bindings, raw, owner)
			}
			if skipOwner[owner] {
				continue
			}
		}
		converted, err := p.converter.FromDatabase(raw[i], b.property.Type, b.property.ColumnType, b.context)
		if err != nil {
			return nil, err
		}
		if err := b.property.Set(instance.Interface(), converted); err != nil {
			return nil, err
		}
	}
	if p.isPointer {
		return instance.Interface(), nil
	}
	return instance.Elem().Interface(), nil
}

// allNull reports whether every column bound to owner's members is NULL in raw.
func allNull(bindings []binding, raw []any, owner *core.OwnedMap) bool {
	for i, b := range bindings {
		if b.property.Owner == owner && raw[i] != nil {
			return false
		}
	}
	return true
}

// wrapPointer turns FromDatabase's element-typed result into target's exact
// type when target is a pointer (a nullable scalar result): NULL becomes a
// typed nil pointer, a value is copied behind a new pointer. FromDatabase
// itself only ever returns the element value (or untyped nil), documented as
// the result mapper's job to wrap.
func wrapPointer(value any, target reflect.Type) any {
	if target.Kind() != reflect.Pointer {
		return value
	}
	if value == nil {
		return reflect.Zero(target).Interface()
	}
	pointer := reflect.New(target.Elem())
	pointer.Elem().Set(reflect.ValueOf(value))
	return pointer.Interface()
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	decimalType = reflect.TypeFor[core.Decimal]()
	guidType    = reflect.TypeFor[core.GUID]()
)

// IsScalarType is the §7 "raw" result shape: a value read directly from
// column 0, never through an entity or a DTO — the fixed table's types, an
// orm.Enum, a registered handler type, and pointers to any of these. Exported
// so SchemaGuard's per-column origin/nullability walk (orm/schema_guard.go)
// shares this classification instead of a second copy (CODING-STANDARD §8).
func IsScalarType(t reflect.Type, converter *TypeConverter) bool {
	element := t
	if element.Kind() == reflect.Pointer {
		element = element.Elem()
	}
	if converter.HasHandler(element) {
		return true
	}
	switch element {
	case decimalType, guidType, timeType:
		return true
	}
	if core.IsEnumType(element) {
		return true
	}
	switch element.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	case reflect.Slice:
		return element.Elem().Kind() == reflect.Uint8
	}
	return false
}

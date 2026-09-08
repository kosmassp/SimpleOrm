// Package metadata: the declaration loader. Named after
// dotnet/src/SimpleOrm/AttributeMapLoader.cs so a reader of one implementation
// finds the other by name, even though Go has no attributes: it reads the
// `orm` struct tag (CODING-STANDARD §10, "The orm tag, precisely") plus the
// optional EntityDef descriptor (entity_def.go), and produces the working
// specs that map_assembler.go turns into an EntityMap. Every violation is
// collected before returning (spec/metadata-model.md).
package metadata

import (
	"reflect"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// loadFromDeclarations is the Loader.load entry point for a type carrying any
// mapping declaration (an `orm` tag or an Entity() descriptor) — precedence
// tier two, between the explicit registration and the convention loader
// (§7.2).
func loadFromDeclarations(l *Loader, t reflect.Type) (*core.EntityMap, error) {
	convention := l.options.Convention()
	errs := &errorCollector{entityType: t}

	def, hasDescriptor := Descriptor(t)
	kind, relationName, schema, sql, declaredParams := resolveSource(t, def, hasDescriptor, convention, errs)

	specs, relationships := readProperties(t, kind, errs)

	byPropertyName := map[string]*MappedPropertySpec{}
	for _, spec := range specs {
		byPropertyName[spec.PropertyName] = spec
	}
	if hasDescriptor {
		applyForeignKeyReferences(t, def.ForeignKeys, byPropertyName, errs)
	}

	indexSpecs := readIndexStreamShape(t, kind, def, hasDescriptor, errs)

	if strings.TrimSpace(sql) != "" {
		checkStatementPlaceholders(t, sql, declaredParams, errs)
	}

	return assemble(t, kind, relationName, schema, sql, declaredParams, specs, indexSpecs, relationships, convention, errs)
}

// resolveSource reads the descriptor's relation source (ADR-0008): a type
// with tags but no descriptor is a convention-named table (CODING-STANDARD
// §10). MAP-012 ("two relation sources") is unreachable here — EntityDef.Source
// is one field.
func resolveSource(
	t reflect.Type, def core.EntityDef, hasDescriptor bool, convention core.NamingConvention, errs *errorCollector,
) (kind core.RelationKind, name, schema, sql string, params []core.StatementParameter) {
	if !hasDescriptor {
		return core.RelationTable, convention.TableName(t.Name()), "", "", nil
	}

	source := def.Source
	kind = source.Kind
	name = source.Name
	schema = source.Schema
	sql = source.SQL
	params = source.Parameters

	if kind == core.RelationTable && name == "" {
		name = convention.TableName(t.Name())
	}

	if kind != core.RelationTable && strings.TrimSpace(sql) == "" {
		errs.add("MAP-019", t.Name(), "the %s defining SQL is empty", kind)
	}

	if kind == core.RelationStatement || kind == core.RelationProcedure {
		params = dedupeParameters(t, params, errs)
	}

	return kind, name, schema, sql, params
}

// dedupeParameters is MAP-017's only reachable sub-case in Go (CODING-STANDARD
// §10): the declaration is a typed name→type list, so "odd token count" and
// "token neither name string nor Type" cannot occur; only a repeated name can.
func dedupeParameters(t reflect.Type, params []core.StatementParameter, errs *errorCollector) []core.StatementParameter {
	seen := map[string]bool{}
	result := make([]core.StatementParameter, 0, len(params))
	for _, p := range params {
		if seen[p.Name] {
			errs.add("MAP-017", t.Name(), "duplicate parameter name '%s'", p.Name)
			continue
		}
		seen[p.Name] = true
		result = append(result, p)
	}
	return result
}

// checkStatementPlaceholders is PRM-010/011: every @placeholder in the
// defining SQL must be declared, and every declared parameter must be used
// (names match case-insensitively). Applies to every source that carries SQL,
// including views (their declared parameter list is always empty, so any
// placeholder in a view's defining SELECT is PRM-010).
func checkStatementPlaceholders(t reflect.Type, sql string, declared []core.StatementParameter, errs *errorCollector) {
	name := t.Name()
	placeholders := core.FindPlaceholders(sql)
	for _, placeholder := range placeholders {
		found := false
		for _, p := range declared {
			if strings.EqualFold(p.Name, placeholder) {
				found = true
				break
			}
		}
		if !found {
			errs.add("PRM-010", name, "SQL uses @%s, which is not declared", placeholder)
		}
	}
	for _, p := range declared {
		used := false
		for _, placeholder := range placeholders {
			if strings.EqualFold(p.Name, placeholder) {
				used = true
				break
			}
		}
		if !used {
			errs.add("PRM-011", name, "declared parameter '%s' is never used by the SQL", p.Name)
		}
	}
}

// applyForeignKeyReferences sets PropertyMap.ForeignKeyReferences from the
// descriptor's EntityDef.ForeignKeys (the Go analog of a per-field
// [ForeignKey(typeof(T))]; CODING-STANDARD §10): a name the specs don't carry
// is MAP-016, the same code the many_to_one FK-property check uses.
func applyForeignKeyReferences(
	t reflect.Type, foreignKeys []core.ForeignKeyDef, byPropertyName map[string]*MappedPropertySpec, errs *errorCollector,
) {
	for _, fk := range foreignKeys {
		spec, ok := byPropertyName[fk.Property]
		if !ok {
			errs.add("MAP-016", target(t, fk.Property), "ForeignKey names '%s', which is not a mapped property", fk.Property)
			continue
		}
		spec.ForeignKeyReferences = fk.References
	}
}

// readIndexStreamShape validates each declared index's token-stream shape
// (MAP-014/015); property resolution (unknown/unmapped property) happens at
// assembly, where every spec's column name is known (map_assembler.go).
func readIndexStreamShape(t reflect.Type, kind core.RelationKind, def core.EntityDef, hasDescriptor bool, errs *errorCollector) []*IndexSpec {
	if !hasDescriptor || len(def.Indexes) == 0 {
		return nil
	}

	if kind != core.RelationTable && kind != core.RelationMaterializedView {
		errs.add("MAP-014", t.Name(), "an index is only valid on tables and materialized views, not a %s", kind)
		return nil
	}

	name := t.Name() + " index"
	specs := make([]*IndexSpec, 0, len(def.Indexes))
	for _, index := range def.Indexes {
		var columns []IndexColumnSpec
		valid := true
		for _, token := range index.Columns {
			switch v := token.(type) {
			case string:
				columns = append(columns, IndexColumnSpec{PropertyName: v})
			case core.SortOrder:
				if len(columns) == 0 {
					errs.add("MAP-015", name, "%s has no preceding column to apply to", v)
					valid = false
					continue
				}
				columns[len(columns)-1].Descending = v == core.Desc
			default:
				errs.add("MAP-015", name, "token '%v' is neither a property name string nor a SortOrder", token)
				valid = false
			}
		}

		for i := 1; i < len(index.Columns); i++ {
			_, prevIsOrder := index.Columns[i-1].(core.SortOrder)
			_, curIsOrder := index.Columns[i].(core.SortOrder)
			if prevIsOrder && curIsOrder {
				errs.add("MAP-015", name, "two consecutive SortOrder tokens; each applies to the column before it")
				valid = false
			}
		}

		if len(columns) == 0 {
			errs.add("MAP-015", name, "the column list is empty")
			valid = false
		}

		if !valid {
			continue
		}

		var indexName *string
		if index.Name != "" {
			n := index.Name
			indexName = &n
		}
		specs = append(specs, &IndexSpec{Name: indexName, Unique: index.IsUnique, Columns: columns})
	}
	return specs
}

// --- field discovery --------------------------------------------------------

// discoveredField is one exported field on the way to becoming a spec: its
// full index path from the entity type and the struct that declares it.
type discoveredField struct {
	Field         reflect.StructField
	Index         []int
	DeclaringType reflect.Type
}

// discoverFields walks exported fields in the order both loaders map them
// (CODING-STANDARD §10, mirroring the C# "most-derived first" rule): the
// struct's own fields first in declaration order, then each embedded struct's
// fields, recursively. Unexported fields are invisible. An embedded pointer is
// MAP-023 — Go structs cannot embed themselves by value, so no cycle guard is
// needed.
func discoverFields(t reflect.Type) ([]discoveredField, []*core.Error) {
	var own []discoveredField
	var embedded []discoveredField
	var errs []*core.Error

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Anonymous {
			fieldType := field.Type
			if fieldType.Kind() == reflect.Pointer {
				errs = append(errs, core.Errorf("MAP-023", target(t, field.Name),
					"an embedded pointer field is not supported; embed %s by value", fieldType.Elem().Name()))
				continue
			}
			if fieldType.Kind() == reflect.Struct {
				sub, subErrs := discoverFields(fieldType)
				errs = append(errs, subErrs...)
				for _, s := range sub {
					index := make([]int, 0, len(s.Index)+1)
					index = append(index, i)
					index = append(index, s.Index...)
					embedded = append(embedded, discoveredField{Field: s.Field, Index: index, DeclaringType: s.DeclaringType})
				}
				continue
			}
		}
		own = append(own, discoveredField{Field: field, Index: []int{i}, DeclaringType: t})
	}

	return append(own, embedded...), errs
}

func pointerToStructElem(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() != reflect.Pointer || t.Elem().Kind() != reflect.Struct {
		return nil, false
	}
	return t.Elem(), true
}

// sliceEntityElem is the entity element type of a slice field: []T or []*T
// where T is a struct (a collection navigation's shape, MAP-020).
func sliceEntityElem(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() != reflect.Slice {
		return nil, false
	}
	elem := t.Elem()
	if elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return nil, false
	}
	return elem, true
}

// --- tag parsing --------------------------------------------------------

// parsedRelationship is one relationship option found while parsing a field's
// `orm` tag, before shape validation.
type parsedRelationship struct {
	kind         core.RelationshipKind
	fkProperties []string // nil for many_to_many, resolved through the descriptor instead
}

// fieldDeclaration is the parsed `orm` tag of one field (CODING-STANDARD §10,
// "The orm tag, precisely"), before the combination rules (MAP-019) and shape
// rules (MAP-020/021/022) validate it.
type fieldDeclaration struct {
	isColumn   bool
	columnName *string
	isIgnore   bool

	isKey        bool
	isGenerated  bool
	isVersion    bool
	isEnumInt    bool
	typeOverride *core.ColumnType

	// isOwned is the `owned` option (ADR-0030); ownedPrefix its `owned=<prefix>`
	// value when given (an empty value disables prefixing), nil for the default.
	isOwned     bool
	ownedPrefix *string

	relationships []parsedRelationship
}

// parseFieldTag tokenizes one `orm` tag value. An unknown option, an unknown
// `type=` token, or an empty token (a malformed tag) is MAP-023 (ADR-0027).
func parseFieldTag(tagValue string, fieldTarget string, errs *errorCollector) fieldDeclaration {
	var decl fieldDeclaration
	for _, raw := range strings.Split(tagValue, ",") {
		token := strings.TrimSpace(raw)
		switch {
		case token == "":
			errs.add("MAP-023", fieldTarget, "malformed orm tag: empty option in '%s'", tagValue)
		case token == "ignore":
			decl.isIgnore = true
		case token == "column":
			decl.isColumn = true
		case strings.HasPrefix(token, "column="):
			name := token[len("column="):]
			decl.isColumn = true
			decl.columnName = &name
		case token == "key":
			decl.isKey = true
		case token == "generated":
			decl.isGenerated = true
		case token == "version":
			decl.isVersion = true
		case token == "enum_int":
			decl.isEnumInt = true
		case strings.HasPrefix(token, "type="):
			columnType, ok := core.ParseColumnType(token[len("type="):])
			if !ok {
				errs.add("MAP-023", fieldTarget, "unknown type token '%s'", token[len("type="):])
				continue
			}
			decl.typeOverride = &columnType
		case token == "owned":
			decl.isOwned = true
		case strings.HasPrefix(token, "owned="):
			prefix := token[len("owned="):]
			decl.isOwned = true
			decl.ownedPrefix = &prefix
		case token == "many_to_many":
			decl.relationships = append(decl.relationships, parsedRelationship{kind: core.RelationshipManyToMany})
		case strings.HasPrefix(token, "many_to_one="):
			decl.relationships = append(decl.relationships, parsedRelationship{
				kind: core.RelationshipManyToOne, fkProperties: splitFKList(token[len("many_to_one="):]),
			})
		case strings.HasPrefix(token, "one_to_many="):
			decl.relationships = append(decl.relationships, parsedRelationship{
				kind: core.RelationshipOneToMany, fkProperties: splitFKList(token[len("one_to_many="):]),
			})
		case strings.HasPrefix(token, "one_to_one="):
			decl.relationships = append(decl.relationships, parsedRelationship{
				kind: core.RelationshipOneToOne, fkProperties: splitFKList(token[len("one_to_one="):]),
			})
		default:
			errs.add("MAP-023", fieldTarget, "unknown orm tag option '%s'", token)
		}
	}
	return decl
}

func splitFKList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, "+")
}

// --- field-level reading --------------------------------------------------------

// readProperties reads every field's `orm` tag into a spec or a relationship
// (the C# AttributeMapLoader.ReadProperties).
func readProperties(t reflect.Type, kind core.RelationKind, errs *errorCollector) ([]*MappedPropertySpec, []*RelationshipSpec) {
	fields, discErrs := discoverFields(t)
	errs.errors = append(errs.errors, discErrs...)

	var specs []*MappedPropertySpec
	var relationships []*RelationshipSpec
	for _, df := range fields {
		readField(t, kind, df, errs, &specs, &relationships)
	}
	return specs, relationships
}

func readField(
	entityType reflect.Type, kind core.RelationKind, df discoveredField,
	errs *errorCollector, specs *[]*MappedPropertySpec, relationships *[]*RelationshipSpec,
) {
	fieldTarget := target(entityType, df.Field.Name)
	tagValue, hasTag := df.Field.Tag.Lookup(TagName)
	if !hasTag {
		errs.add("MAP-010", fieldTarget, "an exported field must carry an orm tag (column, ignore, or a relationship) on a type with mapping declarations")
		return
	}

	decl := parseFieldTag(tagValue, fieldTarget, errs)

	if decl.isOwned {
		if decl.isColumn || decl.isIgnore || decl.isKey || decl.isGenerated || decl.isVersion ||
			decl.isEnumInt || decl.typeOverride != nil || len(decl.relationships) > 0 {
			errs.add("MAP-019", fieldTarget, "owned cannot combine with any other mapping option")
			return
		}
		readOwnedType(df, fieldTarget, decl.ownedPrefix, errs, specs)
		return
	}

	if len(decl.relationships) > 1 {
		errs.add("MAP-019", fieldTarget, "a property carries at most one relationship declaration")
		return
	}
	if len(decl.relationships) == 1 {
		if decl.isColumn || decl.isIgnore {
			errs.add("MAP-019", fieldTarget, "a navigation cannot combine with column or ignore")
		}
		readRelationshipField(entityType, df, fieldTarget, decl.relationships[0], errs, relationships)
		return
	}

	if decl.isIgnore {
		if decl.isColumn {
			errs.add("MAP-019", fieldTarget, "ignore cannot combine with column")
		}
		return
	}

	if !decl.isColumn {
		if decl.isKey || decl.isGenerated || decl.isVersion || decl.isEnumInt || decl.typeOverride != nil {
			errs.add("MAP-019", fieldTarget, "mapping options require 'column' on the same field")
		}
		return
	}

	if decl.isEnumInt {
		valueType := df.Field.Type
		if valueType.Kind() == reflect.Pointer {
			valueType = valueType.Elem()
		}
		if !core.IsEnumType(valueType) {
			errs.add("MAP-023", fieldTarget, "enum_int requires an orm.Enum type, found %s", valueType)
		}
	}

	if kind != core.RelationTable && (decl.isGenerated || decl.isVersion) {
		errs.add("MAP-013", fieldTarget, "generated/version are only valid on a table-backed entity, not a %s", kind)
	}
	if decl.isKey && (kind == core.RelationStatement || kind == core.RelationProcedure) {
		errs.add("MAP-013", fieldTarget, "key is not valid on a %s-backed entity", kind)
	}

	*specs = append(*specs, &MappedPropertySpec{
		Field:              df.Field,
		Index:              df.Index,
		DeclaringType:      df.DeclaringType,
		PropertyName:       df.Field.Name,
		ExplicitColumn:     decl.columnName,
		ColumnTypeOverride: decl.typeOverride,
		IsKey:              decl.isKey,
		IsGenerated:        decl.isGenerated,
		IsVersion:          decl.isVersion,
		EnumAsInt:          decl.isEnumInt,
	})
}

// readOwnedType reads an `owned` value type (ADR-0030): a struct (or pointer
// to one) implementing core.OwnedType, whose `column` fields flatten into the
// owner under the navigation's prefix. The owned type is not an entity — an
// Entity() descriptor, key, version, generated column, relationship, or
// nested owned inside it is MAP-024; the tag rule (MAP-010) applies to its
// fields exactly as to an entity's. Go structs need no constructor.
func readOwnedType(
	df discoveredField, fieldTarget string, explicitPrefix *string,
	errs *errorCollector, specs *[]*MappedPropertySpec,
) {
	ownedType := df.Field.Type
	isNullable := ownedType.Kind() == reflect.Pointer
	if isNullable {
		ownedType = ownedType.Elem()
	}
	if ownedType.Kind() != reflect.Struct || ownedType == timeStructType {
		errs.add("MAP-024", fieldTarget, "an owned navigation must be a struct or a pointer to one; collections and scalars cannot be owned")
		return
	}
	if _, isEntity := Descriptor(ownedType); isEntity {
		errs.add("MAP-024", fieldTarget, "'%s' is an entity (it declares Entity()); an owned type has no table of its own", ownedType.Name())
		return
	}
	if !core.IsOwnedType(ownedType) {
		errs.add("MAP-024", fieldTarget, "'%s' must itself implement orm.OwnedType — that is what keeps it out of the entity set", ownedType.Name())
		return
	}

	spec := &OwnedSpec{Field: df.Field, Index: df.Index, OwnedType: ownedType, IsNullable: isNullable, ExplicitPrefix: explicitPrefix}
	members, discErrs := discoverFields(ownedType)
	errs.errors = append(errs.errors, discErrs...)
	mapped := 0
	for _, member := range members {
		memberTarget := target(ownedType, member.Field.Name)
		tagValue, hasTag := member.Field.Tag.Lookup(TagName)
		if !hasTag {
			errs.add("MAP-010", memberTarget, "an exported field of an owned type must carry an orm tag (column or ignore)")
			continue
		}
		decl := parseFieldTag(tagValue, memberTarget, errs)
		if decl.isKey || decl.isGenerated || decl.isVersion || decl.isOwned || len(decl.relationships) > 0 {
			errs.add("MAP-024", memberTarget, "an owned type's fields carry only column, enum_int, type=, or ignore: no key, version, generated column, relationship, or nested owned")
			continue
		}
		if decl.isIgnore {
			if decl.isColumn {
				errs.add("MAP-019", memberTarget, "ignore cannot combine with column")
			}
			continue
		}
		if !decl.isColumn {
			if decl.isEnumInt || decl.typeOverride != nil {
				errs.add("MAP-019", memberTarget, "mapping options require 'column' on the same field")
			}
			continue
		}
		if decl.isEnumInt {
			valueType := member.Field.Type
			if valueType.Kind() == reflect.Pointer {
				valueType = valueType.Elem()
			}
			if !core.IsEnumType(valueType) {
				errs.add("MAP-023", memberTarget, "enum_int requires an orm.Enum type, found %s", valueType)
			}
		}
		*specs = append(*specs, &MappedPropertySpec{
			Field:              member.Field,
			Index:              member.Index,
			DeclaringType:      member.DeclaringType,
			PropertyName:       member.Field.Name,
			ExplicitColumn:     decl.columnName,
			ColumnTypeOverride: decl.typeOverride,
			EnumAsInt:          decl.isEnumInt,
			Owner:              spec,
		})
		mapped++
	}
	if mapped == 0 {
		errs.add("MAP-024", fieldTarget, "'%s' maps no columns; an owned type needs at least one column field", ownedType.Name())
	}
}

var timeStructType = reflect.TypeFor[time.Time]()

// readRelationshipField resolves one navigation's shape (MAP-020) and its
// foreign-key declaration; arity checks against the other side's key run at
// assembly, once every spec in this type is known (map_assembler.go).
func readRelationshipField(
	entityType reflect.Type, df discoveredField, fieldTarget string, rel parsedRelationship,
	errs *errorCollector, relationships *[]*RelationshipSpec,
) {
	switch rel.kind {
	case core.RelationshipManyToOne:
		targetType, ok := pointerToStructElem(df.Field.Type)
		if !ok {
			errs.add("MAP-020", fieldTarget, "many_to_one must be a pointer to a struct")
			return
		}
		if len(rel.fkProperties) == 0 {
			errs.add("MAP-016", fieldTarget, "many_to_one needs at least one foreign-key property")
			return
		}
		*relationships = append(*relationships, &RelationshipSpec{
			PropertyName: df.Field.Name, Kind: core.RelationshipManyToOne,
			TargetType: targetType, ForeignKeyProperties: rel.fkProperties,
		})

	case core.RelationshipOneToOne:
		targetType, ok := pointerToStructElem(df.Field.Type)
		if !ok {
			errs.add("MAP-020", fieldTarget, "one_to_one must be a single entity reference (pointer to struct), not a collection")
			return
		}
		if !validateTargetForeignKeys(fieldTarget, targetType, rel.fkProperties, errs) {
			return
		}
		*relationships = append(*relationships, &RelationshipSpec{
			PropertyName: df.Field.Name, Kind: core.RelationshipOneToOne,
			TargetType: targetType, ForeignKeyProperties: rel.fkProperties,
		})

	case core.RelationshipOneToMany:
		elementType, ok := sliceEntityElem(df.Field.Type)
		if !ok {
			errs.add("MAP-020", fieldTarget, "one_to_many must be a collection ([]T or []*T) of an entity type")
			return
		}
		if !validateTargetForeignKeys(fieldTarget, elementType, rel.fkProperties, errs) {
			return
		}
		*relationships = append(*relationships, &RelationshipSpec{
			PropertyName: df.Field.Name, Kind: core.RelationshipOneToMany,
			TargetType: elementType, ForeignKeyProperties: rel.fkProperties,
		})

	case core.RelationshipManyToMany:
		elementType, ok := sliceEntityElem(df.Field.Type)
		if !ok {
			errs.add("MAP-020", fieldTarget, "many_to_many must be a collection ([]T or []*T) of an entity type")
			return
		}
		readManyToManyLink(entityType, df.Field.Name, fieldTarget, elementType, errs, relationships)
	}
}

// validateTargetForeignKeys: the named properties must exist (and be
// exported) on the target, and at least one must be named (MAP-021).
func validateTargetForeignKeys(fieldTarget string, targetType reflect.Type, names []string, errs *errorCollector) bool {
	if len(names) == 0 {
		errs.add("MAP-021", fieldTarget, "needs at least one target foreign-key property")
		return false
	}
	var missing []string
	for _, name := range names {
		field, ok := targetType.FieldByName(name)
		if !ok || field.PkgPath != "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		errs.add("MAP-021", fieldTarget, "names foreign-key propert%s '%s' not found on '%s'",
			plural(len(missing)), strings.Join(missing, "', '"), targetType.Name())
		return false
	}
	return true
}

// readManyToManyLink resolves the descriptor's ManyToManyDef for this field
// (MAP-022 when missing) and the link's own ForeignKeys, filtered by which
// side they reference (ADR-0019 add.1). The link is read with Descriptor —
// never Loader.Load, which would recurse.
func readManyToManyLink(
	entityType reflect.Type, fieldName string, fieldTarget string, elementType reflect.Type,
	errs *errorCollector, relationships *[]*RelationshipSpec,
) {
	def, hasDescriptor := Descriptor(entityType)
	var link reflect.Type
	declared := false
	if hasDescriptor {
		for _, m := range def.ManyToMany {
			if m.Property == fieldName {
				link = m.Link
				declared = true
				break
			}
		}
	}
	if !declared {
		errs.add("MAP-022", fieldTarget, "many_to_many has no matching ManyToMany entry in Entity()")
		return
	}

	linkDef, linkHasDescriptor := Descriptor(link)
	var toOwner, toTarget []string
	if linkHasDescriptor {
		for _, fk := range linkDef.ForeignKeys {
			if fk.References == entityType {
				toOwner = append(toOwner, fk.Property)
			}
			if fk.References == elementType {
				toTarget = append(toTarget, fk.Property)
			}
		}
	}

	if len(toOwner) == 0 {
		errs.add("MAP-022", fieldTarget, "link '%s' has no ForeignKey referencing this type", link.Name())
	}
	if len(toTarget) == 0 {
		errs.add("MAP-022", fieldTarget, "link '%s' has no ForeignKey referencing '%s'", link.Name(), elementType.Name())
	}
	if len(toOwner) == 0 || len(toTarget) == 0 {
		return
	}

	*relationships = append(*relationships, &RelationshipSpec{
		PropertyName: fieldName, Kind: core.RelationshipManyToMany, TargetType: elementType,
		LinkType: link, LinkForeignKeysToOwner: toOwner, LinkForeignKeysToTarget: toTarget,
	})
}

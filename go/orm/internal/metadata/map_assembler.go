package metadata

import (
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// errorCollector accumulates every violation found while loading one type's
// map (spec/metadata-model.md: "every violation for a type is collected
// before failing — never first-error-only"). Every loader path threads one
// through to assemble.
type errorCollector struct {
	entityType reflect.Type
	errors     []*core.Error
}

func (c *errorCollector) add(code, target, format string, args ...any) {
	c.errors = append(c.errors, core.Errorf(code, target, format, args...))
}

// mappingErrors returns the aggregate error, or nil when nothing was collected.
func (c *errorCollector) mappingErrors() *core.MappingErrors {
	if len(c.errors) == 0 {
		return nil
	}
	return &core.MappingErrors{EntityType: c.entityType, Errors: c.errors}
}

// target formats "Type.Property" the way every loader error names a field.
func target(entityType reflect.Type, propertyName string) string {
	return entityType.Name() + "." + propertyName
}

// assemble is the shared final stage of every loader path (the C#
// MapAssembler.cs): resolves column names through the convention, runs the
// source-independent validations (duplicate columns, key shape, version
// shape, relationship FK resolution, index column resolution), and produces
// the EntityMap. Violations accumulate in errs; the caller returns one
// *core.MappingErrors with all of them.
func assemble(
	entityType reflect.Type,
	kind core.RelationKind,
	relationName string,
	schema string,
	definingSQL string,
	statementParameters []core.StatementParameter,
	specs []*MappedPropertySpec,
	indexSpecs []*IndexSpec,
	relationshipSpecs []*RelationshipSpec,
	convention core.NamingConvention,
	errs *errorCollector,
) (*core.EntityMap, error) {
	properties, byProperty := buildProperties(entityType, specs, convention, errs)
	validateVersion(entityType, properties, errs)
	keyStrategy := resolveKeyStrategy(entityType, kind, properties, errs)
	indexes := resolveIndexColumns(entityType, relationName, indexSpecs, byProperty, convention, errs)
	relationships := resolveRelationships(entityType, relationshipSpecs, byProperty, errs)

	if mapping := errs.mappingErrors(); mapping != nil {
		return nil, mapping
	}

	return core.NewEntityMap(
		entityType, kind, relationName, schema, definingSQL, statementParameters,
		properties, keyStrategy, indexes, relationships,
	), nil
}

// buildProperties resolves each spec's column name through the convention,
// resolves its neutral ColumnType once (the `type=`/`enum_int` overrides win
// over core.ColumnTypeOf), and detects two properties mapping to the same
// column (MAP-018).
func buildProperties(
	entityType reflect.Type, specs []*MappedPropertySpec, convention core.NamingConvention, errs *errorCollector,
) ([]*core.PropertyMap, map[string]*core.PropertyMap) {
	properties := make([]*core.PropertyMap, 0, len(specs))
	byProperty := map[string]*core.PropertyMap{}
	seenColumns := map[string]string{}

	for _, spec := range specs {
		columnName := convention.ColumnName(spec.PropertyName)
		if spec.ExplicitColumn != nil {
			columnName = *spec.ExplicitColumn
		}
		if other, exists := seenColumns[columnName]; exists {
			errs.add("MAP-018", target(entityType, spec.PropertyName), "maps to column '%s' already used by '%s'", columnName, other)
		} else {
			seenColumns[columnName] = spec.PropertyName
		}

		columnType := core.ColumnTypeOf(spec.Field.Type)
		if spec.ColumnTypeOverride != nil {
			columnType = *spec.ColumnTypeOverride
		}
		if spec.EnumAsInt {
			columnType = core.TypeEnumInt
		}

		property := &core.PropertyMap{
			Field:                spec.Field,
			Index:                spec.Index,
			DeclaringType:        spec.DeclaringType,
			PropertyName:         spec.PropertyName,
			ColumnName:           columnName,
			Type:                 spec.Field.Type,
			ColumnType:           columnType,
			IsNullable:           spec.Field.Type.Kind() == reflect.Pointer,
			IsKey:                spec.IsKey,
			IsGenerated:          spec.IsGenerated,
			IsVersion:            spec.IsVersion,
			ForeignKeyReferences: spec.ForeignKeyReferences,
		}
		properties = append(properties, property)
		byProperty[spec.PropertyName] = property
	}

	return properties, byProperty
}

func validateVersion(entityType reflect.Type, properties []*core.PropertyMap, errs *errorCollector) {
	var version *core.PropertyMap
	for _, p := range properties {
		if !p.IsVersion {
			continue
		}
		if version != nil {
			errs.add("MAP-019", p.Target(), "a second version property; '%s' already is the version column", version.PropertyName)
			continue
		}
		version = p
		if !p.ColumnType.IsInteger() {
			errs.add("MAP-019", p.Target(), "version requires an integer type, found %s", p.ColumnType)
		}
		if p.IsKey {
			errs.add("MAP-019", p.Target(), "version cannot be part of the key")
		}
	}
}

func resolveKeyStrategy(
	entityType reflect.Type, kind core.RelationKind, properties []*core.PropertyMap, errs *errorCollector,
) core.KeyStrategy {
	var keys []*core.PropertyMap
	for _, p := range properties {
		if p.IsKey {
			keys = append(keys, p)
		}
	}
	if len(keys) == 0 {
		if kind == core.RelationTable {
			errs.add("MAP-019", entityType.Name(), "a table-backed entity must declare a key")
		}
		return core.KeyNone
	}

	var generated []*core.PropertyMap
	for _, k := range keys {
		if k.IsGenerated {
			generated = append(generated, k)
		}
	}
	if len(generated) > 0 {
		if len(keys) > 1 {
			errs.add("MAP-019", generated[0].Target(), "generated is not valid on a composite key")
		} else if !keys[0].ColumnType.IsInteger() {
			errs.add("MAP-019", keys[0].Target(),
				"a database-generated key must be an integer type; '%s' is not (client-generated GUIDs drop generated)", keys[0].ColumnType)
		}
		return core.KeyDatabaseGenerated
	}

	if len(keys) == 1 && keys[0].ColumnType == core.TypeGUID {
		return core.KeyClientGuid
	}

	return core.KeyNatural
}

func resolveIndexColumns(
	entityType reflect.Type,
	relationName string,
	indexSpecs []*IndexSpec,
	byProperty map[string]*core.PropertyMap,
	convention core.NamingConvention,
	errs *errorCollector,
) []*core.EntityIndex {
	if len(indexSpecs) == 0 {
		return nil
	}

	indexes := make([]*core.EntityIndex, 0, len(indexSpecs))
	for _, spec := range indexSpecs {
		columns := make([]core.IndexColumn, 0, len(spec.Columns))
		valid := true
		for _, c := range spec.Columns {
			property, ok := byProperty[c.PropertyName]
			if !ok {
				errs.add("MAP-015", entityType.Name()+" index", "'%s' is not a mapped property", c.PropertyName)
				valid = false
				continue
			}
			columns = append(columns, core.IndexColumn{PropertyName: c.PropertyName, ColumnName: property.ColumnName, Descending: c.Descending})
		}
		if !valid {
			continue
		}

		name := ""
		if spec.Name != nil {
			name = *spec.Name
		} else {
			columnNames := make([]string, len(columns))
			for i, c := range columns {
				columnNames[i] = c.ColumnName
			}
			name = convention.IndexName(relationName, columnNames)
		}
		indexes = append(indexes, &core.EntityIndex{Name: name, Columns: columns, Unique: spec.Unique})
	}
	return indexes
}

func resolveRelationships(
	entityType reflect.Type, specs []*RelationshipSpec, byProperty map[string]*core.PropertyMap, errs *errorCollector,
) []*core.RelationshipMap {
	if len(specs) == 0 {
		return nil
	}

	ownerKeyCount := 0
	for _, p := range byProperty {
		if p.IsKey {
			ownerKeyCount++
		}
	}

	relationships := make([]*core.RelationshipMap, 0, len(specs))
	for _, spec := range specs {
		t := target(entityType, spec.PropertyName)

		switch spec.Kind {
		case core.RelationshipManyToOne:
			var unmapped []string
			for _, n := range spec.ForeignKeyProperties {
				if _, ok := byProperty[n]; !ok {
					unmapped = append(unmapped, n)
				}
			}
			if len(unmapped) > 0 {
				errs.add("MAP-016", t, "many_to_one names foreign-key propert%s '%s' that are not mapped properties",
					plural(len(unmapped)), strings.Join(unmapped, "', '"))
				continue
			}
			if arity := keyArity(spec.TargetType); arity > 0 && arity != len(spec.ForeignKeyProperties) {
				errs.add("MAP-016", t, "many_to_one declares %d foreign-key propert%s but '%s' has a %d-part key",
					len(spec.ForeignKeyProperties), plural(len(spec.ForeignKeyProperties)), spec.TargetType.Name(), arity)
				continue
			}

		case core.RelationshipOneToMany, core.RelationshipOneToOne:
			if ownerKeyCount > 0 && len(spec.ForeignKeyProperties) != ownerKeyCount {
				errs.add("MAP-021", t, "declares %d target foreign-key propert%s but this entity has a %d-part key",
					len(spec.ForeignKeyProperties), plural(len(spec.ForeignKeyProperties)), ownerKeyCount)
				continue
			}

		default: // core.RelationshipManyToMany
			if ownerKeyCount > 0 && len(spec.LinkForeignKeysToOwner) != ownerKeyCount {
				errs.add("MAP-022", t, "link '%s' declares %d foreign key(s) referencing this type, whose key has %d part(s)",
					spec.LinkType.Name(), len(spec.LinkForeignKeysToOwner), ownerKeyCount)
				continue
			}
			if arity := keyArity(spec.TargetType); arity > 0 && len(spec.LinkForeignKeysToTarget) != arity {
				errs.add("MAP-022", t, "link '%s' declares %d foreign key(s) referencing '%s', whose key has %d part(s)",
					spec.LinkType.Name(), len(spec.LinkForeignKeysToTarget), spec.TargetType.Name(), arity)
				continue
			}
		}

		relationships = append(relationships, &core.RelationshipMap{
			PropertyName:            spec.PropertyName,
			Kind:                    spec.Kind,
			TargetType:              spec.TargetType,
			ForeignKeyProperties:    spec.ForeignKeyProperties,
			LinkType:                spec.LinkType,
			LinkForeignKeysToOwner:  spec.LinkForeignKeysToOwner,
			LinkForeignKeysToTarget: spec.LinkForeignKeysToTarget,
		})
	}
	return relationships
}

// keyArity is the related type's key arity by a static scan of its `key` tag
// option (embedded structs included) — the C# KeyArity, which never loads the
// related type's full map (a cache-less recursion would loop). Zero means
// "declares none" — the caller skips the arity check rather than guessing.
func keyArity(t reflect.Type) int {
	t = EntityType(t)
	if t == nil || t.Kind() != reflect.Struct {
		return 0
	}
	count := 0
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Anonymous {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				continue
			}
			if embedded.Kind() == reflect.Struct {
				count += keyArity(embedded)
				continue
			}
		}
		tagValue, ok := field.Tag.Lookup(TagName)
		if !ok {
			continue
		}
		for _, token := range strings.Split(tagValue, ",") {
			if strings.TrimSpace(token) == "key" {
				count++
				break
			}
		}
	}
	return count
}

func plural(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}

package metadata

import (
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// loadByConvention is the port of ConventionMapLoader.cs (§7.6): a type with
// no mapping declarations at all maps every exported field by convention to a
// table named by the convention; a field named "id" (case-insensitively) is
// the key — database-generated for integer kinds, client GUID for
// core.GUID, natural otherwise. No indexes, no relationships.
func loadByConvention(l *Loader, t reflect.Type) (*core.EntityMap, error) {
	convention := l.options.Convention()
	errs := &errorCollector{entityType: t}

	fields, discErrs := discoverFields(t)
	errs.errors = append(errs.errors, discErrs...)

	specs := make([]*MappedPropertySpec, 0, len(fields))
	for _, df := range fields {
		isID := strings.EqualFold(df.Field.Name, "id")
		spec := &MappedPropertySpec{
			Field:         df.Field,
			Index:         df.Index,
			DeclaringType: df.DeclaringType,
			PropertyName:  df.Field.Name,
			IsKey:         isID,
		}
		if isID {
			valueType := df.Field.Type
			if valueType.Kind() == reflect.Pointer {
				valueType = valueType.Elem()
			}
			spec.IsGenerated = core.ColumnTypeOf(valueType).IsInteger()
		}
		specs = append(specs, spec)
	}

	return assemble(
		t, core.RelationTable, convention.TableName(t.Name()), "", "", nil,
		specs, nil, nil, convention, errs,
	)
}

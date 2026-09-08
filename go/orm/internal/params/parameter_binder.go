// Package params binds @name placeholders from the public fields of an args
// struct (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/ParameterBinder.cs): both
// directions strict (PRM-001/002), collection-typed fields expand IN (@ids) to
// generated placeholders, and every bound value passes through the type
// converter — never SQL text built from user data.
package params

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
)

// BoundSQL is the rendered statement text plus its bound arguments, ready for
// conn.QueryContext(ctx, bound.SQL, bound.Args...) — every element of Args is a
// sql.NamedArg.
type BoundSQL struct {
	SQL  string
	Args []any
}

// Bind binds sqlText's @name placeholders from args's exported fields, matched
// case-insensitively: a placeholder with no matching field is PRM-001, a field
// no placeholder uses is PRM-002. A slice- or array-typed field (not []byte,
// not a string) is an IN list: without array-parameter support (SQLite,
// always) every occurrence of the placeholder expands to generated names
// (@ids_0, @ids_1, …) using the SQL's own spelling, and an empty collection
// rewrites to the literal NULL. With array-parameter support the collection
// binds as one parameter instead, SQL untouched.
func Bind(sqlText string, args any, converter *mapping.TypeConverter, dialect core.Dialect, queryName string) (BoundSQL, error) {
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return BoundSQL{}, core.Errorf("PRM-001", queryName, "args must be a struct or a pointer to one, got %T", args)
	}
	t := v.Type()

	type field struct {
		name  string
		value reflect.Value
	}
	var fields []field
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		fields = append(fields, field{name: sf.Name, value: v.Field(i)})
	}

	placeholders := core.FindPlaceholders(sqlText)

	for _, placeholder := range placeholders {
		found := false
		for _, f := range fields {
			if strings.EqualFold(f.name, placeholder) {
				found = true
				break
			}
		}
		if !found {
			return BoundSQL{}, core.Errorf("PRM-001", queryName,
				"SQL parameter @%s has no matching property on %s", placeholder, core.TypeName(t))
		}
	}

	text := sqlText
	var namedArgs []any

	for _, f := range fields {
		placeholder := ""
		for _, p := range placeholders {
			if strings.EqualFold(p, f.name) {
				placeholder = p
				break
			}
		}
		if placeholder == "" {
			return BoundSQL{}, core.Errorf("PRM-002", queryName,
				"property %s.%s is never used by the SQL", core.TypeName(t), f.name)
		}

		context := queryName + "." + placeholder

		if isCollection(f.value) {
			elements := collectionElements(f.value)

			if dialect.SupportsArrayParameters() {
				converted := make([]any, len(elements))
				for i, item := range elements {
					cv, err := converter.ToDatabase(item, "", context)
					if err != nil {
						return BoundSQL{}, err
					}
					converted[i] = cv
				}
				namedArgs = append(namedArgs, sql.Named(placeholder, converted))
				continue
			}

			occurrences := core.PlaceholderOccurrences(text, placeholder)
			var names []string
			for i, item := range elements {
				name := fmt.Sprintf("%s_%d", placeholder, i)
				names = append(names, "@"+name)
				cv, err := converter.ToDatabase(item, "", context)
				if err != nil {
					return BoundSQL{}, err
				}
				namedArgs = append(namedArgs, sql.Named(name, cv))
			}
			// An empty collection becomes NULL: "x IN (NULL)" is valid SQL matching no rows.
			replacement := "NULL"
			if len(names) > 0 {
				replacement = strings.Join(names, ", ")
			}
			text = replaceOccurrences(text, occurrences, replacement)
			continue
		}

		cv, err := converter.ToDatabase(f.value.Interface(), "", context)
		if err != nil {
			return BoundSQL{}, err
		}
		namedArgs = append(namedArgs, sql.Named(placeholder, cv))
	}

	return BoundSQL{SQL: text, Args: namedArgs}, nil
}

// isCollection reports whether v is a slice or array field that binds as an IN
// list — strings and byte slices/arrays are values, never collections.
func isCollection(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		return v.Type().Elem().Kind() != reflect.Uint8
	}
	return false
}

func collectionElements(v reflect.Value) []any {
	elements := make([]any, v.Len())
	for i := range elements {
		elements[i] = v.Index(i).Interface()
	}
	return elements
}

// replaceOccurrences rewrites every real occurrence (in reverse, so earlier
// offsets stay valid) with replacement — a lookalike inside a string literal
// or comment is not among occurrences any more than it is a detected placeholder.
func replaceOccurrences(text string, occurrences []core.PlaceholderSpan, replacement string) string {
	for i := len(occurrences) - 1; i >= 0; i-- {
		span := occurrences[i]
		text = text[:span.Index] + replacement + text[span.Index+span.Length:]
	}
	return text
}

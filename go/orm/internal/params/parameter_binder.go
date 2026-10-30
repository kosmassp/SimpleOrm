// Package params binds @name placeholders from the public fields of an args
// struct (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/ParameterBinder.cs): both
// directions strict (PRM-001/002), collection-typed fields expand IN (@ids) to
// generated placeholders, and every bound value passes through the type
// converter — never SQL text built from user data.
package params

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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
// rewrites to the literal NULL. With array-parameter support (PostgreSQL) the
// collection binds as one parameter instead, SQL untouched: a slice typed by
// the converted element type, an empty one typed from the declared element.
func Bind(sqlText string, args any, converter *mapping.TypeConverter, dialect core.Dialect, queryName string) (BoundSQL, error) {
	v := reflect.ValueOf(args)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return BoundSQL{}, core.Errorf("PRM-001", queryName, "args must be a struct or a pointer to one, got %T", args)
	}
	t := v.Type()
	plan := planFor(sqlText, queryName, t)

	if plan.missing != "" {
		return BoundSQL{}, core.Errorf("PRM-001", queryName,
			"SQL parameter @%s has no matching property on %s", plan.missing, core.TypeName(t))
	}

	text := sqlText
	var namedArgs []any
	if len(plan.fields) > 0 {
		namedArgs = make([]any, 0, len(plan.fields))
	}
	expanded := false // an earlier expansion moved the text: a later list rescans it, as before

	for i := range plan.fields {
		fp := &plan.fields[i]
		f := field{name: fp.name, value: v.Field(fp.index)}
		placeholder := fp.placeholder
		if placeholder == "" {
			return BoundSQL{}, core.Errorf("PRM-002", queryName,
				"property %s.%s is never used by the SQL", core.TypeName(t), f.name)
		}

		context := fp.context

		if fp.collection {
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
				typed, err := typedArray(converted, f.value.Type().Elem(), converter, context)
				if err != nil {
					return BoundSQL{}, err
				}
				namedArgs = append(namedArgs, sql.Named(placeholder, typed))
				continue
			}

			occurrences := fp.occurrences
			if expanded {
				occurrences = core.PlaceholderOccurrences(text, placeholder)
			}
			expanded = true
			var names []string
			for i, item := range elements {
				name := placeholder + "_" + strconv.Itoa(i)
				names = append(names, "@"+name)
				cv, err := converter.ToDatabase(item, "", context)
				if err != nil {
					return BoundSQL{}, err
				}
				if cv == nil {
					cv = mapping.TypedNull(core.ColumnTypeOf(f.value.Type().Elem()))
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
		if cv == nil {
			// A NULL carries the type the field's Go type gives it (ADR-0040 add.1, mapping.TypedNull).
			cv = mapping.TypedNull(core.ColumnTypeOf(f.value.Type()))
		}
		namedArgs = append(namedArgs, sql.Named(placeholder, cv))
	}

	return BoundSQL{SQL: text, Args: namedArgs}, nil
}

// field is one exported args field as one call reads it.
type field struct {
	name  string
	value reflect.Value
}

// The binding plan, cached per (SQL text, query name, args type) — ADR-0048 item 2. The placeholder scan, the PRM-001
// verdict, each exported field's placeholder (empty = PRM-002, still raised in field order while binding, so an
// earlier field's conversion error still wins), its error context, whether it is an IN list, and that list's
// occurrences in the original text are pure functions of the key; only the values are read per call. The key holds
// the SQL by value (equal text is the same plan — never a collision across models) and the type by identity. The
// cache stops growing past maxPlans entries (SQL built at run time would otherwise grow it without bound); a plan it
// no longer stores is built per call, as before.
type planKey struct {
	sql       string
	queryName string
	args      reflect.Type
}

type bindPlan struct {
	missing string // the first placeholder no field matches (PRM-001), "" when every one matches
	fields  []fieldPlan
}

type fieldPlan struct {
	index       int    // the field's index in the args struct
	name        string // the field's name
	placeholder string // the SQL's own spelling of the matching placeholder; "" → PRM-002
	context     string // queryName + "." + placeholder: the conversion error's label
	collection  bool
	occurrences []core.PlaceholderSpan // the placeholder's spans in the original text, for an IN list
}

const maxPlans = 4096

var (
	plans     sync.Map // planKey → *bindPlan
	planCount atomic.Int64
)

// planFor returns the cached plan for the key, building it on first use.
func planFor(sqlText, queryName string, t reflect.Type) *bindPlan {
	key := planKey{sql: sqlText, queryName: queryName, args: t}
	if cached, ok := plans.Load(key); ok {
		return cached.(*bindPlan)
	}
	plan := buildPlan(sqlText, queryName, t)
	if planCount.Load() < maxPlans {
		if actual, loaded := plans.LoadOrStore(key, plan); loaded {
			return actual.(*bindPlan)
		}
		planCount.Add(1)
	}
	return plan
}

// buildPlan is the scan and the matching Bind did on every call before the cache.
func buildPlan(sqlText, queryName string, t reflect.Type) *bindPlan {
	plan := &bindPlan{}
	var names []string
	var indices []int
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		names = append(names, sf.Name)
		indices = append(indices, i)
	}

	placeholders := core.FindPlaceholders(sqlText)
	for _, placeholder := range placeholders {
		found := false
		for _, name := range names {
			if strings.EqualFold(name, placeholder) {
				found = true
				break
			}
		}
		if !found {
			plan.missing = placeholder
			return plan
		}
	}

	plan.fields = make([]fieldPlan, len(names))
	for i, name := range names {
		fp := fieldPlan{index: indices[i], name: name}
		for _, p := range placeholders {
			if strings.EqualFold(p, name) {
				fp.placeholder = p
				break
			}
		}
		if fp.placeholder != "" {
			fp.context = queryName + "." + fp.placeholder
			fp.collection = isCollectionType(t.Field(indices[i]).Type)
			if fp.collection {
				fp.occurrences = core.PlaceholderOccurrences(sqlText, fp.placeholder)
			}
		}
		plan.fields[i] = fp
	}
	return plan
}

// isCollectionType reports whether a field of type t is a slice or array that binds as an IN
// list — strings and byte slices/arrays are values, never collections. (A field's kind is its type's.)
func isCollectionType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return t.Elem().Kind() != reflect.Uint8
	}
	return false
}

// typedArray is the array-parameter realization (spec/dialects.md "Array
// parameters"): the converted elements as a slice typed by the converted
// element type — []int64, []string, []float64, []bool, []time.Time, [][]byte —
// so the driver can encode it as the database array. "Typed" means the element
// type travels with the parameter even when there are no elements: an empty
// collection derives its type from the declared element type by converting that
// type's zero value through the same pipeline (so `bigint = any($1)` still
// types where an untyped empty list would not). A null element makes the slice
// of pointers, which drivers encode as NULL entries.
func typedArray(converted []any, declared reflect.Type, converter *mapping.TypeConverter, context string) (any, error) {
	var elementType reflect.Type
	hasNull := false
	for _, item := range converted {
		if item == nil {
			hasNull = true
		} else if elementType == nil {
			elementType = reflect.TypeOf(item)
		}
	}
	if elementType == nil {
		zero := reflect.Zero(declared)
		if declared.Kind() == reflect.Pointer {
			zero = reflect.Zero(declared.Elem())
		}
		sample, err := converter.ToDatabase(zero.Interface(), "", context)
		if err != nil || sample == nil {
			return nil, core.Errorf("MAP-030", context,
				"an array parameter of %s has no converted element type; register a TypeHandler (§7.9)", declared)
		}
		elementType = reflect.TypeOf(sample)
	}

	if hasNull {
		slice := reflect.MakeSlice(reflect.SliceOf(reflect.PointerTo(elementType)), len(converted), len(converted))
		for i, item := range converted {
			if item == nil {
				continue
			}
			element := reflect.New(elementType)
			element.Elem().Set(reflect.ValueOf(item))
			slice.Index(i).Set(element)
		}
		return slice.Interface(), nil
	}

	slice := reflect.MakeSlice(reflect.SliceOf(elementType), len(converted), len(converted))
	for i, item := range converted {
		slice.Index(i).Set(reflect.ValueOf(item))
	}
	return slice.Interface(), nil
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

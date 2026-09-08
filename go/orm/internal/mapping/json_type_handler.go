package mapping

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// JSONTypeHandler is the built-in §7.10 JSON-column handler (mirrors
// dotnet's JsonTypeHandler<T> over System.Text.Json): a TEXT column holding a
// JSON document decodes into T (a struct, a slice of structs for the
// json_group_array nesting pattern, or any nested combination) via plain
// reflection — no struct tags required. Keys match a field case-insensitively
// against either its own name or its snake_case spelling (core.ToSnakeCase),
// numbers are readable from strings, and Decimal/time.Time/orm.Enum members
// use their own §7.9 conventions. Unknown keys are ignored; a JSON null into a
// pointer is nil.
type JSONTypeHandler[T any] struct{}

// RegisterJSON registers T as a JSON column (§7.10) on registry: the extension
// point for nested results the fixed table cannot express on its own.
func RegisterJSON[T any](registry *core.TypeHandlerRegistry) *core.TypeHandlerRegistry {
	return core.RegisterHandler[T](registry, JSONTypeHandler[T]{})
}

// Parse decodes JSON TEXT (string or []byte) into T.
func (JSONTypeHandler[T]) Parse(databaseValue any) (T, error) {
	var zero T
	var text string
	switch v := databaseValue.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return zero, core.Errorf("MAP-031", "JSON", "cannot parse a %T as JSON text", databaseValue)
	}

	var tree any
	if err := json.Unmarshal([]byte(text), &tree); err != nil {
		return zero, core.Errorf("MAP-031", "JSON", "invalid JSON: %s", err)
	}

	target := reflect.New(reflect.TypeFor[T]()).Elem()
	if err := assignJSON(tree, target); err != nil {
		return zero, err
	}
	return target.Interface().(T), nil
}

// Format encodes value as a JSON document with snake_case keys.
func (JSONTypeHandler[T]) Format(value T) (any, error) {
	node, err := dehydrateJSON(reflect.ValueOf(value))
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(node)
	if err != nil {
		return nil, core.Errorf("MAP-030", "JSON", "cannot encode %T: %s", value, err)
	}
	return string(data), nil
}

// assignJSON assigns a decoded JSON tree node (string, float64, bool, nil,
// map[string]any, or []any — encoding/json's untyped decode shapes) onto
// target, which must be addressable and settable.
func assignJSON(node any, target reflect.Value) error {
	if target.Kind() == reflect.Pointer {
		if node == nil {
			target.Set(reflect.Zero(target.Type()))
			return nil
		}
		elem := reflect.New(target.Type().Elem())
		if err := assignJSON(node, elem.Elem()); err != nil {
			return err
		}
		target.Set(elem)
		return nil
	}

	if node == nil {
		return nil // leave the field at its zero value
	}

	switch target.Type() {
	case reflect.TypeFor[core.Decimal]():
		d, err := decimalFromJSON(node)
		if err != nil {
			return err
		}
		target.Set(reflect.ValueOf(d))
		return nil
	case reflect.TypeFor[time.Time]():
		text, ok := node.(string)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected an ISO-8601 string for time.Time, got %T", node)
		}
		t, err := core.ParseMarked(text, "JSON")
		if err != nil {
			return err
		}
		target.Set(reflect.ValueOf(t))
		return nil
	}

	if core.IsEnumType(target.Type()) {
		text, ok := node.(string)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a string for enum %s, got %T", target.Type(), node)
		}
		names := core.EnumNamesOf(target.Type())
		index, found := core.EnumIndex(names, text)
		if !found {
			return core.Errorf("MAP-031", "JSON", "'%s' is not a member of %s", text, target.Type())
		}
		target.Set(core.EnumValue(target.Type(), index, names))
		return nil
	}

	switch target.Kind() {
	case reflect.Struct:
		obj, ok := node.(map[string]any)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a JSON object for %s, got %T", target.Type(), node)
		}
		for _, field := range reflect.VisibleFields(target.Type()) {
			if !field.IsExported() {
				continue
			}
			value, found := findJSONKey(obj, field.Name)
			if !found {
				continue
			}
			if err := assignJSON(value, target.FieldByIndex(field.Index)); err != nil {
				return err
			}
		}
		return nil

	case reflect.Slice:
		arr, ok := node.([]any)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a JSON array for %s, got %T", target.Type(), node)
		}
		slice := reflect.MakeSlice(target.Type(), len(arr), len(arr))
		for i, item := range arr {
			if err := assignJSON(item, slice.Index(i)); err != nil {
				return err
			}
		}
		target.Set(slice)
		return nil

	case reflect.String:
		s, ok := node.(string)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a string for %s, got %T", target.Type(), node)
		}
		target.SetString(s)
		return nil

	case reflect.Bool:
		b, ok := node.(bool)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a bool for %s, got %T", target.Type(), node)
		}
		target.SetBool(b)
		return nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, ok := jsonInt(node)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a number for %s, got %T", target.Type(), node)
		}
		target.SetInt(i)
		return nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		i, ok := jsonInt(node)
		if !ok || i < 0 {
			return core.Errorf("MAP-031", "JSON", "expected a non-negative number for %s, got %T", target.Type(), node)
		}
		target.SetUint(uint64(i))
		return nil

	case reflect.Float32, reflect.Float64:
		f, ok := jsonFloat(node)
		if !ok {
			return core.Errorf("MAP-031", "JSON", "expected a number for %s, got %T", target.Type(), node)
		}
		target.SetFloat(f)
		return nil

	case reflect.Interface:
		target.Set(reflect.ValueOf(node))
		return nil
	}

	return core.Errorf("MAP-030", "JSON", "no JSON conversion for %s", target.Type())
}

// findJSONKey matches a struct field to a JSON key case-insensitively against
// either the field's own name or its snake_case spelling.
func findJSONKey(obj map[string]any, fieldName string) (any, bool) {
	snake := core.ToSnakeCase(fieldName)
	for key, value := range obj {
		if strings.EqualFold(key, fieldName) || strings.EqualFold(key, snake) {
			return value, true
		}
	}
	return nil, false
}

func decimalFromJSON(node any) (core.Decimal, error) {
	switch v := node.(type) {
	case string:
		return core.ParseDecimal(v)
	case float64:
		return core.DecimalFromFloat(v)
	}
	return core.Decimal{}, core.Errorf("MAP-031", "JSON", "expected a number or string for Decimal, got %T", node)
}

// jsonInt reads an integer from a decoded JSON number (float64) or a numeric
// string (JsonNumberHandling.AllowReadingFromString's Go analog).
func jsonInt(node any) (int64, bool) {
	switch v := node.(type) {
	case float64:
		return int64(v), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

func jsonFloat(node any) (float64, bool) {
	switch v := node.(type) {
	case float64:
		return v, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// dehydrateJSON turns a Go value into a tree of JSON-marshalable nodes
// (map[string]any, []any, string, float64, bool, nil), the Format-side mirror
// of assignJSON.
func dehydrateJSON(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}

	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		return dehydrateJSON(v.Elem())
	}

	switch value := v.Interface().(type) {
	case core.Decimal:
		return value.String(), nil
	case time.Time:
		return core.FormatUTC(value), nil
	}

	if core.IsEnumType(v.Type()) {
		name, ok := core.EnumName(v.Interface())
		if !ok {
			return nil, core.Errorf("MAP-031", "JSON", "%v is not a member of %s", v.Interface(), v.Type())
		}
		return name, nil
	}

	switch v.Kind() {
	case reflect.Struct:
		obj := map[string]any{}
		for _, field := range reflect.VisibleFields(v.Type()) {
			if !field.IsExported() {
				continue
			}
			node, err := dehydrateJSON(v.FieldByIndex(field.Index))
			if err != nil {
				return nil, err
			}
			obj[core.ToSnakeCase(field.Name)] = node
		}
		return obj, nil

	case reflect.Slice, reflect.Array:
		arr := make([]any, v.Len())
		for i := range arr {
			node, err := dehydrateJSON(v.Index(i))
			if err != nil {
				return nil, err
			}
			arr[i] = node
		}
		return arr, nil

	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint(), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil
	case reflect.Interface:
		return dehydrateJSON(v.Elem())
	}

	return nil, core.Errorf("MAP-030", "JSON", "no JSON conversion for %s", v.Type())
}

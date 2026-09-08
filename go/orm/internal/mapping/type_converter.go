// Package mapping is the value-conversion side of the pipeline (§7.9,
// mirrors dotnet/src/SimpleOrm/TypeConverter.cs and TypeHandlers.cs): the fixed
// conversion table plus the handler registry — the only two ways a value
// crosses the database boundary — and the built-in JSON column handler
// (§7.10). No reflection-based guessing: an unrecognized type or column token
// fails with MAP-030, a rule that exists but rejects the value with MAP-031.
package mapping

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// TypeConverter is the fixed table (§7.9) plus the handler registry. A nil
// registry holds nothing (core.TypeHandlerRegistry's methods are nil-safe), so
// the zero-handler configuration works without constructing one.
type TypeConverter struct {
	handlers               *core.TypeHandlerRegistry
	bindsTemporalsNatively bool
}

// NewTypeConverter builds a converter. bindsTemporalsNatively mirrors
// Dialect.BindsTemporalsNatively (ADR-0025): false on SQLite, where temporals
// always bind as ISO-8601 text.
func NewTypeConverter(handlers *core.TypeHandlerRegistry, bindsTemporalsNatively bool) *TypeConverter {
	return &TypeConverter{handlers: handlers, bindsTemporalsNatively: bindsTemporalsNatively}
}

// HasHandler reports whether a handler is registered for t (a nullable pointer resolves to its element).
func (c *TypeConverter) HasHandler(t reflect.Type) bool { return c.handlers.Contains(t) }

// FromDatabase converts a database value to a value assignable to target: a
// nullable pointer's element type when target is a pointer, target itself
// otherwise. The caller (the result mapper) handles pointer wrapping through
// PropertyMap.Set; this returns the element value, or nil for a NULL pointer
// target. columnType drives the fixed table — the neutral token, not target's
// Go type, disambiguates cases target alone cannot (a `type=guid` string, a
// `type=date` time.Time): callers without a PropertyMap pass
// core.ColumnTypeOf(target). Handlers win over the fixed table.
func (c *TypeConverter) FromDatabase(value any, target reflect.Type, columnType core.ColumnType, context string) (any, error) {
	element := target
	if element.Kind() == reflect.Pointer {
		element = element.Elem()
	}

	if value == nil {
		if target.Kind() == reflect.Pointer {
			return nil, nil
		}
		return nil, core.Errorf("MAP-031", context, "NULL cannot convert to non-nullable %s", element)
	}

	if parsed, handled, err := c.handlers.Parse(element, value); handled {
		return parsed, err
	}

	switch columnType {
	case core.TypeInt16, core.TypeInt32, core.TypeInt64:
		raw, ok := parseInt64(value)
		if !ok {
			return nil, conversionFailed(value, string(columnType), context)
		}
		return convertInt(raw, element, columnType, value, context)

	case core.TypeDecimal:
		return c.toDecimal(value, context)

	case core.TypeDouble, core.TypeFloat:
		f, ok := toFloat64(value)
		if !ok {
			return nil, conversionFailed(value, "double", context)
		}
		return convertTo(f, element), nil

	case core.TypeBool:
		b, err := toBool(value, context)
		if err != nil {
			return nil, err
		}
		return convertTo(b, element), nil

	case core.TypeString:
		return convertTo(toInvariantString(value), element), nil

	case core.TypeGUID:
		return c.toGUID(value, element, context)

	case core.TypeBytes:
		switch v := value.(type) {
		case []byte:
			return v, nil
		case string:
			return []byte(v), nil
		}
		return nil, conversionFailed(value, "bytes", context)

	case core.TypeDateTime:
		return toTemporal(value, context, core.ParseMarked)

	case core.TypeDateTimeOffset:
		return toTemporal(value, context, core.ParseOffset)

	case core.TypeDate:
		return toTemporal(value, context, core.ParseDate)

	case core.TypeTime:
		return toTemporal(value, context, core.ParseTime)

	case core.TypeEnumText, core.TypeEnumInt:
		return toEnum(value, element, context)
	}

	return nil, core.Errorf("MAP-030", context,
		"no conversion or handler from %T to column type %s; register a TypeHandler (§7.9)", value, columnType)
}

// ToDatabase converts a Go value to a driver value: int64, float64, bool,
// string, []byte, or nil. columnType is "" to derive the token from the
// value's own type (core.ColumnTypeOf) — what parameter binding passes — or an
// explicit token (an enum's [EnumAsInt] flag, a temporal override) that a
// mapped column's storage rule demands. Handlers win over the fixed table.
func (c *TypeConverter) ToDatabase(value any, columnType core.ColumnType, context string) (any, error) {
	if value == nil {
		return nil, nil
	}

	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
		value = rv.Interface()
	}

	if formatted, handled, err := c.handlers.Format(value); handled {
		return formatted, err
	}

	if core.IsEnumType(rv.Type()) {
		name, ok := core.EnumName(value)
		if !ok {
			return nil, core.Errorf("MAP-031", context, "%v is not a member of %s", value, rv.Type())
		}
		if columnType == core.TypeEnumInt {
			names := core.EnumNamesOf(rv.Type())
			index, _ := core.EnumIndex(names, name)
			return int64(index), nil
		}
		return name, nil
	}

	switch v := value.(type) {
	case core.Decimal:
		return v.String(), nil
	case core.GUID:
		return v.String(), nil
	case time.Time:
		return c.formatTemporal(v, columnType), nil
	case []byte:
		return v, nil
	case string:
		return v, nil
	case bool:
		return v, nil
	}

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rv.Bytes(), nil
		}
	}

	return nil, core.Errorf("MAP-030", context,
		"no conversion or handler stores a %s; register a TypeHandler (§7.9)", rv.Type())
}

func (c *TypeConverter) formatTemporal(value time.Time, columnType core.ColumnType) any {
	switch columnType {
	case core.TypeDateTimeOffset:
		if c.bindsTemporalsNatively {
			return value.UTC()
		}
		return core.FormatOffset(value)
	case core.TypeDate:
		if c.bindsTemporalsNatively {
			return value.UTC()
		}
		return core.FormatDate(value)
	case core.TypeTime:
		if c.bindsTemporalsNatively {
			return value.UTC()
		}
		return core.FormatTime(value)
	default: // "" and TypeDateTime both mean the plain datetime rule.
		if c.bindsTemporalsNatively {
			return value.UTC()
		}
		return core.FormatUTC(value)
	}
}

func (c *TypeConverter) toDecimal(value any, context string) (any, error) {
	switch v := value.(type) {
	case string:
		return core.ParseDecimal(v)
	case int64:
		return core.DecimalOf(v), nil
	case float64:
		return core.DecimalFromFloat(v)
	}
	return nil, conversionFailed(value, "decimal", context)
}

// toGUID parses value as a GUID and returns it typed as element: core.GUID
// itself, or a string when the column is a `type=guid`-tagged string field
// (CODING-STANDARD §10 — the neutral token, not the Go type, carries the rule).
func (c *TypeConverter) toGUID(value any, element reflect.Type, context string) (any, error) {
	var g core.GUID
	var err error
	switch v := value.(type) {
	case string:
		g, err = core.ParseGUID(v)
	case []byte:
		g, err = core.GUIDFromBytes(v)
	default:
		return nil, conversionFailed(value, "guid", context)
	}
	if err != nil {
		return nil, err
	}
	if element == reflect.TypeFor[core.GUID]() {
		return g, nil
	}
	if element.Kind() == reflect.String {
		return convertTo(g.String(), element), nil
	}
	return nil, core.Errorf("MAP-030", context, "a guid column cannot target %s", element)
}

func toTemporal(value any, context string, parse func(string, string) (time.Time, error)) (any, error) {
	switch v := value.(type) {
	case string:
		return parse(v, context)
	case time.Time:
		// A driver-native temporal is only possible where the driver itself
		// parsed and marked it; treat it as already marked (CODING-STANDARD §10).
		return v.UTC(), nil
	}
	return nil, conversionFailed(value, "datetime", context)
}

// toEnum mirrors TypeConverter.cs's uniform enum rule: a string value matches a
// member name case-insensitively; anything else is read as its ordinal
// position. Both enum_text and enum_int columns read this way — only writing
// distinguishes name from position (ToDatabase's enumAsInt flag).
func toEnum(value any, element reflect.Type, context string) (any, error) {
	if !core.IsEnumType(element) {
		return nil, core.Errorf("MAP-030", context, "%s is not an enum type", element)
	}
	names := core.EnumNamesOf(element)

	switch v := value.(type) {
	case string:
		index, ok := core.EnumIndex(names, v)
		if !ok {
			return nil, core.Errorf("MAP-031", context, "'%s' is not a member of %s", v, element)
		}
		return core.EnumValue(element, index, names).Interface(), nil
	case int64:
		return enumByPosition(v, element, names, value, context)
	case float64:
		return enumByPosition(int64(v), element, names, value, context)
	}
	return nil, conversionFailed(value, element.String(), context)
}

func enumByPosition(position int64, element reflect.Type, names []string, original any, context string) (any, error) {
	if position < 0 || position >= int64(len(names)) {
		return nil, core.Errorf("MAP-031", context, "position %v is out of range for %s", original, element)
	}
	return core.EnumValue(element, int(position), names).Interface(), nil
}

func parseInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
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

func toFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func toBool(value any, context string) (any, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case int64:
		return v != 0, nil
	case float64:
		return v != 0, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "1" || strings.EqualFold(trimmed, "true") {
			return true, nil
		}
		if trimmed == "0" || strings.EqualFold(trimmed, "false") {
			return false, nil
		}
	}
	return nil, conversionFailed(value, "bool", context)
}

func toInvariantString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}

// convertInt bounds-checks raw against columnType's declared range (the
// neutral token, which may be narrower than element's Go kind) and against
// element's own kind, then builds a value of element's exact type.
func convertInt(raw int64, element reflect.Type, columnType core.ColumnType, original any, context string) (any, error) {
	switch columnType {
	case core.TypeInt16:
		if raw < math.MinInt16 || raw > math.MaxInt16 {
			return nil, conversionFailed(original, "int16", context)
		}
	case core.TypeInt32:
		if raw < math.MinInt32 || raw > math.MaxInt32 {
			return nil, conversionFailed(original, "int32", context)
		}
	}

	result := reflect.New(element).Elem()
	switch element.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if result.OverflowInt(raw) {
			return nil, conversionFailed(original, element.String(), context)
		}
		result.SetInt(raw)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if raw < 0 || result.OverflowUint(uint64(raw)) {
			return nil, conversionFailed(original, element.String(), context)
		}
		result.SetUint(uint64(raw))
	default:
		return nil, core.Errorf("MAP-030", context, "column type %s does not target %s", columnType, element)
	}
	return result.Interface(), nil
}

// convertTo converts a plain-kinded value (string/bool/float64/...) to a named
// Go type sharing that kind (reflect.Value.Convert), e.g. a custom string type.
func convertTo(raw any, element reflect.Type) any {
	return reflect.ValueOf(raw).Convert(element).Interface()
}

func conversionFailed(value any, targetLabel string, context string) error {
	return core.Errorf("MAP-031", context, "cannot convert '%v' (%T) to %s", value, value, targetLabel)
}

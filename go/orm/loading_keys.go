package orm

import (
	"reflect"
	"slices"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The loading engines' key fast paths (ADR-0048 items 4 and 5). A single-part key over one builtin integer type is
// read straight from the field as an int64 and correlated through a map, instead of a boxed core.KeyTuple compared
// with reflect.DeepEqual against every other tuple. The equality is the same: both sides share one Go type, so
// DeepEqual on the boxed values and int64 equality agree; and the order is the same: for these types int64 order is
// CompareValues' order. Every other key — composite, owned, unsigned 64-bit, a named integer type (an int-backed
// enum compares by its text in CompareValues), decimals, strings, GUIDs, temporals — keeps the tuple path.

// integerKeyTypes are the builtin integer types the fast path reads (uint and uint64 stay on the tuple path: an
// int64 cannot hold their whole range in order).
var integerKeyTypes = map[reflect.Type]bool{
	reflect.TypeFor[int]():    true,
	reflect.TypeFor[int8]():   true,
	reflect.TypeFor[int16]():  true,
	reflect.TypeFor[int32]():  true,
	reflect.TypeFor[int64]():  true,
	reflect.TypeFor[uint8]():  true,
	reflect.TypeFor[uint16](): true,
	reflect.TypeFor[uint32](): true,
}

// integerKey reports whether properties is one directly mapped property of a builtin integer type.
func integerKey(properties []*core.PropertyMap) bool {
	return len(properties) == 1 && properties[0].Owner == nil && integerKeyTypes[properties[0].ValueType()]
}

// sameIntegerKey reports whether two correlated property lists are integer keys of one Go type.
func sameIntegerKey(left, right []*core.PropertyMap) bool {
	return integerKey(left) && integerKey(right) && left[0].ValueType() == right[0].ValueType()
}

// readIntegerKey reads an integer key property from an entity (a struct, or a pointer to one, as a reflect.Value);
// false for a null (a nil pointer field) — the part that excludes an owner from querying.
func readIntegerKey(p *core.PropertyMap, entity reflect.Value) (int64, bool) {
	field := reflect.Indirect(entity).FieldByIndex(p.Index)
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return 0, false
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return field.Int(), true
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return int64(field.Uint()), true
	}
	return 0, false
}

// exactNumericStorage are the storage types (lowercased, size stripped) an engine orders as numbers.
var exactNumericStorage = map[string]bool{
	"integer": true, "bigint": true, "int": true, "smallint": true, "tinyint": true, "numeric": true, "decimal": true,
}

// keyOrderIsValueOrder is the sort-skip rule (ADR-0048 item 4, spec/loading.md "value order after fetching"): rows
// that arrive ORDER BY the key are already in value order when every key part is a builtin integer type or a decimal
// and the dialect stores it as an integer or exact numeric type — the engine then compares numbers exactly as
// CompareValues does, and the post-fetch sort is the identity. SQLite's TEXT decimal keeps the sort ('10' before '2');
// strings (collations are not ordinal), GUIDs, temporals, booleans, enums, and bytes always sort.
func keyOrderIsValueOrder(dialect core.Dialect, m *core.EntityMap, properties []*core.PropertyMap) bool {
	if len(properties) == 0 {
		return false
	}
	for _, p := range properties {
		if p.Owner != nil {
			return false
		}
		valueType := p.ValueType()
		if !integerKeyTypes[valueType] && valueType != reflect.TypeFor[core.Decimal]() {
			return false
		}
		storage := strings.ToLower(strings.TrimSpace(dialect.StorageType(m, p)))
		if open := strings.IndexByte(storage, '('); open >= 0 {
			storage = strings.TrimSpace(storage[:open])
		}
		if !exactNumericStorage[storage] {
			return false
		}
	}
	return true
}

// keyOrderIsValueOrder is the rule for m's own key, cached with m's generated text.
func (db *Db) keyOrderIsValueOrder(m *core.EntityMap) bool {
	g := db.generated(m)
	defer db.renders.mu.Unlock()
	if g.keyValueOrder == nil {
		ordered := keyOrderIsValueOrder(db.options.Dialect, m, m.KeyProperties)
		g.keyValueOrder = &ordered
	}
	return *g.keyValueOrder
}

// sortByProperties orders values by the properties' values (core.KeyTuple.Compare — never a stringified rendering,
// spec/loading.md, ADR-0021 add.1: "10 comes after 2"), stably. Each row's key is read once, not twice per
// comparison: the same comparisons over the same sequence, so the same permutation.
func sortByProperties(values []reflect.Value, properties []*core.PropertyMap) {
	if integerKey(properties) {
		type keyed struct {
			key   int64
			value reflect.Value
		}
		rows := make([]keyed, len(values))
		integral := true
		for i, v := range values {
			key, ok := readIntegerKey(properties[0], v)
			if !ok {
				integral = false
				break
			}
			rows[i] = keyed{key, v}
		}
		if integral {
			slices.SortStableFunc(rows, func(a, b keyed) int {
				switch {
				case a.key < b.key:
					return -1
				case a.key > b.key:
					return 1
				}
				return 0
			})
			for i := range rows {
				values[i] = rows[i].value
			}
			return
		}
	}
	type keyed struct {
		key   core.KeyTuple
		value reflect.Value
	}
	rows := make([]keyed, len(values))
	for i, v := range values {
		rows[i] = keyed{extractTuple(properties, v.Interface()), v}
	}
	slices.SortStableFunc(rows, func(a, b keyed) int { return a.key.Compare(b.key) })
	for i := range rows {
		values[i] = rows[i].value
	}
}

// integerOwners is correlate for an integer key: each owner's key (has[i] false for a null, excluded from querying)
// and, when the query needs one (not SubSelect, whose subquery replaces it), the distinct non-null keys as tuples of
// the property's own values, in first-seen order — the list correlate would build.
func integerOwners(entities []any, property *core.PropertyMap, needDistinct bool) (keys []int64, has []bool, distinct []core.KeyTuple) {
	keys = make([]int64, len(entities))
	has = make([]bool, len(entities))
	var seen map[int64]struct{}
	if needDistinct {
		seen = make(map[int64]struct{}, len(entities))
	}
	for i, e := range entities {
		key, ok := readIntegerKey(property, reflect.ValueOf(e))
		if !ok {
			continue
		}
		keys[i], has[i] = key, true
		if needDistinct {
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				distinct = append(distinct, core.KeyTuple{property.Get(e)})
			}
		}
	}
	return keys, has, distinct
}

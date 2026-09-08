package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// crudCaseSpec is the conformance/crud-cases/*.json shape (spec/crud.md).
type crudCaseSpec struct {
	Name  string     `json:"name"`
	Steps []crudStep `json:"steps"`
}

type crudStep struct {
	Op     string                     `json:"op"`
	Entity string                     `json:"entity"`
	Values map[string]json.RawMessage `json:"values"`
	Key    json.RawMessage            `json:"key"`
	As     string                     `json:"as"`
	From   string                     `json:"from"`
	Expect struct {
		Error  string                     `json:"error"`
		Values map[string]json.RawMessage `json:"values"`
	} `json:"expect"`
}

// crudOps is one entity type's generated-CRUD surface, type-erased over `any`
// so the step runner below stays generic-free (Go has no reflection-based
// generic instantiation by name — dispatch happens once, in opsFor).
type crudOps struct {
	new    func() any
	insert func(ctx context.Context, db *orm.Db, entity any) error
	get    func(ctx context.Context, db *orm.Db, key any) (any, error)
	update func(ctx context.Context, db *orm.Db, entity any) error
	delKey func(ctx context.Context, db *orm.Db, key any) error
	delEnt func(ctx context.Context, db *orm.Db, entity any) error
	keyOf  func(db *orm.Db, entity any) (any, error)
}

// opsFor closes over T once so the rest of the runner deals only in `any` —
// the CODING-STANDARD's "dispatch on the entity name to a generic helper".
func opsFor[T any]() crudOps {
	return crudOps{
		new: func() any { return new(T) },
		insert: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.Insert(ctx, db, entity.(*T))
		},
		get: func(ctx context.Context, db *orm.Db, key any) (any, error) {
			value, err := orm.Get[T](ctx, db, key)
			if err != nil {
				return nil, err
			}
			return &value, nil
		},
		update: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.Update(ctx, db, entity.(*T))
		},
		delKey: func(ctx context.Context, db *orm.Db, key any) error {
			return orm.Delete[T](ctx, db, key)
		},
		delEnt: func(ctx context.Context, db *orm.Db, entity any) error {
			return orm.DeleteEntity(ctx, db, entity.(*T))
		},
		keyOf: func(db *orm.Db, entity any) (any, error) {
			m, err := db.Maps().Load(reflect.TypeFor[T]())
			if err != nil {
				return nil, err
			}
			values, err := m.KeyValues(entity)
			if err != nil {
				return nil, err
			}
			return values[0], nil
		},
	}
}

func opsForEntity(name string) (crudOps, error) {
	switch name {
	case "User":
		return opsFor[sample.User](), nil
	case "Transaction":
		return opsFor[sample.Transaction](), nil
	}
	return crudOps{}, fmt.Errorf("crud_cases_test: unknown entity %q", name)
}

// opsForEntityValue dispatches on a snapshot's runtime type: "from" steps
// carry no entity name in the JSON, only a captured instance.
func opsForEntityValue(entity any) (crudOps, error) {
	switch entity.(type) {
	case *sample.User:
		return opsFor[sample.User](), nil
	case *sample.Transaction:
		return opsFor[sample.Transaction](), nil
	}
	return crudOps{}, fmt.Errorf("crud_cases_test: unknown snapshot type %T", entity)
}

// TestCrudCases_BehaveAsSpecified is the conformance/crud-cases/ runner (§9,
// mirrors ConformanceCrudTests.cs): per case, a fresh database with the User
// and Transaction tables created from metadata, replaying insert/get/
// update/delete steps with "$last" keys and "as"/"from" snapshots.
func TestCrudCases_BehaveAsSpecified(t *testing.T) {
	for _, name := range testsupport.ConformanceCases(t, "crud-cases") {
		t.Run(name, func(t *testing.T) {
			var spec crudCaseSpec
			if err := json.Unmarshal(testsupport.ReadConformance(t, "crud-cases", name), &spec); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			ctx := context.Background()
			db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer db.Close()
			if err := orm.CreateTable[sample.User](ctx, db); err != nil {
				t.Fatalf("create User table: %v", err)
			}
			if err := orm.CreateTable[sample.Transaction](ctx, db); err != nil {
				t.Fatalf("create Transaction table: %v", err)
			}

			var lastKey any
			snapshots := map[string]any{}

			for _, step := range spec.Steps {
				result, stepErr := runCrudStep(ctx, db, step, &lastKey, snapshots)
				if stepErr != nil && core.CodeOf(stepErr) == "" {
					t.Fatalf("%s: step %q on %q: unexpected error: %v", name, step.Op, step.Entity, stepErr)
				}
				if core.CodeOf(stepErr) != step.Expect.Error {
					t.Fatalf("%s: step %q on %q: expected error %q, got %v",
						name, step.Op, step.Entity, step.Expect.Error, stepErr)
				}
				if stepErr == nil && result != nil && step.Expect.Values != nil {
					assertValues(t, db, result, step.Expect.Values, name)
				}
			}
		})
	}
}

// runCrudStep replays one step; the "get" result (for value assertions) is returned.
func runCrudStep(ctx context.Context, db *orm.Db, step crudStep, lastKey *any, snapshots map[string]any) (any, error) {
	switch step.Op {
	case "insert":
		ops, err := opsForEntity(step.Entity)
		if err != nil {
			return nil, err
		}
		entity := ops.new()
		if err := applyValues(db, entity, step.Values); err != nil {
			return nil, err
		}
		if err := ops.insert(ctx, db, entity); err != nil {
			return nil, err
		}
		key, err := ops.keyOf(db, entity)
		if err != nil {
			return nil, err
		}
		*lastKey = key
		return nil, nil

	case "get":
		ops, err := opsForEntity(step.Entity)
		if err != nil {
			return nil, err
		}
		key, err := stepKey(step, *lastKey)
		if err != nil {
			return nil, err
		}
		entity, err := ops.get(ctx, db, key)
		if err != nil {
			return nil, err
		}
		if step.As != "" {
			snapshots[step.As] = entity
		}
		return entity, nil

	case "update":
		entity, ok := snapshots[step.From]
		if !ok {
			return nil, fmt.Errorf("no snapshot %q", step.From)
		}
		if err := applyValues(db, entity, step.Values); err != nil {
			return nil, err
		}
		ops, err := opsForEntityValue(entity)
		if err != nil {
			return nil, err
		}
		return nil, ops.update(ctx, db, entity)

	case "delete":
		if step.From != "" {
			entity, ok := snapshots[step.From]
			if !ok {
				return nil, fmt.Errorf("no snapshot %q", step.From)
			}
			ops, err := opsForEntityValue(entity)
			if err != nil {
				return nil, err
			}
			return nil, ops.delEnt(ctx, db, entity)
		}
		ops, err := opsForEntity(step.Entity)
		if err != nil {
			return nil, err
		}
		key, err := stepKey(step, *lastKey)
		if err != nil {
			return nil, err
		}
		return nil, ops.delKey(ctx, db, key)

	default:
		return nil, fmt.Errorf("unknown op %q", step.Op)
	}
}

// stepKey resolves a step's "key" field: a literal, or "$last" for the most recent insert's key.
func stepKey(step crudStep, lastKey any) (any, error) {
	if len(step.Key) == 0 {
		return nil, fmt.Errorf("step has no key")
	}
	var decoded any
	if err := json.Unmarshal(step.Key, &decoded); err != nil {
		return nil, err
	}
	if text, ok := decoded.(string); ok && text == "$last" {
		if lastKey == nil {
			return nil, fmt.Errorf("no prior insert for $last")
		}
		return lastKey, nil
	}
	if number, ok := decoded.(float64); ok {
		return int64(number), nil
	}
	return decoded, nil
}

// applyValues sets values (keyed by column name, the conformance value
// encoding) onto entity, decoded per the property's Go type and token.
func applyValues(db *orm.Db, entity any, values map[string]json.RawMessage) error {
	m, err := db.Maps().Load(reflect.TypeOf(entity).Elem())
	if err != nil {
		return err
	}
	for column, raw := range values {
		property := m.PropertyByColumn(column)
		if property == nil {
			return fmt.Errorf("no mapped property for column %q on %s", column, m.EntityName())
		}
		value, err := decodeConformanceValue(raw, property)
		if err != nil {
			return err
		}
		if err := property.Set(entity, value); err != nil {
			return err
		}
	}
	return nil
}

// decodeConformanceValue reads one conformance-encoded JSON value into the
// Go value the property's token expects (spec/mapping-rules.md).
func decodeConformanceValue(raw json.RawMessage, property *core.PropertyMap) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	switch property.ColumnType {
	case core.TypeInt16, core.TypeInt32, core.TypeInt64:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return intOfKind(n, property.ValueType()), nil

	case core.TypeDecimal:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return orm.ParseDecimal(text)

	case core.TypeDouble, core.TypeFloat:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, err
		}
		if property.ValueType().Kind() == reflect.Float32 {
			return float32(f), nil
		}
		return f, nil

	case core.TypeBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		return b, nil

	case core.TypeGUID:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return orm.ParseGUID(text)

	case core.TypeDateTime:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return core.ParseMarked(text, "conformance")

	case core.TypeDateTimeOffset:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return core.ParseOffset(text, "conformance")

	case core.TypeDate:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return core.ParseDate(text, "conformance")

	case core.TypeTime:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return core.ParseTime(text, "conformance")

	case core.TypeEnumText, core.TypeEnumInt:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		names := core.EnumNamesOf(property.ValueType())
		index, ok := core.EnumIndex(names, text)
		if !ok {
			return nil, fmt.Errorf("%q is not a member of %s", text, property.ValueType())
		}
		return core.EnumValue(property.ValueType(), index, names).Interface(), nil

	default:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return text, nil
	}
}

func intOfKind(value int64, target reflect.Type) any {
	switch target.Kind() {
	case reflect.Int16:
		return int16(value)
	case reflect.Int32:
		return int32(value)
	default:
		return value
	}
}

// assertValues compares entity's mapped properties against expected (keyed by
// column name); decimals compare scale-normalized (core.Decimal.Equal), the
// rest compare as the encoded conformance text.
func assertValues(t *testing.T, db *orm.Db, entity any, expected map[string]json.RawMessage, caseName string) {
	t.Helper()
	m, err := db.Maps().Load(reflect.TypeOf(entity).Elem())
	if err != nil {
		t.Fatalf("%s: %v", caseName, err)
		return
	}

	for column, raw := range expected {
		property := m.PropertyByColumn(column)
		if property == nil {
			t.Errorf("%s: no mapped property for column %q", caseName, column)
			continue
		}
		actual := property.Get(entity)
		wantText, wantNull := expectedText(raw)

		if property.ColumnType == core.TypeDecimal {
			if wantNull {
				if actual != nil {
					t.Errorf("%s: column %s: expected null, got %v", caseName, column, actual)
				}
				continue
			}
			want, err := orm.ParseDecimal(wantText)
			if err != nil {
				t.Fatalf("%s: column %s: bad expected decimal %q: %v", caseName, column, wantText, err)
			}
			got, ok := actual.(orm.Decimal)
			if !ok || !got.Equal(want) {
				t.Errorf("%s: column %s: got %v, want %s", caseName, column, actual, want)
			}
			continue
		}

		gotText, gotNull := encodeForCompare(actual, property.ColumnType)
		if gotNull != wantNull || (!gotNull && gotText != wantText) {
			t.Errorf("%s: column %s: got %q (null=%v), want %q (null=%v)",
				caseName, column, gotText, gotNull, wantText, wantNull)
		}
	}
}

func encodeForCompare(value any, columnType core.ColumnType) (text string, isNull bool) {
	if value == nil {
		return "", true
	}
	switch columnType {
	case core.TypeGUID:
		if g, ok := value.(orm.GUID); ok {
			return g.String(), false
		}
	case core.TypeDateTime:
		if tm, ok := value.(time.Time); ok {
			return core.FormatUTC(tm), false
		}
	case core.TypeDateTimeOffset:
		if tm, ok := value.(time.Time); ok {
			return core.FormatOffset(tm), false
		}
	case core.TypeDate:
		if tm, ok := value.(time.Time); ok {
			return core.FormatDate(tm), false
		}
	case core.TypeTime:
		if tm, ok := value.(time.Time); ok {
			return core.FormatTime(tm), false
		}
	case core.TypeEnumText, core.TypeEnumInt:
		if name, ok := core.EnumName(value); ok {
			return name, false
		}
	case core.TypeBool:
		if b, ok := value.(bool); ok {
			if b {
				return "true", false
			}
			return "false", false
		}
	}
	return fmt.Sprint(value), false
}

// expectedText reduces one conformance-encoded expected value to comparable text (or null).
func expectedText(raw json.RawMessage) (text string, isNull bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return string(raw), false
	}
	switch v := decoded.(type) {
	case bool:
		if v {
			return "true", false
		}
		return "false", false
	case float64:
		if n, ok := normalizeJSONNumber(v).(int64); ok {
			return strconv.FormatInt(n, 10), false
		}
		return strconv.FormatFloat(v, 'g', -1, 64), false
	case string:
		return v, false
	default:
		return fmt.Sprint(decoded), false
	}
}

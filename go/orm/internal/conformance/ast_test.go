package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// sampleEntities is the ten fixture types by their conformance "entity" name
// (mirrors ConformanceAstTests.cs's assembly scan — Go has none, so this is
// the explicit stand-in).
var sampleEntities = map[string]reflect.Type{
	"User":                 reflect.TypeFor[sample.User](),
	"Role":                 reflect.TypeFor[sample.Role](),
	"UserRole":             reflect.TypeFor[sample.UserRole](),
	"Transaction":          reflect.TypeFor[sample.Transaction](),
	"TransactionDetail":    reflect.TypeFor[sample.TransactionDetail](),
	"UserProfile":          reflect.TypeFor[sample.UserProfile](),
	"DailySales":           reflect.TypeFor[sample.DailySales](),
	"MonthlySalesTotal":    reflect.TypeFor[sample.MonthlySalesTotal](),
	"UserActivityReport":   reflect.TypeFor[sample.UserActivityReport](),
	"UserTransactionTotal": reflect.TypeFor[sample.UserTransactionTotal](),
}

// astCase is the conformance/ast/*.json shape (spec/query-ast.md): a select
// AST as JSON, and per-dialect expected SQL/parameters or a dialect-neutral
// error code. This port has one dialect (sqlite); other dialects' expectations
// are read and ignored.
type astCase struct {
	Name   string          `json:"name"`
	Entity string          `json:"entity"`
	Select json.RawMessage `json:"select"`
	Expect struct {
		Error  string `json:"error"`
		SQLite struct {
			SQL        string        `json:"sql"`
			Parameters []interface{} `json:"parameters"`
		} `json:"sqlite"`
	} `json:"expect"`
}

// TestAst_RendersAsSpecified is the ast/ runner (§9, ADR-0020), mirroring
// ConformanceAstTests.cs: build the AST from JSON, render through the SQLite
// dialect, and compare the exact SQL and ordered parameter values, or the
// error code.
func TestAst_RendersAsSpecified(t *testing.T) {
	dir := testsupport.ConformanceFolder(t, "ast")
	for _, name := range testsupport.ConformanceCases(t, "ast") {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read conformance/ast/%s: %v", name, err)
			}
			var c astCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			entityType, ok := sampleEntities[c.Entity]
			if !ok {
				t.Fatalf("%s: unknown entity %q", name, c.Entity)
			}
			m, err := metadata.NewLoader(nil).Load(entityType)
			if err != nil {
				t.Fatalf("%s: loading %s: %v", name, c.Entity, err)
			}

			ast, err := parseSelect(m, c.Select)
			if err != nil {
				t.Fatalf("%s: parsing select: %v", name, err)
			}

			var bound []any
			bind := func(value any, _ *core.PropertyMap) (string, error) {
				bound = append(bound, value)
				return "@c" + itoa(len(bound)-1), nil
			}

			sql, renderErr := sqlite.New().SelectSQL(ast, bind)

			if c.Expect.Error != "" {
				if core.CodeOf(renderErr) != c.Expect.Error {
					t.Errorf("expected error %s, got %v", c.Expect.Error, renderErr)
				}
				return
			}

			if renderErr != nil {
				t.Fatalf("unexpected error: %v", renderErr)
			}
			if sql != c.Expect.SQLite.SQL {
				t.Errorf("sql:\n got  %q\n want %q", sql, c.Expect.SQLite.SQL)
			}
			wantParams := decodeAllAny(c.Expect.SQLite.Parameters)
			if !paramsEqual(wantParams, bound) {
				t.Errorf("parameters:\n got  %#v\n want %#v", bound, wantParams)
			}
		})
	}
}

// parseSelect builds a core.SelectAst from the JSON encoding in spec/query-ast.md.
func parseSelect(m *core.EntityMap, raw json.RawMessage) (*core.SelectAst, error) {
	var doc struct {
		Where   []json.RawMessage `json:"where"`
		OrderBy []struct {
			Property string `json:"property"`
			Order    string `json:"order"`
		} `json:"orderBy"`
		Limit  *int64 `json:"limit"`
		Offset *int64 `json:"offset"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
	}

	where := make([]core.Criteria, len(doc.Where))
	for i, node := range doc.Where {
		c, err := parseCriteria(node)
		if err != nil {
			return nil, err
		}
		where[i] = c
	}

	orderings := make([]core.Ordering, len(doc.OrderBy))
	for i, o := range doc.OrderBy {
		order := core.Asc
		switch o.Order {
		case "", "asc":
			order = core.Asc
		case "desc":
			order = core.Desc
		}
		orderings[i] = core.Ordering{Property: o.Property, Order: order}
	}

	return &core.SelectAst{Map: m, Where: where, Orderings: orderings, Limit: doc.Limit, Offset: doc.Offset}, nil
}

func parseCriteria(raw json.RawMessage) (core.Criteria, error) {
	var node struct {
		Op       string            `json:"op"`
		Property string            `json:"property"`
		Value    json.RawMessage   `json:"value"`
		Values   []json.RawMessage `json:"values"`
		Args     []json.RawMessage `json:"args"`
		Arg      json.RawMessage   `json:"arg"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, err
	}

	switch node.Op {
	case "eq":
		return &core.Comparison{Property: node.Property, Operator: "=", Value: decodeValue(node.Value)}, nil
	case "ne":
		return &core.Comparison{Property: node.Property, Operator: "<>", Value: decodeValue(node.Value)}, nil
	case "gt":
		return &core.Comparison{Property: node.Property, Operator: ">", Value: decodeValue(node.Value)}, nil
	case "ge":
		return &core.Comparison{Property: node.Property, Operator: ">=", Value: decodeValue(node.Value)}, nil
	case "lt":
		return &core.Comparison{Property: node.Property, Operator: "<", Value: decodeValue(node.Value)}, nil
	case "le":
		return &core.Comparison{Property: node.Property, Operator: "<=", Value: decodeValue(node.Value)}, nil
	case "like":
		return &core.Comparison{Property: node.Property, Operator: "like", Value: decodeValue(node.Value)}, nil
	case "in":
		return &core.InList{Property: node.Property, Values: decodeAll(node.Values)}, nil
	case "is_null":
		return &core.NullCheck{Property: node.Property, Negated: false}, nil
	case "is_not_null":
		return &core.NullCheck{Property: node.Property, Negated: true}, nil
	case "and":
		children, err := parseCriteriaList(node.Args)
		if err != nil {
			return nil, err
		}
		return &core.Composite{Operator: "and", Children: children}, nil
	case "or":
		children, err := parseCriteriaList(node.Args)
		if err != nil {
			return nil, err
		}
		return &core.Composite{Operator: "or", Children: children}, nil
	case "not":
		inner, err := parseCriteria(node.Arg)
		if err != nil {
			return nil, err
		}
		return &core.Negation{Inner: inner}, nil
	}
	return nil, core.Errorf("QRY-006", "ast case", "unknown op %q", node.Op)
}

func parseCriteriaList(raws []json.RawMessage) ([]core.Criteria, error) {
	list := make([]core.Criteria, len(raws))
	for i, raw := range raws {
		c, err := parseCriteria(raw)
		if err != nil {
			return nil, err
		}
		list[i] = c
	}
	return list, nil
}

// decodeValue converts one raw JSON value the way the AST needs it: a whole
// number decodes as int64 (matching bound parameters like limit/offset and
// integer property values), any other number as float64 — the conformance
// fixtures' documented numeric encoding (CLAUDE.md §9).
func decodeValue(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return normalizeNumber(v)
}

func decodeAll(raws []json.RawMessage) []any {
	if raws == nil {
		return nil
	}
	values := make([]any, len(raws))
	for i, raw := range raws {
		values[i] = decodeValue(raw)
	}
	return values
}

func decodeAllAny(values []interface{}) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = normalizeNumber(v)
	}
	return out
}

// normalizeNumber converts encoding/json's default float64 decode to int64
// when the value is integral, so e.g. `1` compares equal to the int64 the
// renderer bound.
func normalizeNumber(v any) any {
	if f, ok := v.(float64); ok {
		return normalizeJSONNumber(f)
	}
	return v
}

// paramsEqual compares bound parameter slices element-wise; nil and an empty
// slice both mean "no parameters" (reflect.DeepEqual would tell them apart).
func paramsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

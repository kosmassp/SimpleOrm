// Package render is the shared reference rendering of a SelectAst (ADR-0020,
// spec/query-ast.md): explicit column list (never *), parameters bound in
// render order (@c0…: WHERE first, then limit, then offset), dialect knobs
// consulted where SQL differs. A dialect's SelectSQL normally delegates here
// and overrides what its SQL disagrees with — the AST is the contract, this
// rendering is the reference.
//
// This port mirrors dotnet/src/SimpleOrm/AnsiSelectRenderer.cs minus joins,
// projection, and subquery membership — Level 2 features not yet in scope for
// the Go port (ADR-0027, CLAUDE.md §7b M2/M4). core.SelectAst and core.Criteria
// carry no such nodes, so the shape below is already the Level 1 subset; the
// decomposition (Render/column/Resolve) stays ready for them.
package render

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// SelectSQL renders the select: an explicit column list from ast.Map.Properties
// in metadata order, the WHERE predicate (one bare, two or more implicitly
// ANDed), ORDER BY, and paging through the dialect's LimitOffsetClause. Pure:
// no database, no side effects beyond calling bind in render order.
func SelectSQL(dialect core.Dialect, ast *core.SelectAst, bind core.BindCriteriaParameter) (string, error) {
	m := ast.Map
	queryName := m.EntityName() + " criteria"

	if ast.Limit != nil && *ast.Limit < 0 {
		return "", core.Errorf("QRY-008", queryName,
			"negative limit %d — dialects disagree on its meaning (SQLite: no limit at all); refuse the arithmetic bug instead",
			*ast.Limit)
	}
	if ast.Offset != nil && *ast.Offset < 0 {
		return "", core.Errorf("QRY-008", queryName,
			"negative offset %d — dialects disagree on its meaning (SQLite: no limit at all); refuse the arithmetic bug instead",
			*ast.Offset)
	}

	columns := make([]string, len(m.Properties))
	for i, p := range m.Properties {
		columns[i] = dialect.QuoteIdentifier(p.ColumnName)
	}

	var b strings.Builder
	b.WriteString("select ")
	b.WriteString(strings.Join(columns, ", "))
	b.WriteString(" from ")
	b.WriteString(dialect.QuoteIdentifier(m.RelationName))

	if len(ast.Where) > 0 {
		var predicate core.Criteria
		if len(ast.Where) == 1 {
			predicate = ast.Where[0]
		} else {
			predicate = core.And(ast.Where...)
		}
		rendered, err := renderCriteria(predicate, m, queryName, dialect, bind)
		if err != nil {
			return "", err
		}
		b.WriteString(" where ")
		b.WriteString(rendered)
	}

	if len(ast.Orderings) > 0 {
		parts := make([]string, len(ast.Orderings))
		for i, o := range ast.Orderings {
			property, err := Resolve(m, o.Property, queryName)
			if err != nil {
				return "", err
			}
			part := dialect.QuoteIdentifier(property.ColumnName)
			if o.Order == core.Desc {
				part += " desc"
			}
			parts[i] = part
		}
		b.WriteString(" order by ")
		b.WriteString(strings.Join(parts, ", "))
	}

	if ast.Limit != nil || ast.Offset != nil {
		if len(ast.Orderings) == 0 && dialect.PagingRequiresOrderBy() {
			// The dialect's paging clause is only legal after ORDER BY (SQL
			// Server); an unordered page is already order-arbitrary, so the
			// constant placeholder changes nothing observable (ADR-0024).
			b.WriteString(" order by (select null)")
		}

		var limitParameter, offsetParameter string
		if ast.Limit != nil {
			p, err := bind(*ast.Limit, nil)
			if err != nil {
				return "", err
			}
			limitParameter = p
		}
		if ast.Offset != nil {
			p, err := bind(*ast.Offset, nil)
			if err != nil {
				return "", err
			}
			offsetParameter = p
		}
		clause := dialect.LimitOffsetClause(limitParameter, offsetParameter)
		if clause != "" {
			b.WriteByte(' ')
			b.WriteString(clause)
		}
	}

	return b.String(), nil
}

// renderCriteria renders one predicate node (ADR-0020): null semantics on
// Comparison, the empty/null rules on InList, composite parenthesization, and
// Negation's "not " prefix.
func renderCriteria(
	criteria core.Criteria, m *core.EntityMap, queryName string, dialect core.Dialect, bind core.BindCriteriaParameter,
) (string, error) {
	switch c := criteria.(type) {
	case *core.Comparison:
		if c.Value == nil {
			col, err := column(m, c.Property, queryName, dialect)
			if err != nil {
				return "", err
			}
			switch c.Operator {
			case "=":
				return col + " is null", nil
			case "<>":
				return col + " is not null", nil
			default:
				return "", core.Errorf("QRY-007", queryName,
					"'%s %s null' has no meaning in SQL; use IsNull/IsNotNull", c.Property, c.Operator)
			}
		}

		property, err := Resolve(m, c.Property, queryName)
		if err != nil {
			return "", err
		}
		col, err := column(m, c.Property, queryName, dialect)
		if err != nil {
			return "", err
		}
		placeholder, err := bind(c.Value, property)
		if err != nil {
			return "", err
		}
		return col + " " + c.Operator + " " + placeholder, nil

	case *core.InList:
		if len(c.Values) == 0 {
			if _, err := Resolve(m, c.Property, queryName); err != nil {
				return "", err // an unknown property is QRY-006 even when empty
			}
			return "1 = 0", nil
		}
		for _, value := range c.Values {
			if value == nil {
				return "", core.Errorf("QRY-007", queryName,
					"the IN list for '%s' contains null, which SQL IN can never match; combine Or(In(…), IsNull(…))",
					c.Property)
			}
		}

		property, err := Resolve(m, c.Property, queryName)
		if err != nil {
			return "", err
		}
		col, err := column(m, c.Property, queryName, dialect)
		if err != nil {
			return "", err
		}
		placeholders := make([]string, len(c.Values))
		for i, value := range c.Values {
			p, err := bind(value, property)
			if err != nil {
				return "", err
			}
			placeholders[i] = p
		}
		return col + " in (" + strings.Join(placeholders, ", ") + ")", nil

	case *core.NullCheck:
		col, err := column(m, c.Property, queryName, dialect)
		if err != nil {
			return "", err
		}
		if c.Negated {
			return col + " is not null", nil
		}
		return col + " is null", nil

	case *core.Composite:
		if len(c.Children) == 0 {
			// The identity truth-values: an empty AND is true, an empty OR is
			// false — dynamic composition may legitimately produce either, and
			// invalid SQL ("()") names nothing (§2).
			if c.Operator == "and" {
				return "1 = 1", nil
			}
			return "1 = 0", nil
		}
		parts := make([]string, len(c.Children))
		for i, child := range c.Children {
			part, err := renderCriteria(child, m, queryName, dialect, bind)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "(" + strings.Join(parts, " "+c.Operator+" ") + ")", nil

	case *core.Negation:
		inner, err := renderCriteria(c.Inner, m, queryName, dialect, bind)
		if err != nil {
			return "", err
		}
		return "not " + inner, nil

	default:
		return "", core.Errorf("QRY-006", queryName, "unknown criteria node %T", criteria)
	}
}

func column(m *core.EntityMap, property string, queryName string, dialect core.Dialect) (string, error) {
	p, err := Resolve(m, property, queryName)
	if err != nil {
		return "", err
	}
	return dialect.QuoteIdentifier(p.ColumnName), nil
}

// Resolve is property-name resolution (spec/query-ast.md): exact match first,
// then case-insensitive — but a case-insensitive match that fits more than one
// property is ambiguous, and ambiguity is QRY-006, never a silent first-wins.
func Resolve(m *core.EntityMap, property string, queryName string) (*core.PropertyMap, error) {
	for _, p := range m.Properties {
		if p.PropertyName == property {
			return p, nil
		}
	}

	var matches []*core.PropertyMap
	for _, p := range m.Properties {
		if strings.EqualFold(p.PropertyName, property) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, core.Errorf("QRY-006", queryName, "'%s' is not a mapped property of %s", property, m.EntityName())
	default:
		names := make([]string, len(matches))
		for i, p := range matches {
			names[i] = p.PropertyName
		}
		return nil, core.Errorf("QRY-006", queryName,
			"'%s' is ambiguous on %s: matches %s", property, m.EntityName(), strings.Join(names, ", "))
	}
}

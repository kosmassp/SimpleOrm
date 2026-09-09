// Package render is the shared reference rendering of a SelectAst (ADR-0020,
// spec/query-ast.md): explicit column list (never *), parameters bound in
// render order (@c0…: WHERE first, then limit, then offset), dialect knobs
// consulted where SQL differs. A dialect's SelectSQL normally delegates here
// and overrides what its SQL disagrees with — the AST is the contract, this
// rendering is the reference.
//
// It also renders the Level 2 extensions (spec/query-ast.md "Level 2
// extensions: projection, joins, subquery membership", ADR-0022 add.1): a
// Projection restricts and orders the selected root columns; Joins render as
// LEFT JOINs with the root aliased "t" and every root/projected-join column
// re-aliased "<alias>_<column>"; SubqueryMembership (in_select) renders a row
// value or, for a dialect without row-value IN, a correlated EXISTS over the
// same subquery, rendered through this same function so its placeholders
// continue the outer numbering. Built from spec/query-ast.md (Level 2 is not
// a line-by-line port of the C# renderer); the decomposition
// (SelectSQL/renderCriteria/column/Resolve) is what made the extension a pure
// addition — every existing Level 1 rendering stays byte-identical to the
// reference's rendered SQL (CODING-STANDARD §5).
package render

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// SelectSQL renders the select: an explicit column list (ast.Projection when
// set, else every mapped column of ast.Map, in metadata order), the WHERE
// predicate (one bare, two or more implicitly ANDed), ORDER BY, and paging
// through the dialect's LimitOffsetClause. When ast.Joins is non-empty the
// root aliases "t", every selected column re-aliases "<alias>_<column>", and
// root predicates/orderings qualify with "t.". Pure: no database, no side
// effects beyond calling bind in render order.
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

	hasJoins := len(ast.Joins) > 0
	// The root needs its own alias whenever joins hang columns off it, or
	// whenever a composite subquery membership somewhere in WHERE has to
	// rewrite as a correlated EXISTS (spec: "the root gains alias t for the
	// correlation — without re-aliasing its columns, which only joins do").
	needsRootAlias := hasJoins || requiresExistsRewrite(ast.Where, dialect)
	rootAlias := ""
	if needsRootAlias {
		rootAlias = "t"
	}

	rootProperties := m.Properties
	if ast.Projection != nil {
		rootProperties = ast.Projection
	}

	columns := make([]string, 0, len(rootProperties)+joinColumnCount(ast.Joins))
	for _, p := range rootProperties {
		expr := aliasQualifiedColumn(dialect, rootAlias, p.ColumnName)
		if hasJoins {
			expr += " as " + dialect.QuoteIdentifier(rootAlias+"_"+p.ColumnName)
		}
		columns = append(columns, expr)
	}
	for _, j := range ast.Joins {
		if !j.Project {
			continue
		}
		for _, p := range j.Target.Properties {
			expr := j.Alias + "." + dialect.QuoteIdentifier(p.ColumnName) + " as " + dialect.QuoteIdentifier(j.Alias+"_"+p.ColumnName)
			columns = append(columns, expr)
		}
	}

	var b strings.Builder
	b.WriteString("select ")
	b.WriteString(strings.Join(columns, ", "))
	b.WriteString(" from ")
	b.WriteString(dialect.QuoteIdentifier(m.RelationName))
	if needsRootAlias {
		b.WriteString(" ")
		b.WriteString(rootAlias)
	}

	if hasJoins {
		joinClauses, err := renderJoins(ast, dialect, queryName)
		if err != nil {
			return "", err
		}
		for _, clause := range joinClauses {
			b.WriteString(" left join ")
			b.WriteString(clause)
		}
	}

	if len(ast.Where) > 0 {
		var predicate core.Criteria
		if len(ast.Where) == 1 {
			predicate = ast.Where[0]
		} else {
			predicate = core.And(ast.Where...)
		}
		rendered, err := renderCriteria(predicate, m, queryName, dialect, bind, rootAlias)
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
			part := aliasQualifiedColumn(dialect, rootAlias, property.ColumnName)
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

// joinColumnCount is a capacity hint: the number of columns every projected
// join contributes.
func joinColumnCount(joins []*core.SelectJoin) int {
	n := 0
	for _, j := range joins {
		if j.Project {
			n += len(j.Target.Properties)
		}
	}
	return n
}

// aliasQualifiedColumn quotes a column name through the dialect and, when
// alias is non-empty, prefixes it "<alias>.". Aliases themselves are never
// quoted (spec/query-ast.md).
func aliasQualifiedColumn(dialect core.Dialect, alias, columnName string) string {
	col := dialect.QuoteIdentifier(columnName)
	if alias == "" {
		return col
	}
	return alias + "." + col
}

// renderJoins renders every join's " <relation> <alias> on <onClause>" (the
// caller prepends "left join "), in declaration order. An ON pair names
// properties resolved through the target's map and the parent's map — the
// parent is the join's own root when ParentAlias is "", or an earlier join's
// target when it names that join's alias — exactly like predicates (QRY-006
// when unknown), tagged with the root's query name.
func renderJoins(ast *core.SelectAst, dialect core.Dialect, queryName string) ([]string, error) {
	aliasMaps := make(map[string]*core.EntityMap, len(ast.Joins))
	for _, j := range ast.Joins {
		aliasMaps[j.Alias] = j.Target
	}

	clauses := make([]string, len(ast.Joins))
	for i, j := range ast.Joins {
		parentAlias := j.ParentAlias
		var parentMap *core.EntityMap
		if parentAlias == "" {
			parentAlias = "t"
			parentMap = ast.Map
		} else if m, ok := aliasMaps[parentAlias]; ok {
			parentMap = m
		} else {
			return nil, core.Errorf("QRY-006", queryName,
				"join %q names parent alias %q, which is not a declared join", j.Alias, parentAlias)
		}

		pairs := make([]string, len(j.On))
		for k, pair := range j.On {
			parentProperty, err := Resolve(parentMap, pair.ParentProperty, queryName)
			if err != nil {
				return nil, err
			}
			targetProperty, err := Resolve(j.Target, pair.TargetProperty, queryName)
			if err != nil {
				return nil, err
			}
			pairs[k] = j.Alias + "." + dialect.QuoteIdentifier(targetProperty.ColumnName) +
				" = " + parentAlias + "." + dialect.QuoteIdentifier(parentProperty.ColumnName)
		}

		clauses[i] = dialect.QuoteIdentifier(j.Target.RelationName) + " " + j.Alias + " on " + strings.Join(pairs, " and ")
	}
	return clauses, nil
}

// requiresExistsRewrite reports whether any SubqueryMembership reachable from
// the predicate list (through Composite/Negation nesting) is a composite
// membership (more than one property) on a dialect without row-value IN — the
// one case that forces the root to gain an alias for the correlation, even
// with no joins.
func requiresExistsRewrite(where []core.Criteria, dialect core.Dialect) bool {
	for _, c := range where {
		if requiresExistsRewriteNode(c, dialect) {
			return true
		}
	}
	return false
}

func requiresExistsRewriteNode(criteria core.Criteria, dialect core.Dialect) bool {
	switch c := criteria.(type) {
	case *core.SubqueryMembership:
		return len(c.Properties) > 1 && !dialect.SupportsRowValueIn()
	case *core.Composite:
		for _, child := range c.Children {
			if requiresExistsRewriteNode(child, dialect) {
				return true
			}
		}
	case *core.Negation:
		return requiresExistsRewriteNode(c.Inner, dialect)
	}
	return false
}

// renderCriteria renders one predicate node (ADR-0020): null semantics on
// Comparison, the empty/null rules on InList, composite parenthesization,
// Negation's "not " prefix, and subquery membership (Level 2). rootAlias is
// "" for a plain select, or "t" when the root needs qualifying (joins, or a
// forced EXISTS rewrite elsewhere in the tree) — every root column reference
// picks it up uniformly, so a query mixing a qualifying reason with an
// otherwise-unaliased predicate still parses.
func renderCriteria(
	criteria core.Criteria, m *core.EntityMap, queryName string, dialect core.Dialect, bind core.BindCriteriaParameter, rootAlias string,
) (string, error) {
	switch c := criteria.(type) {
	case *core.Comparison:
		if c.Value == nil {
			col, err := column(m, c.Property, queryName, dialect, rootAlias)
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
		col, err := column(m, c.Property, queryName, dialect, rootAlias)
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
		col, err := column(m, c.Property, queryName, dialect, rootAlias)
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
		col, err := column(m, c.Property, queryName, dialect, rootAlias)
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
			part, err := renderCriteria(child, m, queryName, dialect, bind, rootAlias)
			if err != nil {
				return "", err
			}
			parts[i] = part
		}
		return "(" + strings.Join(parts, " "+c.Operator+" ") + ")", nil

	case *core.Negation:
		inner, err := renderCriteria(c.Inner, m, queryName, dialect, bind, rootAlias)
		if err != nil {
			return "", err
		}
		return "not " + inner, nil

	case *core.SubqueryMembership:
		return renderSubqueryMembership(c, m, queryName, dialect, bind, rootAlias)

	default:
		return "", core.Errorf("QRY-006", queryName, "unknown criteria node %T", criteria)
	}
}

// renderSubqueryMembership renders in_select (spec/query-ast.md "Subquery
// membership"): a single property renders "<col> in (<subquery>)"; several
// render a row value "(<c1>, <c2>) in (<subquery>)" when the dialect supports
// it, else a correlated EXISTS over the same subquery aliased "s", correlating
// each subquery-projected column against the root property it was compared to
// (rootAlias is always non-empty here — requiresExistsRewrite forces it). The
// subquery renders through the same SelectSQL and the same bind closure, so
// its placeholders continue the outer numbering.
func renderSubqueryMembership(
	c *core.SubqueryMembership, m *core.EntityMap, queryName string, dialect core.Dialect, bind core.BindCriteriaParameter, rootAlias string,
) (string, error) {
	properties := make([]*core.PropertyMap, len(c.Properties))
	columns := make([]string, len(c.Properties))
	for i, name := range c.Properties {
		p, err := Resolve(m, name, queryName)
		if err != nil {
			return "", err
		}
		properties[i] = p
		columns[i] = aliasQualifiedColumn(dialect, rootAlias, p.ColumnName)
	}

	subquerySQL, err := SelectSQL(dialect, c.Subquery, bind)
	if err != nil {
		return "", err
	}

	if len(properties) == 1 {
		return columns[0] + " in (" + subquerySQL + ")", nil
	}

	if dialect.SupportsRowValueIn() {
		return "(" + strings.Join(columns, ", ") + ") in (" + subquerySQL + ")", nil
	}

	// The correlated EXISTS rewrite (ADR-0024, no row-value IN): the subquery
	// becomes derived table "s"; each compared column correlates against the
	// projected column in the same position. requiresExistsRewrite already
	// forced rootAlias non-empty for this case; default defensively to "t" if
	// that invariant is ever violated.
	alias := rootAlias
	if alias == "" {
		alias = "t"
	}

	projected := c.Subquery.Projection
	if projected == nil {
		projected = c.Subquery.Map.Properties
	}
	if len(projected) != len(properties) {
		return "", core.Errorf("QRY-006", queryName,
			"in_select over %s projects %d column(s) but %d propert(y/ies) are compared",
			c.Subquery.Map.EntityName(), len(projected), len(properties))
	}

	correlations := make([]string, len(properties))
	for i, p := range properties {
		correlations[i] = "s." + dialect.QuoteIdentifier(projected[i].ColumnName) + " = " + alias + "." + dialect.QuoteIdentifier(p.ColumnName)
	}

	return "exists (select 1 from (" + subquerySQL + ") s where " + strings.Join(correlations, " and ") + ")", nil
}

func column(m *core.EntityMap, property string, queryName string, dialect core.Dialect, rootAlias string) (string, error) {
	p, err := Resolve(m, property, queryName)
	if err != nil {
		return "", err
	}
	return aliasQualifiedColumn(dialect, rootAlias, p.ColumnName), nil
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

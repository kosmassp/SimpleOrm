package core

// SortOrder is the sort direction token: for orderings and for index column
// streams (ADR-0007 add.3), where it follows the column it applies to.
type SortOrder int

const (
	Asc SortOrder = iota
	Desc
)

// Token is the conformance encoding ("asc"/"desc").
func (o SortOrder) Token() string {
	if o == Desc {
		return "desc"
	}
	return "asc"
}

func (o SortOrder) String() string { return o.Token() }

// Ordering is one ORDER BY term of a criteria query: a property name and a direction.
type Ordering struct {
	Property string
	Order    SortOrder
}

// SelectAst is the criteria query as data (§10.4, ADR-0012/0020): source
// metadata, an implicitly ANDed predicate list, orderings, and paging. Every
// front-end produces this and never SQL text; the dialect renders it
// (Dialect.SelectSQL). Property names, not column names: the renderer resolves
// them through the metadata (QRY-006). GROUP BY is deliberately absent —
// aggregations are statement entities. Projection and Joins are the Level 2
// extensions (spec/query-ast.md "Level 2 extensions", ADR-0022 add.1): eager
// loading builds them; no front-end exposes them directly.
type SelectAst struct {
	Map *EntityMap
	// Where holds the predicates, implicitly ANDed. Empty means no WHERE clause.
	Where     []Criteria
	Orderings []Ordering
	Limit     *int64
	Offset    *int64
	// Projection lists the root properties to select, in order; nil selects
	// every mapped column (a subquery selects only what it feeds).
	Projection []*PropertyMap
	// Joins are LEFT JOINs for join-mode eager loading; empty for plain queries.
	Joins []*SelectJoin
}

// SelectJoin is one joined relation of a select (spec/query-ast.md): LEFT JOIN
// Target aliased Alias, ON equality pairs between the parent's properties and
// the target's. Only projected joins contribute columns (a many-to-many's link
// joins without projecting). The aliases are part of the AST, not chosen by
// the renderer.
type SelectJoin struct {
	Target *EntityMap
	Alias  string
	// ParentAlias is the alias this join hangs off; "" joins to the root.
	ParentAlias string
	On          []JoinPair
	// Project: whether the join's columns are selected, aliased <alias>_<column>.
	Project bool
}

// JoinPair is one ON equality: parent property = target property, each
// resolved through its own map.
type JoinPair struct {
	ParentProperty string
	TargetProperty string
}

// Criteria is the query AST (§10.4, ADR-0012): explicit trees built from the
// factories below — the portable core every language expresses natively.
// Property names (not column names) resolve through the entity's metadata at
// render time; values always bind as parameters. Composition is explicit, so
// SQL's and/or precedence ambiguity cannot occur.
type Criteria interface {
	criteria()
}

// Comparison is a binary comparison; a nil Value renders is [not] null for
// = and <> (ADR-0020) and is QRY-007 for the ordered operators and like.
type Comparison struct {
	Property string
	Operator string
	Value    any
}

// InList is membership: empty renders the false predicate; a nil element is QRY-007.
type InList struct {
	Property string
	Values   []any
}

// NullCheck is the explicit is [not] null.
type NullCheck struct {
	Property string
	Negated  bool
}

// Composite is and/or over children; empty renders its identity truth-value.
type Composite struct {
	Operator string
	Children []Criteria
}

// Negation is not over one node.
type Negation struct {
	Inner Criteria
}

// SubqueryMembership is the Level 2 in_select predicate (spec/query-ast.md,
// ADR-0022 add.1 — SubSelect eager loading): the listed root properties, as a
// row value when more than one, in (select …) over Subquery, whose projection
// has the same arity. The subquery renders through the same renderer and bind
// function (placeholders continue the outer numbering); where the dialect has
// no row-value IN, the renderer rewrites a composite membership as a
// correlated EXISTS over the aliased root.
type SubqueryMembership struct {
	Properties []string
	Subquery   *SelectAst
}

func (*Comparison) criteria()         {}
func (*InList) criteria()             {}
func (*NullCheck) criteria()          {}
func (*Composite) criteria()          {}
func (*Negation) criteria()           {}
func (*SubqueryMembership) criteria() {}

// InSelect is subquery membership: properties in (select …) over subquery.
// Part of the Level 2 AST; loading builds it and the conformance runner
// replays it — the criteria chain does not expose it.
func InSelect(properties []string, subquery *SelectAst) Criteria {
	return &SubqueryMembership{Properties: properties, Subquery: subquery}
}

// Eq is equality; nil renders is null (ADR-0020) — never = NULL, which silently matches nothing.
func Eq(property string, value any) Criteria { return &Comparison{property, "=", value} }

// Ne is inequality; nil renders is not null (ADR-0020).
func Ne(property string, value any) Criteria { return &Comparison{property, "<>", value} }

// Gt is a > comparison; unlike Eq/Ne, a nil value is QRY-007 (ADR-0020).
func Gt(property string, value any) Criteria { return &Comparison{property, ">", value} }

// Ge is a >= comparison; unlike Eq/Ne, a nil value is QRY-007 (ADR-0020).
func Ge(property string, value any) Criteria { return &Comparison{property, ">=", value} }

// Lt is a < comparison; unlike Eq/Ne, a nil value is QRY-007 (ADR-0020).
func Lt(property string, value any) Criteria { return &Comparison{property, "<", value} }

// Le is a <= comparison; unlike Eq/Ne, a nil value is QRY-007 (ADR-0020).
func Le(property string, value any) Criteria { return &Comparison{property, "<=", value} }

// Like is SQL LIKE; the caller supplies the wildcards (%, _).
func Like(property string, pattern string) Criteria { return &Comparison{property, "like", pattern} }

// In is membership. One generic signature covers every C# overload: a typed
// slice spreads (In("ID", ids...)), literals list (In("Name", "Ada", "Grace")),
// and a lone string is a one-element list — the string-is-IEnumerable<char>
// trap of the reference cannot occur. An empty list matches no rows, visibly.
func In[T any](property string, values ...T) Criteria {
	boxed := make([]any, len(values))
	for i, v := range values {
		boxed[i] = v
	}
	return &InList{property, boxed}
}

// IsNull is the explicit is-null check.
func IsNull(property string) Criteria { return &NullCheck{property, false} }

// IsNotNull is the explicit is-not-null check.
func IsNotNull(property string) Criteria { return &NullCheck{property, true} }

// And composes criteria with AND; an empty list renders its identity truth-value (true).
func And(criteria ...Criteria) Criteria { return &Composite{"and", criteria} }

// Or composes criteria with OR; an empty list renders its identity truth-value (false).
func Or(criteria ...Criteria) Criteria { return &Composite{"or", criteria} }

// Not negates one criteria node.
func Not(criteria Criteria) Criteria { return &Negation{criteria} }

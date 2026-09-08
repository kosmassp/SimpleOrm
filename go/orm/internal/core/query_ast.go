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
// aggregations are statement entities. Joins and projections arrive with
// Level 2 eager loading and extend this type rather than replace it.
type SelectAst struct {
	Map *EntityMap
	// Where holds the predicates, implicitly ANDed. Empty means no WHERE clause.
	Where     []Criteria
	Orderings []Ordering
	Limit     *int64
	Offset    *int64
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

func (*Comparison) criteria() {}
func (*InList) criteria()     {}
func (*NullCheck) criteria()  {}
func (*Composite) criteria()  {}
func (*Negation) criteria()   {}

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

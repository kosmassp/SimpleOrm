package core

// FetchMode chooses how eager loading (Include) fills the included navigations
// (spec/loading.md "Eager loading", ADR-0022 + add.1). Every mode loads an
// identical graph; they differ only in round trips and data shape.
type FetchMode int

const (
	// FetchMultiQuery (the default): the root query plus one batched key-list
	// query per navigation (two for many-to-many); chunks past the parameter
	// budget; paging always correct.
	FetchMultiQuery FetchMode = iota
	// FetchSubSelect: the root query plus one query per navigation filtering
	// IN (select … from the root query); never chunks; a paged root gains a
	// key-tiebroken ordering applied to both evaluations.
	FetchSubSelect
	// FetchJoin: one SELECT with LEFT JOINs; a collection include refuses
	// paging (REL-005) and at most one collection navigation joins (REL-006).
	FetchJoin
)

// Token is the conformance spelling ("MultiQuery", "SubSelect", "Join").
func (m FetchMode) Token() string {
	switch m {
	case FetchSubSelect:
		return "SubSelect"
	case FetchJoin:
		return "Join"
	default:
		return "MultiQuery"
	}
}

func (m FetchMode) String() string { return m.Token() }

// LoadChunkSize is the reference's owner budget per batched key-list query
// (spec/loading.md: "chunked only past the parameter budget — reference: 500
// owners per query").
const LoadChunkSize = 500

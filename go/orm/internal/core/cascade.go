package core

import "strings"

// CascadeOperations is the set of unit-of-work operations a navigation
// cascades (Level 3 M2, ADR-0034 §7c, spec/unit-of-work.md "Cascades —
// declared, never inferred"): Persist, Remove, both, or none. A flag set,
// because a navigation may declare both.
type CascadeOperations int

const (
	// CascadePersist cascades Persist to the navigation's targets.
	CascadePersist CascadeOperations = 1 << iota
	// CascadeRemove cascades Remove to the navigation's targets.
	CascadeRemove
)

// Has reports whether every operation in op is in the set.
func (c CascadeOperations) Has(op CascadeOperations) bool { return op != 0 && c&op == op }

// Tokens are the export's `cascade` values in the spec's order: persist before remove.
func (c CascadeOperations) Tokens() []string {
	var tokens []string
	if c.Has(CascadePersist) {
		tokens = append(tokens, "persist")
	}
	if c.Has(CascadeRemove) {
		tokens = append(tokens, "remove")
	}
	return tokens
}

func (c CascadeOperations) String() string {
	if c == 0 {
		return "none"
	}
	return strings.Join(c.Tokens(), "+")
}

// ParseCascadeOperations reads the tag form of a cascade declaration:
// `persist`, `remove`, or both joined with `+` (`persist+remove`), the way a
// composite foreign-key list joins. An unknown or empty token reports false.
func ParseCascadeOperations(text string) (CascadeOperations, bool) {
	var operations CascadeOperations
	for _, token := range strings.Split(text, "+") {
		switch strings.TrimSpace(token) {
		case "persist":
			operations |= CascadePersist
		case "remove":
			operations |= CascadeRemove
		default:
			return 0, false
		}
	}
	return operations, true
}

package core

import (
	"regexp"
	"strings"
)

var placeholderPattern = regexp.MustCompile(`@([A-Za-z_][A-Za-z0-9_]*)`)

// PlaceholderSpan is one real occurrence of a placeholder in SQL text: byte index and length.
type PlaceholderSpan struct {
	Index  int
	Length int
}

// placeholderMatches finds the `@name` matches of masked SQL as index pairs (the whole match, then the name). A
// placeholder is an `@` not preceded by another `@`: `@@rowcount` and `@@identity`, SQL Server's system functions, are
// never placeholders (spec/session.md "Parameters", ADR-0044) — the regular expression alone would read the second
// `@` as the start of `@rowcount`.
func placeholderMatches(masked string) [][]int {
	var matches [][]int
	for _, match := range placeholderPattern.FindAllStringSubmatchIndex(masked, -1) {
		if match[0] > 0 && masked[match[0]-1] == '@' {
			continue
		}
		matches = append(matches, match)
	}
	return matches
}

// FindPlaceholders finds the distinct @name placeholders in SQL, in first-seen
// order, ignoring lookalikes inside string literals and comments, and `@@` system
// functions (spec/session.md). The one placeholder scanner (CODING-STANDARD §8): the
// binder, the statement loader (PRM-010/011), and SchemaGuard all use it.
func FindPlaceholders(sql string) []string {
	masked := maskSQL(sql)
	seen := map[string]bool{}
	var names []string
	for _, match := range placeholderMatches(masked) {
		name := masked[match[2]:match[3]]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// PlaceholderOccurrences lists every real occurrence of one placeholder
// (case-insensitive), for rewriting (IN-list expansion) exactly as for detection.
func PlaceholderOccurrences(sql string, placeholder string) []PlaceholderSpan {
	masked := maskSQL(sql)
	var spans []PlaceholderSpan
	for _, match := range placeholderMatches(masked) {
		name := masked[match[2]:match[3]]
		if strings.EqualFold(name, placeholder) {
			spans = append(spans, PlaceholderSpan{Index: match[0], Length: match[1] - match[0]})
		}
	}
	return spans
}

// NamedPlaceholderSpan is one real occurrence of a placeholder with the name as the SQL spells it.
type NamedPlaceholderSpan struct {
	Name string
	PlaceholderSpan
}

// PlaceholderSpans lists every real occurrence of every placeholder in text order, in one scan — what
// FindPlaceholders and PlaceholderOccurrences find together (a rewriter of every placeholder would otherwise scan
// the text once per name).
func PlaceholderSpans(sql string) []NamedPlaceholderSpan {
	masked := maskSQL(sql)
	matches := placeholderMatches(masked)
	spans := make([]NamedPlaceholderSpan, len(matches))
	for i, match := range matches {
		spans[i] = NamedPlaceholderSpan{Name: masked[match[2]:match[3]], PlaceholderSpan: PlaceholderSpan{Index: match[0], Length: match[1] - match[0]}}
	}
	return spans
}

// maskSQL is a length-preserving mask: string-literal and comment bytes become
// spaces, so match positions in the mask are positions in the original SQL.
func maskSQL(sql string) string {
	out := make([]byte, 0, len(sql))
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == '\'':
			// String literal; '' is the escaped quote.
			out = append(out, ' ')
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						out = append(out, ' ', ' ')
						i += 2
						continue
					}
					out = append(out, ' ')
					i++
					break
				}
				out = append(out, ' ')
				i++
			}
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				out = append(out, ' ')
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			out = append(out, ' ', ' ')
			i += 2
			for i+1 < len(sql) && !(sql[i] == '*' && sql[i+1] == '/') {
				out = append(out, ' ')
				i++
			}
			if i+1 < len(sql) {
				out = append(out, ' ', ' ')
			} else if i < len(sql) {
				out = append(out, ' ')
			}
			i = min(i+2, len(sql))
		default:
			out = append(out, c)
			i++
		}
	}
	return string(out)
}

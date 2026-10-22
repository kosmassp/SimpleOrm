package sqlite

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// The inverse of the SQLite trigger rendering (Level 4, ADR-0042, spec/migrations.md "Snapshots"): the shadow
// replayer's history side. From metadata a trigger snapshot is the linked declaration; from history it is the
// stored SQLite trigger parsed back — header fields from the dialect's fixed grammar, the dialect's token
// expansions folded back into tokens — and the two must agree byte for byte apart from generatedAt. A
// hand-written trigger the grammar does not match is skipped with a note.

// storedTriggerPattern is the fixed grammar of what CreateTriggerSQL renders, after NormalizeTriggerDDL:
// `create trigger <n> <timing> <event>[ of <columns>] on <relation> for each row begin <body> end`.
var storedTriggerPattern = regexp.MustCompile(
	`^create trigger (\S+) (before|after|instead of) (insert|update|delete)(?: of (.+?))? on (\S+) for each row begin (.*) end$`)

// rowRelationPattern finds a row-token expansion: `(select new.a as a, new.b as b)` over new. or old.
var rowRelationPattern = regexp.MustCompile(`\(select (?:new|old)\.\w+ as \w+(?:, (?:new|old)\.\w+ as \w+)*\)`)

// ParseTriggerRows reads the rows of Dialect.TriggerDefinitionSQL for the declared trigger `name` back to its
// declaration. SQLite's per-event triggers merge: rows named `<name>_<event>` (or the bare name for a single
// event) must agree on relation, timing, and folded body, and their events union in the fixed order; the
// update-of list comes from the update row. The relation kind is not in the text — the declaration comes
// back with RelationKind empty (the caller knows whether the relation is a table or a view).
//
// The result is nil with an empty note when there are no rows (the trigger is absent), and nil with a note
// when a row does not match the grammar (a hand-written trigger, skipped).
func ParseTriggerRows(name string, rows []string) (*migrations.TriggerDeclaration, string) {
	if len(rows) == 0 {
		return nil, ""
	}

	var declaration *migrations.TriggerDeclaration
	var events core.TriggerEvents
	for _, row := range rows {
		parsed, event, note := parseStoredTrigger(name, row)
		if parsed == nil {
			return nil, note
		}
		if declaration == nil {
			declaration = parsed
		} else if declaration.Relation != parsed.Relation || declaration.Timing != parsed.Timing || declaration.SQL != parsed.SQL {
			return nil, fmt.Sprintf("trigger %s: its per-event triggers differ in relation, timing, or body; not a declaration SimpleOrm wrote", name)
		}
		if events.Has(event) {
			return nil, fmt.Sprintf("trigger %s: two stored triggers for the %s event", name, event.Token())
		}
		events |= event
		if event == core.EventUpdate {
			declaration.UpdateOf = parsed.UpdateOf
		}
	}
	declaration.Events = events
	return declaration, ""
}

// parseStoredTrigger parses one stored statement; the event it fires on comes back beside the declaration.
func parseStoredTrigger(name, row string) (*migrations.TriggerDeclaration, core.TriggerEvents, string) {
	match := storedTriggerPattern.FindStringSubmatch(migrations.NormalizeTriggerDDL(row))
	if match == nil {
		return nil, 0, fmt.Sprintf("trigger %s: the stored statement does not match the rendering grammar (a hand-written trigger); skipped", name)
	}
	storedName, timingWord, eventWord, updateOf, relation, body := match[1], match[2], match[3], match[4], match[5], match[6]

	var event core.TriggerEvents
	switch eventWord {
	case "insert":
		event = core.EventInsert
	case "update":
		event = core.EventUpdate
	default:
		event = core.EventDelete
	}
	if storedName != name && storedName != name+"_"+eventWord {
		return nil, 0, fmt.Sprintf("trigger %s: the stored trigger is named %s, which the renderer would not have given it; skipped", name, storedName)
	}

	timing := core.TimingAfter
	switch timingWord {
	case "before":
		timing = core.TimingBefore
	case "instead of":
		timing = core.TimingInsteadOf
	}

	declaration := &migrations.TriggerDeclaration{
		Name: name, Relation: relation, Timing: timing, Events: event,
		SQL: core.NormalizeTriggerBody(foldSQLiteExpansions(body)),
	}
	if updateOf != "" {
		for _, column := range strings.Split(updateOf, ",") {
			declaration.UpdateOf = append(declaration.UpdateOf, strings.TrimSpace(column))
		}
	}
	return declaration, event, ""
}

// foldSQLiteExpansions is the inverse of expandSQLiteToken: the strftime form back to `{now}`, a one-row
// derived table over new. or old. back to `{new: cols}` / `{old: cols}`. An occurrence that is not exactly the
// expansion (mixed rows, an alias that differs from its column) is left as written.
func foldSQLiteExpansions(body string) string {
	body = strings.ReplaceAll(body, sqliteNowExpansion, "{now}")
	return rowRelationPattern.ReplaceAllStringFunc(body, func(expansion string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(expansion, "(select "), ")")
		var kind string
		columns := make([]string, 0, 4)
		for _, part := range strings.Split(inner, ", ") {
			source, alias, found := strings.Cut(part, " as ")
			row, column, dotted := strings.Cut(source, ".")
			if !found || !dotted || column != alias || (kind != "" && kind != row) {
				return expansion
			}
			kind = row
			columns = append(columns, column)
		}
		return "{" + kind + ": " + strings.Join(columns, ", ") + "}"
	})
}

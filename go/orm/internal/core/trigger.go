package core

import (
	"fmt"
	"reflect"
	"strings"
)

// TriggerTiming is when a trigger fires relative to the write (Level 4, ADR-0042, spec/metadata-model.md
// "Triggers"). The zero value is no timing — a declaration that forgot it is MAP-029, never a silent default.
type TriggerTiming int

const (
	// TimingBefore fires before the write (SQLite, PostgreSQL; DDL-005 on SQL Server).
	TimingBefore TriggerTiming = iota + 1
	// TimingAfter fires after the write.
	TimingAfter
	// TimingInsteadOf replaces the write: views everywhere, tables on SQL Server only (DDL-005 elsewhere).
	TimingInsteadOf
)

// Token is the export's timing token: `before`, `after`, `instead_of`; "" for no timing.
func (t TriggerTiming) Token() string {
	switch t {
	case TimingBefore:
		return "before"
	case TimingAfter:
		return "after"
	case TimingInsteadOf:
		return "instead_of"
	}
	return ""
}

func (t TriggerTiming) String() string {
	if token := t.Token(); token != "" {
		return token
	}
	return fmt.Sprintf("TriggerTiming(%d)", int(t))
}

// ParseTriggerTiming reads an export token back; false for anything else.
func ParseTriggerTiming(token string) (TriggerTiming, bool) {
	switch token {
	case "before":
		return TimingBefore, true
	case "after":
		return TimingAfter, true
	case "instead_of":
		return TimingInsteadOf, true
	}
	return 0, false
}

// TriggerEvents is the set of write events a trigger fires on, combinable with `|`
// (`orm.EventUpdate | orm.EventDelete`) — a flag set like CascadeOperations, because one declaration may
// name several events and Go has no enum-set (CODING-STANDARD §10).
type TriggerEvents int

const (
	EventInsert TriggerEvents = 1 << iota
	EventUpdate
	EventDelete
)

// triggerEventOrder is the fixed order events are listed, exported, and rendered in.
var triggerEventOrder = []TriggerEvents{EventInsert, EventUpdate, EventDelete}

// Has reports whether every event in e is in the set.
func (t TriggerEvents) Has(e TriggerEvents) bool { return e != 0 && t&e == e }

// List splits the set into single events in the fixed order insert, update, delete.
func (t TriggerEvents) List() []TriggerEvents {
	var events []TriggerEvents
	for _, e := range triggerEventOrder {
		if t.Has(e) {
			events = append(events, e)
		}
	}
	return events
}

// Token is a single event's name: `insert`, `update`, `delete`; "" for anything else.
func (t TriggerEvents) Token() string {
	switch t {
	case EventInsert:
		return "insert"
	case EventUpdate:
		return "update"
	case EventDelete:
		return "delete"
	}
	return ""
}

// Tokens are the export's `events` values in the fixed order.
func (t TriggerEvents) Tokens() []string {
	events := t.List()
	tokens := make([]string, len(events))
	for i, e := range events {
		tokens[i] = e.Token()
	}
	return tokens
}

func (t TriggerEvents) String() string {
	if t == 0 {
		return "none"
	}
	return strings.Join(t.Tokens(), "+")
}

// ParseTriggerEvents reads the export's `events` list back; false for an unknown or repeated token.
func ParseTriggerEvents(tokens []string) (TriggerEvents, bool) {
	var events TriggerEvents
	for _, token := range tokens {
		var event TriggerEvents
		switch token {
		case "insert":
			event = EventInsert
		case "update":
			event = EventUpdate
		case "delete":
			event = EventDelete
		default:
			return 0, false
		}
		if events.Has(event) {
			return 0, false
		}
		events |= event
	}
	return events, true
}

// TriggerDef is the declaration a trigger class returns (Level 4, ADR-0042): the Go analog of the C#
// `[Trigger("name", timing, events, "body")]` on a trigger class — a name, the timing, the events, an optional
// update-of list naming properties of the linked entity, and one neutral body with the tokens `{new: cols}`,
// `{old: cols}`, `{now}` (spec/metadata-model.md "Triggers"). Nothing in it names a dialect.
type TriggerDef struct {
	Name     string
	Timing   TriggerTiming
	Events   TriggerEvents
	UpdateOf []string
	SQL      string
}

// TriggerDeclarer is implemented by a trigger class — an empty struct whose Trigger method returns its
// declaration. It is a declaration object, never an entity (spec/metadata-model.md): no columns, no key; a
// load of it is MAP-029.
//
//	type RoleAuditTrigger struct{}
//
//	func (RoleAuditTrigger) Trigger() orm.TriggerDef {
//	    return orm.TriggerDef{Name: "trg_roles_audit", Timing: orm.TimingAfter, Events: orm.EventUpdate, SQL: `…`}
//	}
type TriggerDeclarer interface {
	Trigger() TriggerDef
}

// TriggerLink is the entity's link to a trigger class (the C# `[Trigger(typeof(X))]` on the entity),
// repeatable: `Triggers: []orm.TriggerLink{orm.Trigger[RoleAuditTrigger]()}` in the descriptor.
type TriggerLink struct {
	Type reflect.Type
}

// Trigger links the trigger class T. T is unconstrained so that a type with no declaration, or one that is
// also an entity, reaches the loader and is reported as MAP-029 with every other violation rather than
// failing to compile (the C# link names any class).
func Trigger[T any]() TriggerLink { return TriggerLink{Type: reflect.TypeFor[T]()} }

var triggerDeclarerType = reflect.TypeFor[TriggerDeclarer]()

// TriggerDeclarationOf reads a trigger class's declaration from a zero value (value or pointer receiver);
// false when the type declares none.
func TriggerDeclarationOf(t reflect.Type) (TriggerDef, bool) {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct || !reflect.PointerTo(t).Implements(triggerDeclarerType) {
		return TriggerDef{}, false
	}
	return reflect.New(t).Interface().(TriggerDeclarer).Trigger(), true
}

// IsTriggerType reports whether t is a trigger class: scans skip it, and loading it is MAP-029.
func IsTriggerType(t reflect.Type) bool {
	_, ok := TriggerDeclarationOf(t)
	return ok
}

// TriggerDefinition is a declaration resolved against its entity (spec/metadata-model.md "Declared
// triggers"): UpdateOf holds column names, SQL the body as written (trimmed, tokens unexpanded) — what a
// dialect renders (Dialect.CreateTriggerSQL), so a comment or a layout the author chose survives into the
// database. The export, the snapshots, and the diff hold and compare NormalizeTriggerBody of it. It is
// neutral: nothing in it names a dialect.
type TriggerDefinition struct {
	Name     string
	Timing   TriggerTiming
	Events   TriggerEvents
	UpdateOf []string
	SQL      string
}

// SameAs compares two definitions the way the diff does: timing, events, the update-of list (in declared
// order, as it renders), and the body after normalization — a layout-only edit declares nothing. The name is
// the object's identity and is compared too.
func (d TriggerDefinition) SameAs(other TriggerDefinition) bool {
	if d.Name != other.Name || d.Timing != other.Timing || d.Events != other.Events || len(d.UpdateOf) != len(other.UpdateOf) {
		return false
	}
	for i := range d.UpdateOf {
		if d.UpdateOf[i] != other.UpdateOf[i] {
			return false
		}
	}
	return NormalizeTriggerBody(d.SQL) == NormalizeTriggerBody(other.SQL)
}

// EntityTrigger is one linked trigger on an entity's map, in link order: the declaration class and its
// resolved definition.
type EntityTrigger struct {
	// Type is the trigger class the entity links.
	Type       reflect.Type
	Definition TriggerDefinition
}

// --- the body tokens (spec/metadata-model.md "Triggers") -----------------------------------------

// TriggerTokenKind is which of the three tokens a body occurrence is.
type TriggerTokenKind int

const (
	// TokenNew is `{new: a, b}` — the inserted or updated rows as a relation.
	TokenNew TriggerTokenKind = iota + 1
	// TokenOld is `{old: a, b}` — the deleted or pre-update rows.
	TokenOld
	// TokenNow is `{now}` — UTC now in the §7.9 storage form.
	TokenNow
)

// TriggerToken is one token occurrence in a body: its kind, its column list (row tokens), and the byte span
// it occupies in the scanned text.
type TriggerToken struct {
	Kind    TriggerTokenKind
	Columns []string
	Start   int
	End     int
}

// Canonical is the token's one spelling: `{new: a, b}`, `{old: a}`, `{now}`.
func (t TriggerToken) Canonical() string {
	switch t.Kind {
	case TokenNew:
		return "{new: " + strings.Join(t.Columns, ", ") + "}"
	case TokenOld:
		return "{old: " + strings.Join(t.Columns, ", ") + "}"
	}
	return "{now}"
}

// ScanTriggerTokens finds the tokens of a body, in order, and describes every problem (MAP-029): an unknown or
// malformed token, and a `--` line comment (whitespace is collapsed in snapshots and would fold the rest of the
// body into it). A token is `{…}` with no brace inside, outside a single-quoted SQL literal — braces inside a
// literal are text, and so is a lone `{` or `}` that opens or closes no token (spec/metadata-model.md "Triggers"
// Clarifications). The kind is case-insensitive. The returned tokens exclude the malformed ones.
func ScanTriggerTokens(body string) (tokens []TriggerToken, problems []string) {
	lineComment := false
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\'':
			// A single-quoted literal; '' is the escaped quote. Unterminated, it runs to the end.
			for i++; i < len(body); i++ {
				if body[i] == '\'' {
					if i+1 < len(body) && body[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		case c == '-' && i+1 < len(body) && body[i+1] == '-':
			if !lineComment {
				lineComment = true
				problems = append(problems,
					"a body cannot carry a -- line comment (whitespace is collapsed in snapshots); use /* … */")
			}
		case c == '{':
			end := strings.IndexAny(body[i+1:], "{}")
			if end < 0 || body[i+1+end] == '{' {
				continue // a lone '{' is text
			}
			end += i + 1
			token, problem := parseTriggerToken(body[i+1:end], i, end+1)
			if problem != "" {
				problems = append(problems, problem)
			} else {
				tokens = append(tokens, token)
			}
			i = end
		}
	}
	return tokens, problems
}

func parseTriggerToken(inner string, start, end int) (TriggerToken, string) {
	spelled := "{" + inner + "}"
	name, list, hasList := strings.Cut(inner, ":")
	name = strings.ToLower(strings.TrimSpace(name))
	var kind TriggerTokenKind
	switch name {
	case "now":
		if hasList {
			return TriggerToken{}, fmt.Sprintf("the token %s: {now} takes no column list", spelled)
		}
		return TriggerToken{Kind: TokenNow, Start: start, End: end}, ""
	case "new":
		kind = TokenNew
	case "old":
		kind = TokenOld
	default:
		return TriggerToken{}, fmt.Sprintf("unknown token %s (the tokens are {new: columns}, {old: columns}, {now})", spelled)
	}
	if !hasList {
		return TriggerToken{}, fmt.Sprintf("the row token %s has no column list; write {%s: a, b}", spelled, name)
	}

	var columns []string
	for _, part := range strings.Split(list, ",") {
		column := strings.TrimSpace(part)
		if column == "" {
			return TriggerToken{}, fmt.Sprintf("the row token %s has an empty column entry", spelled)
		}
		columns = append(columns, column)
	}
	return TriggerToken{Kind: kind, Columns: columns, Start: start, End: end}, ""
}

// RenderTriggerBody is what a dialect inserts between its wrapper's lines: the body trimmed, every token
// replaced by expand's text, the rest verbatim. A declaration that reaches a dialect without passing the
// loader (the data-driven step form) is checked here by the same rules: a malformed token or a `--` comment
// is MAP-029.
func RenderTriggerBody(name, body string, expand func(TriggerToken) string) (string, error) {
	body = strings.TrimSpace(body)
	if _, problems := ScanTriggerTokens(body); len(problems) > 0 {
		return "", Errorf("MAP-029", name, "the trigger body is malformed: %s", strings.Join(problems, "; "))
	}
	return ExpandTriggerTokens(body, expand), nil
}

// ExpandTriggerTokens replaces every well-formed token of body with expand's text, leaving everything else —
// including a malformed token, which MAP-029 has already refused — as written. The dialects' one expansion
// loop: a hundredth dialect supplies expand and nothing else.
func ExpandTriggerTokens(body string, expand func(TriggerToken) string) string {
	tokens, _ := ScanTriggerTokens(body)
	if len(tokens) == 0 {
		return body
	}
	var b strings.Builder
	last := 0
	for _, token := range tokens {
		b.WriteString(body[last:token.Start])
		b.WriteString(expand(token))
		last = token.End
	}
	b.WriteString(body[last:])
	return b.String()
}

// NormalizeTriggerBody is the body as the export, the snapshots, and the diff hold it: whitespace collapsed
// (layout is not schema) and each token in its canonical spelling, still unexpanded. A body with a malformed
// token keeps its collapsed text as written.
func NormalizeTriggerBody(body string) string {
	collapsed := strings.Join(strings.Fields(body), " ")
	tokens, problems := ScanTriggerTokens(collapsed)
	if len(problems) > 0 || len(tokens) == 0 {
		return collapsed
	}
	return ExpandTriggerTokens(collapsed, func(t TriggerToken) string { return t.Canonical() })
}

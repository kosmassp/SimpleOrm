package migrations

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// RelationKindTable and RelationKindView are the `relationKind` tokens of a trigger declaration: the
// relation a trigger is on is a table or a view (spec/migrations.md "Snapshots").
const (
	RelationKindTable = "table"
	RelationKindView  = "view"
)

// TriggerDeclaration is a trigger's neutral declaration as migrations see it (Level 4, ADR-0042): the
// object (the trigger's declared name), the relation and its kind, the timing, the events, the update-of
// column names, and the body with its tokens unexpanded. It is the content of a trigger snapshot document, the
// shape the diff compares, and what a data-driven step feeds the dialect — one declaration serves every
// dialect, because nothing in it names one.
type TriggerDeclaration struct {
	Name         string
	Relation     string
	RelationKind string
	Timing       core.TriggerTiming
	Events       core.TriggerEvents
	UpdateOf     []string
	SQL          string
}

// OnView is whether the relation is a view (the `onView` argument of Dialect.CreateTriggerSQL).
func (d *TriggerDeclaration) OnView() bool { return d.RelationKind == RelationKindView }

// Definition is the declaration as the dialect renders it. The body is passed as held — a snapshot's is
// normalized, a literal step's is the author's frozen text.
func (d *TriggerDeclaration) Definition() core.TriggerDefinition {
	return core.TriggerDefinition{Name: d.Name, Timing: d.Timing, Events: d.Events, UpdateOf: d.UpdateOf, SQL: d.SQL}
}

// DeclarationFromEntity is a linked trigger as a declaration: the metadata-side producer of a trigger
// snapshot, beside FromMap for tables.
func DeclarationFromEntity(m *core.EntityMap, trigger *core.EntityTrigger) *TriggerDeclaration {
	kind := RelationKindTable
	if m.Kind == core.RelationView {
		kind = RelationKindView
	}
	definition := trigger.Definition
	return &TriggerDeclaration{
		Name: definition.Name, Relation: m.RelationName, RelationKind: kind,
		Timing: definition.Timing, Events: definition.Events, UpdateOf: definition.UpdateOf, SQL: definition.SQL,
	}
}

// SameAs is the diff's equality (spec/migrations.md "The generator toolchain"): relation, kind, timing, events,
// the update-of list, and the whitespace-normalized body — a layout-only edit declares nothing.
func (d *TriggerDeclaration) SameAs(other *TriggerDeclaration) bool {
	if d.Relation != other.Relation || d.RelationKind != other.RelationKind {
		return false
	}
	return d.Definition().SameAs(other.Definition())
}

// ExportTrigger renders the trigger snapshot document (spec/migrations.md "Snapshots"): object, kind,
// relation, relationKind, asOfVersion, generatedAt, timing, events in the fixed order, updateOf only when
// declared, and the body normalized — byte-identical from metadata and from the shadow's parse of history.
func ExportTrigger(d *TriggerDeclaration, asOfVersion int64, generatedAt time.Time) string {
	doc := core.JSONObject{}
	doc = doc.Set("object", d.Name)
	doc = doc.Set("kind", "trigger")
	doc = doc.Set("relation", d.Relation)
	doc = doc.Set("relationKind", d.RelationKind)
	doc = doc.Set("asOfVersion", asOfVersion)
	doc = doc.Set("generatedAt", core.FormatUTC(generatedAt))
	doc = doc.Set("timing", d.Timing.Token())
	events := make([]any, 0, 3)
	for _, token := range d.Events.Tokens() {
		events = append(events, token)
	}
	doc = doc.Set("events", events)
	if len(d.UpdateOf) > 0 {
		updateOf := make([]any, len(d.UpdateOf))
		for i, column := range d.UpdateOf {
			updateOf[i] = column
		}
		doc = doc.Set("updateOf", updateOf)
	}
	doc = doc.Set("sql", core.NormalizeTriggerBody(d.SQL))
	return core.CanonicalJSON(doc)
}

// triggerSnapshotJSON is the on-disk shape of a trigger snapshot.
type triggerSnapshotJSON struct {
	Object       string   `json:"object"`
	Kind         string   `json:"kind"`
	Relation     string   `json:"relation"`
	RelationKind string   `json:"relationKind"`
	AsOfVersion  int64    `json:"asOfVersion"`
	Timing       string   `json:"timing"`
	Events       []string `json:"events"`
	UpdateOf     []string `json:"updateOf"`
	SQL          string   `json:"sql"`
}

// ParseTriggerSnapshot reads a trigger snapshot document (also the diff-cases' declaration form).
func ParseTriggerSnapshot(data []byte) (*TriggerDeclaration, int64, error) {
	var doc triggerSnapshotJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("parse trigger snapshot: %w", err)
	}
	timing, ok := core.ParseTriggerTiming(doc.Timing)
	if !ok {
		return nil, 0, fmt.Errorf("parse trigger snapshot %s: unknown timing %q", doc.Object, doc.Timing)
	}
	events, ok := core.ParseTriggerEvents(doc.Events)
	if !ok || events == 0 {
		return nil, 0, fmt.Errorf("parse trigger snapshot %s: unknown or empty events %v", doc.Object, doc.Events)
	}
	kind := doc.RelationKind
	if kind == "" {
		kind = RelationKindTable
	}
	return &TriggerDeclaration{
		Name: doc.Object, Relation: doc.Relation, RelationKind: kind,
		Timing: timing, Events: events, UpdateOf: doc.UpdateOf, SQL: doc.SQL,
	}, doc.AsOfVersion, nil
}

// TriggerDiff is the generator's diff of one trigger (spec/migrations.md "The generator toolchain"): by
// declaration, neutral on both sides, nothing rendered.
type TriggerDiff struct {
	// IsNew: declared, no snapshot.
	IsNew bool
	// IsChanged: declared and snapshotted, and different.
	IsChanged bool
	// IsRemoved: snapshotted, no longer declared — gated by --allow-remove (DDL-003).
	IsRemoved bool
}

// HasChanges reports whether the diff has anything to emit.
func (d *TriggerDiff) HasChanges() bool { return d.IsNew || d.IsChanged || d.IsRemoved }

// DiffTrigger diffs a declared trigger against its latest snapshot; either side may be nil.
func DiffTrigger(current, snapshot *TriggerDeclaration) *TriggerDiff {
	switch {
	case current == nil && snapshot == nil:
		return &TriggerDiff{}
	case snapshot == nil:
		return &TriggerDiff{IsNew: true}
	case current == nil:
		return &TriggerDiff{IsRemoved: true}
	}
	return &TriggerDiff{IsChanged: !current.SameAs(snapshot)}
}

var triggerCreatePrefix = regexp.MustCompile(`(?i)^create\s+(or\s+replace\s+)?(trigger|function)\b`)

// NormalizeTriggerDDL is a stored or rendered trigger statement in comparable form (spec/migrations.md "The
// view and trigger apply guard"): whitespace collapsed and the leading `create [or replace] trigger|function`
// keywords lowercased — databases recase them when storing the text. The rest is compared as written.
func NormalizeTriggerDDL(sql string) string {
	collapsed := strings.Join(strings.Fields(sql), " ")
	match := triggerCreatePrefix.FindString(collapsed)
	if match == "" {
		return collapsed
	}
	return strings.ToLower(match) + collapsed[len(match):]
}

// TriggerStatementsMatch is the MIG-012 trigger comparison: the live rows and the expected statements, each
// normalized, are the same set — SQLite's per-event triggers read back ordered by name, not by event.
func TriggerStatementsMatch(live, expected []string) bool {
	a, b := normalizedSet(live), normalizedSet(expected)
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

func normalizedSet(statements []string) []string {
	out := make([]string, len(statements))
	for i, statement := range statements {
		out[i] = NormalizeTriggerDDL(statement)
	}
	sort.Strings(out)
	return out
}

// latestTriggerSnapshot scans dir's trigger snapshots for the one with the highest asOfVersion for which
// accept holds — the same one directory scan the table and DDL snapshots use (CODING-STANDARD §8).
func latestTriggerSnapshot(dir string, accept func(asOfVersion int64) bool) (declaration *TriggerDeclaration, atVersion int64, ok bool) {
	atVersion = -1
	for _, file := range snapshotFilesIn(dir) {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		candidate, asOfVersion, err := ParseTriggerSnapshot(data)
		if err != nil {
			continue
		}
		if accept(asOfVersion) && asOfVersion > atVersion {
			atVersion, declaration, ok = asOfVersion, candidate, true
		}
	}
	return declaration, atVersion, ok
}

// latestTriggerSnapshotBelow is the trigger's latest snapshot strictly below version, or nil.
func latestTriggerSnapshotBelow(dir string, version int64) *TriggerDeclaration {
	declaration, _, ok := latestTriggerSnapshot(dir, func(v int64) bool { return v < version })
	if !ok {
		return nil
	}
	return declaration
}

// LatestTriggerSnapshotAtOrBefore is dir's latest trigger snapshot with asOfVersion <= version — the shadow
// replayer's trusted-baseline read for triggers.
func LatestTriggerSnapshotAtOrBefore(dir string, version int64) (declaration *TriggerDeclaration, atVersion int64, ok bool) {
	return latestTriggerSnapshot(dir, func(v int64) bool { return v <= version })
}

package conformance

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"

	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// TestFlushCases_BehaveAsSpecified is the conformance/flush-cases/ runner (Level 3
// M2, spec/unit-of-work.md): the tracking-case step engine — one engine for both
// folders — with the unit-of-work verbs (persist, remove, flush, begin, commit,
// rollback), navigations on `new`, and the pending / sameValue / count
// expectations. A flush's statements are what the recording dialect was asked to
// render, in order.
//
// Every flush case runs three ways — Batching Off, Auto, and Auto with the dialect sending each message as one round
// trip (default MaxStatementsPerMessage) — and the statements, the summary, and every expectation must come out the same (spec/session.md "The step scripts", ADR-0044):
// the grouping is batch-cases'.
func TestFlushCases_BehaveAsSpecified(t *testing.T) {
	runStepCases(t, "flush-cases",
		stepVariant{name: "batching off", batching: &batchingSpec{Mode: "off"}},
		stepVariant{name: "batching auto", batching: &batchingSpec{Mode: "auto"}},
		stepVariant{name: "batching auto, one round trip per message", batching: &batchingSpec{Mode: "auto"}, oneRoundTrip: true})
}

// expectedStatement is one entry of a flush's expectStatements.
type expectedStatement struct {
	Op      string          `json:"op"`
	Entity  string          `json:"entity"`
	Columns json.RawMessage `json:"columns"`
}

// checkStatements judges an expectStatements list (absent: unchecked) against what the dialect was asked to render.
func (r *trackingRun) checkStatements(i int, raw json.RawMessage) {
	r.t.Helper()
	if len(raw) == 0 {
		return
	}
	var want []expectedStatement
	if err := json.Unmarshal(raw, &want); err != nil {
		r.fatalf(i, "expectStatements is not a list of statements: %s", raw)
	}
	got := r.dialect.Statements
	if len(got) != len(want) {
		r.fatalf(i, "expected %d statements, the dialect rendered %d: %s", len(want), len(got), describeStatements(got))
	}
	for n := range want {
		if !statementMatches(got[n], want[n]) {
			r.fatalf(i, "statement %d: expected %s %s %s, the dialect rendered %s", n+1, want[n].Op, want[n].Entity, want[n].Columns, describeStatements(got[n:n+1]))
		}
	}
}

// checkMessages judges an expectMessages list (absent: unchecked) against the message sizes the session reported —
// the plan's grouping of the step's statements, one entry per message.
func (r *trackingRun) checkMessages(i int, raw json.RawMessage) {
	r.t.Helper()
	if len(raw) == 0 {
		return
	}
	var want []int
	if err := json.Unmarshal(raw, &want); err != nil {
		r.fatalf(i, "expectMessages is not a list of statement counts: %s", raw)
	}
	got := r.dialect.Messages
	if len(got) != len(want) {
		r.fatalf(i, "expected messages %v, the session formed %v", want, got)
	}
	for n := range want {
		if got[n] != want[n] {
			r.fatalf(i, "expected messages %v, the session formed %v", want, got)
		}
	}
}

// checkFlush judges a flush step's expectStatements (absent: unchecked), expectMessages, and
// expectSummary against what the dialect rendered, the session planned, and what Flush returned.
func (r *trackingRun) checkFlush(i int, step trackingStep) {
	r.t.Helper()
	r.checkStatements(i, step.Flush.ExpectStatements)
	// A flush case run under Off carries no grouping to pin: the message plan is batch-cases'.
	r.checkMessages(i, step.Flush.ExpectMessages)
	if expected := step.Flush.ExpectSummary; expected != nil {
		if r.summary.Inserted != expected.Inserted || r.summary.Updated != expected.Updated || r.summary.Deleted != expected.Deleted {
			r.fatalf(i, "summary = %+v, want inserted %d updated %d deleted %d", r.summary, expected.Inserted, expected.Updated, expected.Deleted)
		}
	}
}

// statementMatches compares a rendered statement with an expected one; an
// update's columns are the property list, or "all" for the full-row update.
func statementMatches(got testsupport.RenderedStatement, want expectedStatement) bool {
	if got.Op != want.Op || got.Entity != want.Entity {
		return false
	}
	if want.Op != "update" || len(want.Columns) == 0 {
		return true
	}
	var all string
	if json.Unmarshal(want.Columns, &all) == nil {
		return all == "all" && got.All
	}
	var columns []string
	if json.Unmarshal(want.Columns, &columns) != nil {
		return false
	}
	return !got.All && sameNames(got.Properties, columns)
}

func describeStatements(statements []testsupport.RenderedStatement) string {
	text := "["
	for n, statement := range statements {
		if n > 0 {
			text += ", "
		}
		text += statement.Op + " " + statement.Entity
		if statement.Op == "update" {
			if statement.All {
				text += " all"
			} else {
				text += fmt.Sprintf(" %v", statement.Properties)
			}
		}
	}
	return text + "]"
}

// setNavigation assigns earlier refs to a navigation field of a new instance:
// a ref name to a pointer navigation, a list of ref names to a collection.
func (r *trackingRun) setNavigation(i int, entity any, navigation string, raw json.RawMessage) error {
	field := reflect.ValueOf(entity).Elem().FieldByName(navigation)
	if !field.IsValid() {
		return fmt.Errorf("%s has no navigation %q", reflect.TypeOf(entity).Elem().Name(), navigation)
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		field.Set(reflect.ValueOf(r.ref(i, single)))
		return nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return fmt.Errorf("navigation %q takes a ref or a list of refs: %s", navigation, raw)
	}
	collection := reflect.MakeSlice(field.Type(), 0, len(names))
	for _, name := range names {
		element := reflect.ValueOf(r.ref(i, name))
		if field.Type().Elem().Kind() != reflect.Pointer {
			element = element.Elem()
		}
		collection = reflect.Append(collection, element)
	}
	field.Set(collection)
	return nil
}

var valuePathPattern = regexp.MustCompile(`^(\w+)\.(\w+)$`)

// expectSameValue compares two "ref.Property" paths by their encoded value.
func (r *trackingRun) expectSameValue(i int, left, right string) {
	r.t.Helper()
	leftText, leftNull := r.valueOf(i, left)
	rightText, rightNull := r.valueOf(i, right)
	if leftNull != rightNull || leftText != rightText {
		r.fatalf(i, "%s = %q (null=%v), %s = %q (null=%v): expected the same value", left, leftText, leftNull, right, rightText, rightNull)
	}
}

func (r *trackingRun) valueOf(i int, path string) (string, bool) {
	r.t.Helper()
	match := valuePathPattern.FindStringSubmatch(path)
	if match == nil {
		r.fatalf(i, "malformed value path %q (ref.Property)", path)
	}
	entity := r.ref(i, match[1])
	m, err := r.db.Maps().Load(reflect.TypeOf(entity).Elem())
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	p, ok := propertyOf(m, match[2])
	if !ok {
		r.fatalf(i, "no mapped property %q on %s", match[2], m.EntityName())
	}
	return encodeForCompare(p.Get(entity), p.ColumnType)
}

// expectCount reads the relation's row count through a separate connection to the
// same file, so what a rolled-back flush left (or did not) is what the database holds.
func (r *trackingRun) expectCount(i int, entity string, want int) {
	r.t.Helper()
	ops, err := trackingOpsFor(entity)
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	m, err := r.db.Maps().Load(ops.typ)
	if err != nil {
		r.fatalf(i, "%v", err)
	}
	raw, err := sql.Open(sqlite.DriverName, sqlite.DataSourceName(r.path))
	if err != nil {
		r.fatalf(i, "open a second connection: %v", err)
	}
	defer raw.Close()

	var count int
	query := "select count(*) from " + r.db.Dialect().QuoteIdentifier(m.RelationName)
	if err := raw.QueryRowContext(r.ctx, query).Scan(&count); err != nil {
		r.fatalf(i, "count %s: %v", entity, err)
	}
	if count != want {
		r.fatalf(i, "count(%s) = %s rows, want %d", entity, strconv.Itoa(count), want)
	}
}

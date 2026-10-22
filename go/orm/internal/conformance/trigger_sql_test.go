package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
)

// triggerSQLCase is the conformance/trigger-sql/*.json shape (Level 4, ADR-0042, spec/dialects.md "Triggers"):
// a neutral trigger declaration on a relation, and per dialect either the exact create and drop statements or
// the DDL-005 refusal. The expect block is read as a map so every dialect astDialects knows is checked; a case
// lacking a shipped dialect's expectation fails (§9: all three are mandatory).
type triggerSQLCase struct {
	Name         string `json:"name"`
	Relation     string `json:"relation"`
	RelationKind string `json:"relationKind"`
	Trigger      struct {
		Name     string   `json:"name"`
		Timing   string   `json:"timing"`
		Events   []string `json:"events"`
		UpdateOf []string `json:"updateOf"`
		SQL      string   `json:"sql"`
	} `json:"trigger"`
	Expect map[string]triggerSQLExpectation `json:"expect"`
}

// triggerSQLExpectation is one dialect's pinned rendering: statements, or an error code.
type triggerSQLExpectation struct {
	Create []string `json:"create"`
	Drop   []string `json:"drop"`
	Error  string   `json:"error"`
}

// TestTriggerSQL_RendersAsSpecified is the trigger-sql/ runner (ADR-0042): render each case's declaration
// through every shipped dialect and compare the exact create and drop statements, or the DDL-005 refusal.
// Pure — no database.
func TestTriggerSQL_RendersAsSpecified(t *testing.T) {
	const folder = "trigger-sql"
	dir := testsupport.ConformanceFolder(t, folder)
	dialectNames := make([]string, 0, len(astDialects))
	for name := range astDialects {
		dialectNames = append(dialectNames, name)
	}
	sort.Strings(dialectNames)

	for _, name := range testsupport.ConformanceCases(t, folder) {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("read %s/%s: %v", folder, name, err)
			}
			var c triggerSQLCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			for _, dialectName := range dialectNames {
				t.Run(dialectName, func(t *testing.T) {
					renderTriggerSQLCase(t, c, dialectName, astDialects[dialectName]())
				})
			}
		})
	}
}

func renderTriggerSQLCase(t *testing.T, c triggerSQLCase, dialectName string, dialect core.Dialect) {
	t.Helper()
	want, ok := c.Expect[dialectName]
	if !ok {
		t.Fatalf("%s lacks the %q expectation — every dialect pins every case (§9)", c.Name, dialectName)
	}

	timing, ok := core.ParseTriggerTiming(c.Trigger.Timing)
	if !ok {
		t.Fatalf("%s: unknown timing %q", c.Name, c.Trigger.Timing)
	}
	events, ok := core.ParseTriggerEvents(c.Trigger.Events)
	if !ok {
		t.Fatalf("%s: unknown events %v", c.Name, c.Trigger.Events)
	}
	definition := core.TriggerDefinition{
		Name: c.Trigger.Name, Timing: timing, Events: events, UpdateOf: c.Trigger.UpdateOf, SQL: c.Trigger.SQL,
	}

	creates, err := dialect.CreateTriggerSQL(c.Relation, c.RelationKind == "view", definition)
	if want.Error != "" {
		if got := core.CodeOf(err); got != want.Error {
			t.Fatalf("expected error %s, got %v (statements %q)", want.Error, err, creates)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(creates, want.Create) {
		t.Errorf("create:\n got  %q\n want %q", creates, want.Create)
	}
	if drops := dialect.DropTriggerSQL(c.Relation, c.Trigger.Name); !reflect.DeepEqual(drops, want.Drop) {
		t.Errorf("drop:\n got  %q\n want %q", drops, want.Drop)
	}
}

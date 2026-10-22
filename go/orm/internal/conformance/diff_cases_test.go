package conformance

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// diffCase is conformance/diff-cases/*.json (conformance/README.md): both
// shapes in snapshot form, declared renames, and the exact expected change —
// no database, pure data (mirrors ConformanceDiffTests.cs).
type diffCase struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Current  json.RawMessage   `json:"current"`
	Snapshot json.RawMessage   `json:"snapshot"`
	Renames  map[string]string `json:"renames"`
	Expect   struct {
		IsNew          bool       `json:"isNew"`
		IsChanged      bool       `json:"isChanged"`
		IsRemoved      bool       `json:"isRemoved"`
		Added          []string   `json:"added"`
		Removed        []string   `json:"removed"`
		Renamed        [][]string `json:"renamed"`
		AddedIndexes   []string   `json:"addedIndexes"`
		RemovedIndexes []string   `json:"removedIndexes"`
		Unsupported    []string   `json:"unsupported"`
	} `json:"expect"`
}

func TestDiffCases_BehaveAsSpecified(t *testing.T) {
	for _, file := range testsupport.ConformanceCases(t, "diff-cases") {
		t.Run(file, func(t *testing.T) {
			data := testsupport.ReadConformance(t, "diff-cases", file)
			var c diffCase
			if err := json.Unmarshal(data, &c); err != nil {
				t.Fatalf("parse case: %v", err)
			}

			if c.Kind == "trigger" {
				assertTriggerDiff(t, c)
				return
			}

			current, _, err := migrations.ParseTable(c.Current)
			if err != nil {
				t.Fatalf("parse current: %v", err)
			}
			var snapshot *migrations.TableSchema
			if len(c.Snapshot) > 0 && string(c.Snapshot) != "null" {
				snapshot, _, err = migrations.ParseTable(c.Snapshot)
				if err != nil {
					t.Fatalf("parse snapshot: %v", err)
				}
			}

			diff := migrations.DiffSchemas(current, snapshot, c.Renames, declaredRenames(t, c.Current))

			if diff.IsNew != c.Expect.IsNew {
				t.Errorf("isNew: want %v, got %v", c.Expect.IsNew, diff.IsNew)
			}
			assertNames(t, "added", c.Expect.Added, columnNames(diff.Added))
			assertNames(t, "removed", c.Expect.Removed, columnNames(diff.Removed))
			assertRenamePairs(t, c.Expect.Renamed, diff.Renamed)
			assertNames(t, "removedIndexes", c.Expect.RemovedIndexes, diff.RemovedIndexNames)

			// addedIndexes: the names of the indexes the diff adds (the SQL is the dialect's rendering, ADR-0043).
			added := make([]string, len(diff.AddedIndexes))
			for i, index := range diff.AddedIndexes {
				added[i] = index.Name
			}
			assertNames(t, "addedIndexes", c.Expect.AddedIndexes, added)

			if len(c.Expect.Unsupported) != len(diff.Unsupported) {
				t.Errorf("unsupported: want %d, got %d (%v)", len(c.Expect.Unsupported), len(diff.Unsupported), diff.Unsupported)
			}
			for _, fragment := range c.Expect.Unsupported {
				if !containsSubstring(diff.Unsupported, fragment) {
					t.Errorf("expected an unsupported entry containing %q, got %v", fragment, diff.Unsupported)
				}
			}
		})
	}
}

// declaredRenames reads the model's declarations out of the case's current columns ("renamedFrom", Level 3 M5):
// the snapshot form has no field for them, so the runner feeds them as the second rename map -- old column
// name -> the column carrying the declaration.
func declaredRenames(t *testing.T, current json.RawMessage) map[string]string {
	t.Helper()
	var shape struct {
		Columns []struct {
			Column      string  `json:"column"`
			RenamedFrom *string `json:"renamedFrom"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(current, &shape); err != nil {
		t.Fatalf("parse current columns: %v", err)
	}
	declared := map[string]string{}
	for _, c := range shape.Columns {
		if c.RenamedFrom != nil {
			declared[*c.RenamedFrom] = c.Column
		}
	}
	return declared
}

func columnNames(specs []migrations.ColumnSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.Name
	}
	return out
}

func assertNames(t *testing.T, field string, want, got []string) {
	t.Helper()
	wantSorted := append([]string(nil), want...)
	gotSorted := append([]string(nil), got...)
	sort.Strings(wantSorted)
	sort.Strings(gotSorted)
	if len(wantSorted) != len(gotSorted) {
		t.Errorf("%s: want %v, got %v", field, wantSorted, gotSorted)
		return
	}
	for i := range wantSorted {
		if wantSorted[i] != gotSorted[i] {
			t.Errorf("%s: want %v, got %v", field, wantSorted, gotSorted)
			return
		}
	}
}

func assertRenamePairs(t *testing.T, want [][]string, got []migrations.ColumnRename) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("renamed: want %v, got %v", want, got)
		return
	}
	wantPairs := make([]string, len(want))
	for i, p := range want {
		wantPairs[i] = p[0] + "->" + p[1]
	}
	gotPairs := make([]string, len(got))
	for i, r := range got {
		gotPairs[i] = r.From + "->" + r.To
	}
	sort.Strings(wantPairs)
	sort.Strings(gotPairs)
	for i := range wantPairs {
		if wantPairs[i] != gotPairs[i] {
			t.Errorf("renamed: want %v, got %v", wantPairs, gotPairs)
			return
		}
	}
}

func containsSubstring(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// assertTriggerDiff is the "kind": "trigger" form (ADR-0042): both sides are trigger declarations in snapshot
// form, either null, and the expectation is isNew, isChanged, isRemoved, or nothing — by declaration, nothing
// rendered.
func assertTriggerDiff(t *testing.T, c diffCase) {
	t.Helper()
	parse := func(raw json.RawMessage, side string) *migrations.TriggerDeclaration {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		declaration, _, err := migrations.ParseTriggerSnapshot(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", side, err)
		}
		return declaration
	}

	diff := migrations.DiffTrigger(parse(c.Current, "current"), parse(c.Snapshot, "snapshot"))
	if diff.IsNew != c.Expect.IsNew || diff.IsChanged != c.Expect.IsChanged || diff.IsRemoved != c.Expect.IsRemoved {
		t.Errorf("trigger diff: want new=%v changed=%v removed=%v, got new=%v changed=%v removed=%v",
			c.Expect.IsNew, c.Expect.IsChanged, c.Expect.IsRemoved, diff.IsNew, diff.IsChanged, diff.IsRemoved)
	}
}

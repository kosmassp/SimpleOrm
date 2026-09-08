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
	Current  json.RawMessage   `json:"current"`
	Snapshot json.RawMessage   `json:"snapshot"`
	Renames  map[string]string `json:"renames"`
	Expect   struct {
		IsNew          bool       `json:"isNew"`
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

			diff := migrations.DiffSchemas(current, snapshot, c.Renames)

			if diff.IsNew != c.Expect.IsNew {
				t.Errorf("isNew: want %v, got %v", c.Expect.IsNew, diff.IsNew)
			}
			assertNames(t, "added", c.Expect.Added, columnNames(diff.Added))
			assertNames(t, "removed", c.Expect.Removed, columnNames(diff.Removed))
			assertRenamePairs(t, c.Expect.Renamed, diff.Renamed)
			assertNames(t, "removedIndexes", c.Expect.RemovedIndexes, diff.RemovedIndexNames)

			if len(c.Expect.AddedIndexes) != len(diff.AddedIndexSQL) {
				t.Errorf("addedIndexes: want %d, got %d (%v)", len(c.Expect.AddedIndexes), len(diff.AddedIndexSQL), diff.AddedIndexSQL)
			}
			for _, name := range c.Expect.AddedIndexes {
				if !containsSubstring(diff.AddedIndexSQL, name) {
					t.Errorf("expected an added-index statement containing %q, got %v", name, diff.AddedIndexSQL)
				}
			}

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
